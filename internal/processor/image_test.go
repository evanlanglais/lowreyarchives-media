package processor

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/rs/zerolog"
)

func TestIsImageWebReady(t *testing.T) {
	ip := &ImageProcessor{
		cfg: &config.Config{
			ImageMaxWidth:  2048,
			ImageMaxHeight: 2048,
		},
		logger: zerolog.Nop(),
	}

	tests := []struct {
		name string
		info *ImageInfo
		want bool
	}{
		{
			name: "web-ready JPEG within limits",
			info: &ImageInfo{Format: "jpeg", Width: 1920, Height: 1080},
			want: true,
		},
		{
			name: "JPEG at exact limits",
			info: &ImageInfo{Format: "jpeg", Width: 2048, Height: 2048},
			want: true,
		},
		{
			name: "PNG within limits",
			info: &ImageInfo{Format: "png", Width: 1920, Height: 1080},
			want: false,
		},
		{
			name: "GIF within limits",
			info: &ImageInfo{Format: "gif", Width: 640, Height: 480},
			want: false,
		},
		{
			name: "WebP within limits",
			info: &ImageInfo{Format: "webp", Width: 1920, Height: 1080},
			want: false,
		},
		{
			name: "JPEG oversized width",
			info: &ImageInfo{Format: "jpeg", Width: 3000, Height: 1080},
			want: false,
		},
		{
			name: "JPEG oversized height",
			info: &ImageInfo{Format: "jpeg", Width: 1920, Height: 3000},
			want: false,
		},
		{
			name: "JPEG both oversized",
			info: &ImageInfo{Format: "jpeg", Width: 3000, Height: 3000},
			want: false,
		},
		{
			name: "small JPEG",
			info: &ImageInfo{Format: "jpeg", Width: 100, Height: 100},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ip.isImageWebReady(tt.info)
			if got != tt.want {
				t.Errorf("isImageWebReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessWithImageMagick_GifFrameSpecifier(t *testing.T) {
	tempDir := t.TempDir()

	// Create a minimal 1x1 JPEG fixture to serve as mocked output
	fixturePath := filepath.Join(tempDir, "fixture.jpg")
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	f, err := os.Create(fixturePath)
	if err != nil {
		t.Fatalf("failed to create fixture: %v", err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		f.Close()
		t.Fatalf("failed to encode fixture: %v", err)
	}
	f.Close()

	// Create a mock convert script that records args and produces output file
	argsLogPath := filepath.Join(tempDir, "args.log")
	mockScriptPath := filepath.Join(tempDir, "mock_convert.sh")
	scriptContent := fmt.Sprintf(`#!/bin/sh
echo "$@" >> "%s"
for last; do true; done
cp "%s" "$last"
`, argsLogPath, fixturePath)

	if err := os.WriteFile(mockScriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}

	ip := &ImageProcessor{
		cfg: &config.Config{
			MagickConvertPath: mockScriptPath,
			ImageMaxWidth:     2048,
			ImageMaxHeight:    2048,
			ImageQuality:      95,
		},
		logger: zerolog.Nop(),
	}

	inputGif := filepath.Join(tempDir, "animation.gif")
	if err := os.WriteFile(inputGif, []byte("fake-gif-data"), 0644); err != nil {
		t.Fatalf("failed to write fake gif: %v", err)
	}

	outputDir := filepath.Join(tempDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}

	res, err := ip.processWithImageMagick(context.Background(), inputGif, outputDir)
	if err != nil {
		t.Fatalf("processWithImageMagick failed: %v", err)
	}

	if res == nil {
		t.Fatal("expected non-nil result")
	}

	expectedOptimized := filepath.Join(outputDir, "animation_optimized.jpg")
	if res.OptimizedPath != expectedOptimized {
		t.Errorf("got OptimizedPath %q, want %q", res.OptimizedPath, expectedOptimized)
	}

	expectedThumb := filepath.Join(outputDir, "animation_thumb.jpg")
	if res.ThumbnailPath != expectedThumb {
		t.Errorf("got ThumbnailPath %q, want %q", res.ThumbnailPath, expectedThumb)
	}

	// Verify logged args include [0] frame specifier
	logBytes, err := os.ReadFile(argsLogPath)
	if err != nil {
		t.Fatalf("failed to read args log: %v", err)
	}
	logStr := string(logBytes)

	expectedFrameArg := inputGif + "[0]"
	if !strings.Contains(logStr, expectedFrameArg) {
		t.Errorf("expected convert arguments to contain frame specifier %q, got log:\n%s", expectedFrameArg, logStr)
	}
}

func TestProcess_AnimatedGif(t *testing.T) {
	tempDir := t.TempDir()

	// Create JPEG fixture for thumbnail
	fixturePath := filepath.Join(tempDir, "fixture.jpg")
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	f, err := os.Create(fixturePath)
	if err != nil {
		t.Fatalf("failed to create fixture: %v", err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		f.Close()
		t.Fatalf("failed to encode fixture: %v", err)
	}
	f.Close()

	// Mock identify script returning 5 frames
	mockIdentifyPath := filepath.Join(tempDir, "mock_identify.sh")
	if err := os.WriteFile(mockIdentifyPath, []byte("#!/bin/sh\nprintf '5\\n5\\n5\\n5\\n5\\n'\n"), 0755); err != nil {
		t.Fatalf("failed to write mock identify: %v", err)
	}

	// Mock convert script copying fixture
	argsLogPath := filepath.Join(tempDir, "convert_args.log")
	mockConvertPath := filepath.Join(tempDir, "mock_convert.sh")
	scriptContent := fmt.Sprintf(`#!/bin/sh
echo "$@" >> "%s"
for last; do true; done
cp "%s" "$last"
`, argsLogPath, fixturePath)
	if err := os.WriteFile(mockConvertPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write mock convert: %v", err)
	}

	ip := &ImageProcessor{
		cfg: &config.Config{
			MagickIdentifyPath: mockIdentifyPath,
			MagickConvertPath:  mockConvertPath,
			ImageMaxWidth:      2048,
			ImageMaxHeight:     2048,
			ImageQuality:       95,
		},
		logger: zerolog.Nop(),
	}

	// Create valid GIF file
	inputGif := filepath.Join(tempDir, "animated.gif")
	gifFile, err := os.Create(inputGif)
	if err != nil {
		t.Fatalf("failed to create gif file: %v", err)
	}
	palette := color.Palette{color.White, color.Black}
	palImg := image.NewPaletted(image.Rect(0, 0, 50, 50), palette)
	if err := gif.Encode(gifFile, palImg, nil); err != nil {
		gifFile.Close()
		t.Fatalf("failed to encode gif: %v", err)
	}
	gifFile.Close()

	outputDir := filepath.Join(tempDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}

	res, err := ip.Process(context.Background(), inputGif, outputDir)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if !res.Skipped {
		t.Errorf("expected Skipped to be true for animated GIF, got false")
	}

	if res.OptimizedPath != "" {
		t.Errorf("expected OptimizedPath to be empty, got %q", res.OptimizedPath)
	}

	expectedThumb := filepath.Join(outputDir, "animated_thumb.jpg")
	if res.ThumbnailPath != expectedThumb {
		t.Errorf("expected ThumbnailPath %q, got %q", expectedThumb, res.ThumbnailPath)
	}

	// Check that thumbnail command used [0]
	logBytes, err := os.ReadFile(argsLogPath)
	if err != nil {
		t.Fatalf("failed to read args log: %v", err)
	}
	logStr := string(logBytes)
	expectedFrameArg := inputGif + "[0]"
	if !strings.Contains(logStr, expectedFrameArg) {
		t.Errorf("expected convert thumbnail args to contain %q, got:\n%s", expectedFrameArg, logStr)
	}
}

func TestIsAnimatedGIF(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name         string
		filename     string
		scriptOutput string
		scriptExit   int
		want         bool
	}{
		{
			name:         "animated gif (multiple frames)",
			filename:     "anim.gif",
			scriptOutput: "12\n12\n12\n",
			scriptExit:   0,
			want:         true,
		},
		{
			name:         "static gif (single frame)",
			filename:     "static.gif",
			scriptOutput: "1\n",
			scriptExit:   0,
			want:         false,
		},
		{
			name:         "uppercase extension",
			filename:     "anim.GIF",
			scriptOutput: "3\n",
			scriptExit:   0,
			want:         true,
		},
		{
			name:         "non-gif file",
			filename:     "photo.jpg",
			scriptOutput: "1\n",
			scriptExit:   0,
			want:         false,
		},
		{
			name:         "identify command failure",
			filename:     "broken.gif",
			scriptOutput: "",
			scriptExit:   1,
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scriptPath := filepath.Join(tempDir, "identify_"+tt.name+".sh")
			content := fmt.Sprintf("#!/bin/sh\nprintf '%%s' '%s'\nexit %d\n", tt.scriptOutput, tt.scriptExit)
			if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
				t.Fatalf("failed to write script: %v", err)
			}

			ip := &ImageProcessor{
				cfg: &config.Config{
					MagickIdentifyPath: scriptPath,
				},
				logger: zerolog.Nop(),
			}

			got := ip.isAnimatedGIF(context.Background(), filepath.Join(tempDir, tt.filename))
			if got != tt.want {
				t.Errorf("isAnimatedGIF() = %v, want %v", got, tt.want)
			}
		})
	}
}
