package processor

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/rs/zerolog"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	// Register image formats
	_ "golang.org/x/image/webp"
)

// ImageProcessor handles image optimization and conversion
type ImageProcessor struct {
	cfg    *config.Config
	logger zerolog.Logger
}

// NewImageProcessor creates a new image processor
func NewImageProcessor(cfg *config.Config, logger zerolog.Logger) *ImageProcessor {
	return &ImageProcessor{
		cfg:    cfg,
		logger: logger.With().Str("component", "image_processor").Logger(),
	}
}

// ImageInfo contains metadata about an image file
type ImageInfo struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
}

// ImageResult contains the results of image processing
type ImageResult struct {
	OptimizedPath string     `json:"optimized_path"`
	ThumbnailPath string     `json:"thumbnail_path"`
	Info          *ImageInfo `json:"info"`
	Skipped       bool       `json:"skipped"`
}

// isImageWebReady checks if an image is already in a web-ready format that
// doesn't need optimization. The image must be JPEG and within dimension limits.
func (p *ImageProcessor) isImageWebReady(info *ImageInfo) bool {
	if info.Format != "jpeg" {
		return false
	}
	if info.Width > p.cfg.ImageMaxWidth || info.Height > p.cfg.ImageMaxHeight {
		return false
	}
	return true
}

// Process optimizes an image file
func (p *ImageProcessor) Process(ctx context.Context, inputPath, outputDir string) (*ImageResult, error) {
	// Open and decode image config (for format detection and dimensions)
	file, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}

	imgConfig, format, err := image.DecodeConfig(file)
	file.Close()

	if err != nil {
		// Unsupported format (HEIC, RAW, etc.) — use ImageMagick
		return p.processWithImageMagick(ctx, inputPath, outputDir)
	}

	info := &ImageInfo{
		Width:  imgConfig.Width,
		Height: imgConfig.Height,
		Format: format,
	}

	p.logger.Info().
		Str("input", inputPath).
		Int("width", info.Width).
		Int("height", info.Height).
		Str("format", format).
		Msg("processing image")

	// If the image is an animated GIF, keep the original untouched for archive fidelity
	// and generate a still thumbnail from the first frame.
	if format == "gif" && p.isAnimatedGIF(ctx, inputPath) {
		p.logger.Info().
			Str("input", inputPath).
			Msg("image is an animated GIF, keeping original and generating thumbnail")

		baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		thumbnailPath := filepath.Join(outputDir, baseName+"_thumb.jpg")

		thumbCmd := exec.CommandContext(ctx, p.cfg.MagickConvertPath,
			inputPath+"[0]",
			"-auto-orient",
			"-thumbnail", "400x400>",
			"-quality", "80",
			"-strip",
			thumbnailPath,
		)
		if err := thumbCmd.Run(); err != nil {
			p.logger.Warn().Err(err).Msg("failed to generate thumbnail for animated GIF")
			thumbnailPath = ""
		}

		return &ImageResult{
			ThumbnailPath: thumbnailPath,
			Info:          info,
			Skipped:       true,
		}, nil
	}

	// Check if image is web-ready AND has correct EXIF orientation.
	// Images with non-standard orientation need processing even if otherwise web-ready,
	// because Go's image.Decode (and most web browsers inconsistently) ignores EXIF orientation.
	if p.isImageWebReady(info) && !p.needsOrientationCorrection(ctx, inputPath) {
		p.logger.Info().
			Str("format", info.Format).
			Int("width", info.Width).
			Int("height", info.Height).
			Msg("image is web-ready with correct orientation, skipping optimization")

		// Generate thumbnail using ImageMagick for consistency
		baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		thumbnailPath := filepath.Join(outputDir, baseName+"_thumb.jpg")

		thumbCmd := exec.CommandContext(ctx, p.cfg.MagickConvertPath,
			inputPath+"[0]",
			"-thumbnail", "400x400>",
			"-quality", "80",
			"-strip",
			thumbnailPath,
		)
		if err := thumbCmd.Run(); err != nil {
			p.logger.Warn().Err(err).Msg("failed to generate thumbnail")
			thumbnailPath = ""
		}

		return &ImageResult{
			ThumbnailPath: thumbnailPath,
			Info:          info,
			Skipped:       true,
		}, nil
	}

	// Process through ImageMagick for correct EXIF orientation handling
	return p.processWithImageMagick(ctx, inputPath, outputDir)
}

// isAnimatedGIF checks if an image is an animated GIF with multiple frames
// using ImageMagick identify (frame count > 1).
func (p *ImageProcessor) isAnimatedGIF(ctx context.Context, path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".gif" {
		return false
	}

	cmd := exec.CommandContext(ctx, p.cfg.MagickIdentifyPath, "-format", "%n\n", path)
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return false
	}

	count, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return false
	}

	return count > 1
}

// needsOrientationCorrection checks if an image has a non-standard EXIF orientation
// that requires auto-orient to display correctly (e.g., iPhone portrait photos).
func (p *ImageProcessor) needsOrientationCorrection(ctx context.Context, path string) bool {
	cmd := exec.CommandContext(ctx, p.cfg.MagickIdentifyPath, "-format", "%[EXIF:Orientation]", path)
	output, err := cmd.Output()
	if err != nil {
		return false // Can't determine — assume no correction needed
	}
	orient := strings.TrimSpace(string(output))
	// EXIF Orientation 1 = normal; empty/undefined = no EXIF orientation tag = normal
	// Any other value (3=180°, 6=90°CW, 8=90°CCW, etc.) needs correction
	return orient != "" && orient != "1" && orient != "undefined"
}

// processWithImageMagick handles image processing with correct EXIF orientation
func (p *ImageProcessor) processWithImageMagick(ctx context.Context, inputPath, outputDir string) (*ImageResult, error) {
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	thumbnailPath := filepath.Join(outputDir, baseName+"_thumb.jpg")

	// Check if animated GIF: keep original untouched and generate still thumbnail from first frame.
	if p.isAnimatedGIF(ctx, inputPath) {
		p.logger.Info().
			Str("input", inputPath).
			Msg("image is an animated GIF, keeping original and generating thumbnail")

		thumbCmd := exec.CommandContext(ctx, p.cfg.MagickConvertPath,
			inputPath+"[0]",
			"-auto-orient",
			"-thumbnail", "400x400>",
			"-quality", "80",
			"-strip",
			thumbnailPath,
		)
		if err := thumbCmd.Run(); err != nil {
			p.logger.Warn().Err(err).Msg("failed to generate thumbnail for animated GIF")
			thumbnailPath = ""
		}

		return &ImageResult{
			ThumbnailPath: thumbnailPath,
			Info:          &ImageInfo{Format: "gif"},
			Skipped:       true,
		}, nil
	}

	outputPath := filepath.Join(outputDir, baseName+"_optimized.jpg")

	// Frame specifier [0] ensures multi-frame images (such as static GIFs or single-frame files)
	// produce a single output file instead of frame-suffixed files (e.g. name-0.jpg, name-1.jpg).
	inputWithFrame := inputPath + "[0]"

	// Use ImageMagick convert for the main image
	args := []string{
		inputWithFrame,
		"-auto-orient",
		"-resize", fmt.Sprintf("%dx%d>", p.cfg.ImageMaxWidth, p.cfg.ImageMaxHeight),
		"-quality", fmt.Sprintf("%d", p.cfg.ImageQuality),
		"-strip", // Remove metadata
		outputPath,
	}

	cmd := exec.CommandContext(ctx, p.cfg.MagickConvertPath, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ImageMagick convert failed: %w", err)
	}

	// Generate thumbnail
	thumbArgs := []string{
		inputWithFrame,
		"-auto-orient",
		"-thumbnail", "400x400>",
		"-quality", "80",
		"-strip",
		thumbnailPath,
	}

	thumbCmd := exec.CommandContext(ctx, p.cfg.MagickConvertPath, thumbArgs...)
	if err := thumbCmd.Run(); err != nil {
		p.logger.Warn().Err(err).Msg("failed to generate thumbnail with ImageMagick")
		thumbnailPath = ""
	}

	// Get info from the output file
	outputFile, err := os.Open(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open optimized image: %w", err)
	}
	defer outputFile.Close()

	imgConfig, format, err := image.DecodeConfig(outputFile)
	if err != nil {
		// Return with unknown dimensions if we can't decode
		return &ImageResult{
			OptimizedPath: outputPath,
			ThumbnailPath: thumbnailPath,
			Info:          &ImageInfo{Format: "jpeg"},
		}, nil
	}

	return &ImageResult{
		OptimizedPath: outputPath,
		ThumbnailPath: thumbnailPath,
		Info: &ImageInfo{
			Width:  imgConfig.Width,
			Height: imgConfig.Height,
			Format: format,
		},
	}, nil
}
