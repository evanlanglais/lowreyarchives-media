package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/rs/zerolog"
)

// VideoProcessor handles video transcoding and optimization
type VideoProcessor struct {
	cfg    *config.Config
	logger zerolog.Logger
}

// NewVideoProcessor creates a new video processor
func NewVideoProcessor(cfg *config.Config, logger zerolog.Logger) *VideoProcessor {
	return &VideoProcessor{
		cfg:    cfg,
		logger: logger.With().Str("component", "video_processor").Logger(),
	}
}

// VideoInfo contains metadata about a video file
type VideoInfo struct {
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Duration     float64 `json:"duration"`
	Bitrate      int64   `json:"bitrate"`
	Codec        string  `json:"codec"`
	FrameRate    float64 `json:"frame_rate"`
	HasAudio     bool    `json:"has_audio"`
	AudioCodec   string  `json:"audio_codec,omitempty"`
	IsInterlaced bool    `json:"is_interlaced"`
	FieldOrder   string  `json:"field_order,omitempty"` // "tt", "bb", "tb", "bt", "progressive", "tff", "bff"
}

// ProcessResult contains the results of video processing
type ProcessResult struct {
	OptimizedPath string     `json:"optimized_path"`
	ThumbnailPath string     `json:"thumbnail_path"`
	Info          *VideoInfo `json:"info"`
}

// GetVideoInfo extracts metadata from a video file using ffprobe
func (p *VideoProcessor) GetVideoInfo(ctx context.Context, inputPath string) (*VideoInfo, error) {
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	}

	cmd := exec.CommandContext(ctx, p.cfg.FFprobePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var probeResult struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
			FieldOrder string `json:"field_order"` // "progressive", "tt", "bb", "tb", "bt", or empty
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
	}

	if err := json.Unmarshal(output, &probeResult); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	info := &VideoInfo{}

	// Parse video stream info
	for _, stream := range probeResult.Streams {
		if stream.CodecType == "video" {
			info.Width = stream.Width
			info.Height = stream.Height
			info.Codec = stream.CodecName
			// Parse frame rate (e.g., "30/1" -> 30.0)
			if parts := strings.Split(stream.RFrameRate, "/"); len(parts) == 2 {
				num, _ := strconv.ParseFloat(parts[0], 64)
				den, _ := strconv.ParseFloat(parts[1], 64)
				if den > 0 {
					info.FrameRate = num / den
				}
			}
			// Check for interlacing - first try metadata
			info.FieldOrder = stream.FieldOrder
			info.IsInterlaced = isInterlacedFieldOrder(stream.FieldOrder)
		} else if stream.CodecType == "audio" {
			info.HasAudio = true
			info.AudioCodec = stream.CodecName
		}
	}

	// Parse format info
	if probeResult.Format.Duration != "" {
		info.Duration, _ = strconv.ParseFloat(probeResult.Format.Duration, 64)
	}
	if probeResult.Format.BitRate != "" {
		info.Bitrate, _ = strconv.ParseInt(probeResult.Format.BitRate, 10, 64)
	}

	// If metadata didn't indicate interlacing, analyze actual frame content
	// Many old captured videos (VHS, DV, analog) lack correct field_order metadata
	if !info.IsInterlaced {
		detected, fieldOrder, err := p.detectInterlaceByContent(ctx, inputPath)
		if err != nil {
			p.logger.Warn().Err(err).Msg("content-based interlace detection failed, relying on metadata")
		} else if detected {
			p.logger.Info().
				Str("detected_field_order", fieldOrder).
				Str("metadata_field_order", info.FieldOrder).
				Msg("content analysis detected interlacing missed by metadata")
			info.IsInterlaced = true
			if info.FieldOrder == "" || info.FieldOrder == "progressive" || info.FieldOrder == "unknown" {
				info.FieldOrder = fieldOrder
			}
		}
	}

	return info, nil
}

// isInterlacedFieldOrder checks if the field_order indicates interlaced content
func isInterlacedFieldOrder(fieldOrder string) bool {
	switch fieldOrder {
	case "tt", "tb", "bt", "bb": // top-top, top-bottom, bottom-top, bottom-bottom
		return true
	case "progressive", "", "unknown":
		return false
	default:
		return false
	}
}

// detectInterlaceByContent uses ffmpeg's idet filter to analyze actual frame content
// for interlacing patterns. This catches interlaced content where container metadata
// is missing or incorrect (common with old VHS captures, DV imports, etc.)
func (p *VideoProcessor) detectInterlaceByContent(ctx context.Context, inputPath string) (bool, string, error) {
	// Analyze up to 500 frames with the idet filter
	args := []string{
		"-i", inputPath,
		"-vf", "idet",
		"-frames:v", "500",
		"-an",
		"-f", "null",
		"-",
	}

	cmd := exec.CommandContext(ctx, p.cfg.FFmpegPath, args...)
	// idet outputs to stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, "", fmt.Errorf("idet detection failed: %w", err)
	}

	return parseIdetOutput(string(output))
}

// parseIdetOutput parses the idet filter output to determine interlacing
func parseIdetOutput(output string) (bool, string, error) {
	// idet outputs lines like:
	// [Parsed_idet_0 @ 0x...] Repeated Fields: Neither: 500 Top:     0 Bottom:     0
	// [Parsed_idet_0 @ 0x...] Single frame detection: TFF:   250 BFF:     0 Progressive:   200 Undetermined:    50
	// [Parsed_idet_0 @ 0x...] Multi frame detection: TFF:   300 BFF:     0 Progressive:   150 Undetermined:    50

	var tff, bff, progressive, undetermined int

	// Parse "Multi frame detection" line (most reliable)
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "Multi frame detection") {
			continue
		}

		// Extract counts from the line
		tff = extractIdetCount(line, "TFF:")
		bff = extractIdetCount(line, "BFF:")
		progressive = extractIdetCount(line, "Progressive:")
		undetermined = extractIdetCount(line, "Undetermined:")
		break
	}

	total := tff + bff + progressive + undetermined
	if total == 0 {
		return false, "", fmt.Errorf("no idet statistics found in output")
	}

	interlaced := tff + bff
	// Consider interlaced if more than 30% of analyzed frames show interlacing
	// This threshold avoids false positives from occasional combing artifacts
	// while still catching genuinely interlaced content
	ratio := float64(interlaced) / float64(total)
	if ratio > 0.30 {
		fieldOrder := "tff"
		if bff > tff {
			fieldOrder = "bff"
		}
		return true, fieldOrder, nil
	}

	return false, "", nil
}

// extractIdetCount extracts the numeric count after a label in an idet output line
func extractIdetCount(line, label string) int {
	idx := strings.Index(line, label)
	if idx < 0 {
		return 0
	}
	rest := strings.TrimSpace(line[idx+len(label):])
	// Take the first whitespace-delimited token
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return 0
	}
	val, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return val
}

// Process transcodes a video file to an optimized format
func (p *VideoProcessor) Process(ctx context.Context, inputPath, outputDir string) (*ProcessResult, error) {
	// Get video info first
	info, err := p.GetVideoInfo(ctx, inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	p.logger.Info().
		Str("input", inputPath).
		Int("width", info.Width).
		Int("height", info.Height).
		Float64("duration", info.Duration).
		Bool("interlaced", info.IsInterlaced).
		Str("field_order", info.FieldOrder).
		Msg("processing video")

	// Generate output filename with _optimized suffix to ensure uniqueness
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(outputDir, baseName+"_optimized."+p.cfg.VideoOutputFormat)
	thumbnailPath := filepath.Join(outputDir, baseName+"_thumb.jpg")

	// Calculate target dimensions (maintain aspect ratio, cap at max)
	targetWidth, targetHeight := p.calculateTargetDimensions(info.Width, info.Height)

	// Build ffmpeg arguments for transcoding
	args := p.buildTranscodeArgs(inputPath, outputPath, targetWidth, targetHeight, info.HasAudio, info.IsInterlaced)

	p.logger.Debug().Strs("args", args).Msg("running ffmpeg transcode")

	cmd := exec.CommandContext(ctx, p.cfg.FFmpegPath, args...)
	cmd.Stderr = os.Stderr // Allow ffmpeg to output progress
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg transcode failed: %w", err)
	}

	// Generate thumbnail (use deinterlaced frame if source is interlaced)
	if err := p.generateThumbnail(ctx, inputPath, thumbnailPath, info.Duration, info.IsInterlaced); err != nil {
		p.logger.Warn().Err(err).Msg("failed to generate thumbnail, continuing without it")
		thumbnailPath = ""
	}

	return &ProcessResult{
		OptimizedPath: outputPath,
		ThumbnailPath: thumbnailPath,
		Info:          info,
	}, nil
}

// calculateTargetDimensions calculates the target dimensions while maintaining aspect ratio
func (p *VideoProcessor) calculateTargetDimensions(width, height int) (int, int) {
	maxW := p.cfg.MaxVideoWidth
	maxH := p.cfg.MaxVideoHeight

	// If already within limits, keep original
	if width <= maxW && height <= maxH {
		// Ensure even dimensions for video encoding
		return width - (width % 2), height - (height % 2)
	}

	// Calculate scaling factor
	scaleW := float64(maxW) / float64(width)
	scaleH := float64(maxH) / float64(height)
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}

	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)

	// Ensure even dimensions
	return newWidth - (newWidth % 2), newHeight - (newHeight % 2)
}

// buildTranscodeArgs builds the ffmpeg arguments for transcoding
func (p *VideoProcessor) buildTranscodeArgs(input, output string, width, height int, hasAudio, isInterlaced bool) []string {
	// Build video filter chain
	var filters []string

	// Add deinterlacing filter first if needed (before scaling)
	// Using yadif (Yet Another DeInterlacing Filter) which is good quality and reasonably fast
	// mode=1 means output one frame for each field (doubles framerate for smoother motion)
	// mode=0 would output one frame for each frame (keeps original framerate)
	if isInterlaced {
		filters = append(filters, "yadif=mode=0:parity=-1:deint=1")
	}

	// Add scaling filter
	filters = append(filters, fmt.Sprintf("scale=%d:%d", width, height))

	// Combine filters with comma separator
	filterChain := strings.Join(filters, ",")

	args := []string{
		"-y",                  // Overwrite output
		"-i", input,           // Input file
		"-c:v", p.cfg.VideoCodec,
		"-preset", p.cfg.VideoPreset,
		"-crf", p.cfg.VideoCRF,
		"-vf", filterChain,
		"-movflags", "+faststart", // Enable streaming
		"-pix_fmt", "yuv420p",     // Compatibility
	}

	if hasAudio {
		args = append(args,
			"-c:a", p.cfg.AudioCodec,
			"-b:a", "128k",
		)
	} else {
		args = append(args, "-an")
	}

	args = append(args, output)
	return args
}

// generateThumbnail creates a thumbnail from the video
func (p *VideoProcessor) generateThumbnail(ctx context.Context, input, output string, duration float64, isInterlaced bool) error {
	// Take thumbnail from 10% into the video, or at 1 second for short videos
	seekTime := duration * 0.1
	if seekTime < 1 {
		seekTime = 1
	}
	if seekTime > duration {
		seekTime = 0
	}

	// Build filter chain for thumbnail
	var filters []string
	if isInterlaced {
		// Deinterlace the frame to avoid combing artifacts in thumbnail
		filters = append(filters, "yadif=mode=0:parity=-1:deint=1")
	}
	filters = append(filters, fmt.Sprintf("scale=%d:-1", p.cfg.ImageMaxWidth/2)) // Half size for thumbnails
	filterChain := strings.Join(filters, ",")

	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.2f", seekTime),
		"-i", input,
		"-vframes", "1",
		"-q:v", "2",
		"-vf", filterChain,
		output,
	}

	cmd := exec.CommandContext(ctx, p.cfg.FFmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("thumbnail generation failed: %w", err)
	}
	return nil
}

// GenerateHLSPlaylist creates an HLS playlist for adaptive streaming (optional advanced feature)
func (p *VideoProcessor) GenerateHLSPlaylist(ctx context.Context, inputPath, outputDir string) (string, error) {
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	playlistPath := filepath.Join(outputDir, baseName+".m3u8")

	args := []string{
		"-y",
		"-i", inputPath,
		"-c:v", p.cfg.VideoCodec,
		"-preset", p.cfg.VideoPreset,
		"-crf", p.cfg.VideoCRF,
		"-c:a", p.cfg.AudioCodec,
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", "10",
		"-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(outputDir, baseName+"_%03d.ts"),
		playlistPath,
	}

	cmd := exec.CommandContext(ctx, p.cfg.FFmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("HLS generation failed: %w", err)
	}

	return playlistPath, nil
}
