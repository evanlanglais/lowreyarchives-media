package processor

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/rs/zerolog"

	// Register image formats
	_ "image/gif"
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
}

// Process optimizes an image file
func (p *ImageProcessor) Process(ctx context.Context, inputPath, outputDir string) (*ImageResult, error) {
	// Open and decode the image
	file, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}
	defer file.Close()

	// Decode image configuration first (to get dimensions without full decode)
	imgConfig, format, err := image.DecodeConfig(file)
	if err != nil {
		// Try using ImageMagick for unsupported formats (HEIC, RAW, etc.)
		return p.processWithImageMagick(ctx, inputPath, outputDir)
	}

	// Reset file pointer
	file.Seek(0, 0)

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

	// Generate output paths
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(outputDir, baseName+"_optimized.jpg")
	thumbnailPath := filepath.Join(outputDir, baseName+"_thumb.jpg")

	// Decode the full image
	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Calculate target dimensions
	targetWidth, targetHeight := p.calculateTargetDimensions(info.Width, info.Height)

	// Resize if needed
	if targetWidth != info.Width || targetHeight != info.Height {
		img = p.resize(img, targetWidth, targetHeight)
	}

	// Save optimized version
	if err := p.saveJPEG(img, outputPath, p.cfg.ImageQuality); err != nil {
		return nil, fmt.Errorf("failed to save optimized image: %w", err)
	}

	// Generate thumbnail
	thumbWidth, thumbHeight := p.calculateThumbnailDimensions(info.Width, info.Height)
	thumb := p.resize(img, thumbWidth, thumbHeight)
	if err := p.saveJPEG(thumb, thumbnailPath, 80); err != nil {
		p.logger.Warn().Err(err).Msg("failed to generate thumbnail")
		thumbnailPath = ""
	}

	return &ImageResult{
		OptimizedPath: outputPath,
		ThumbnailPath: thumbnailPath,
		Info:          info,
	}, nil
}

// processWithImageMagick handles formats not supported by Go's image package
func (p *ImageProcessor) processWithImageMagick(ctx context.Context, inputPath, outputDir string) (*ImageResult, error) {
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(outputDir, baseName+"_optimized.jpg")
	thumbnailPath := filepath.Join(outputDir, baseName+"_thumb.jpg")

	// Use ImageMagick convert for the main image
	args := []string{
		inputPath,
		"-auto-orient",
		"-resize", fmt.Sprintf("%dx%d>", p.cfg.ImageMaxWidth, p.cfg.ImageMaxHeight),
		"-quality", fmt.Sprintf("%d", p.cfg.ImageQuality),
		"-strip", // Remove metadata
		outputPath,
	}

	cmd := exec.CommandContext(ctx, "convert", args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ImageMagick convert failed: %w", err)
	}

	// Generate thumbnail
	thumbArgs := []string{
		inputPath,
		"-auto-orient",
		"-thumbnail", "400x400>",
		"-quality", "80",
		"-strip",
		thumbnailPath,
	}

	thumbCmd := exec.CommandContext(ctx, "convert", thumbArgs...)
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

// calculateTargetDimensions calculates target dimensions maintaining aspect ratio
func (p *ImageProcessor) calculateTargetDimensions(width, height int) (int, int) {
	maxW := p.cfg.ImageMaxWidth
	maxH := p.cfg.ImageMaxHeight

	if width <= maxW && height <= maxH {
		return width, height
	}

	scaleW := float64(maxW) / float64(width)
	scaleH := float64(maxH) / float64(height)
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}

	return int(float64(width) * scale), int(float64(height) * scale)
}

// calculateThumbnailDimensions calculates thumbnail dimensions
func (p *ImageProcessor) calculateThumbnailDimensions(width, height int) (int, int) {
	const maxThumb = 400

	if width <= maxThumb && height <= maxThumb {
		return width, height
	}

	scale := float64(maxThumb) / float64(width)
	if float64(maxThumb)/float64(height) < scale {
		scale = float64(maxThumb) / float64(height)
	}

	return int(float64(width) * scale), int(float64(height) * scale)
}

// resize performs simple nearest-neighbor resizing
// For production, consider using a proper resizing library like "github.com/nfnt/resize"
func (p *ImageProcessor) resize(img image.Image, width, height int) image.Image {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX := x * srcW / width
			srcY := y * srcH / height
			dst.Set(x, y, img.At(bounds.Min.X+srcX, bounds.Min.Y+srcY))
		}
	}

	return dst
}

// saveJPEG saves an image as JPEG with the given quality
func (p *ImageProcessor) saveJPEG(img image.Image, path string, quality int) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return jpeg.Encode(file, img, &jpeg.Options{Quality: quality})
}

// savePNG saves an image as PNG
func (p *ImageProcessor) savePNG(img image.Image, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return png.Encode(file, img)
}

// EncodeJPEGToBuffer encodes an image to a JPEG buffer
func (p *ImageProcessor) EncodeJPEGToBuffer(img image.Image, quality int) (io.Reader, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return &buf, nil
}
