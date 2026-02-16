package processor

import (
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
