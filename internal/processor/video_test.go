package processor

import (
	"testing"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/rs/zerolog"
)

func TestParseIdetOutput(t *testing.T) {
	tests := []struct {
		name          string
		output        string
		wantDetected  bool
		wantField     string
		wantErr       bool
	}{
		{
			name: "DV capture with BFF interlacing (real output)",
			output: `ffmpeg version 8.0.1 Copyright (c) 2000-2025 the FFmpeg developers
  built with Apple clang version 17.0.0 (clang-1700.6.3.2)
[aist#0:1/pcm_s16le @ 0x126e07060] Guessed Channel Layout: stereo
Input #0, avi, from '1998 dec 24.avi':
  Duration: 00:07:04.16, start: 0.000000, bitrate: 30316 kb/s
  Stream #0:0: Video: dvvideo (dvsd / 0x64737664), yuv411p, 720x480 [SAR 8:9 DAR 4:3], 28771 kb/s, 29.97 fps, 29.97 tbr, 29.97 tbn
  Stream #0:1: Audio: pcm_s16le ([1][0][0][0] / 0x0001), 48000 Hz, stereo, s16, 1536 kb/s
[Parsed_idet_0 @ 0x600003bb0300] Repeated Fields: Neither:     0 Top:     0 Bottom:     0
[Parsed_idet_0 @ 0x600003bb0300] Single frame detection: TFF:     0 BFF:     0 Progressive:     0 Undetermined:     0
[Parsed_idet_0 @ 0x600003bb0300] Multi frame detection: TFF:     0 BFF:     0 Progressive:     0 Undetermined:     0
Stream mapping:
  Stream #0:0 -> #0:0 (dvvideo (native) -> wrapped_avframe (native))
Press [q] to stop, [?] for help
Output #0, null, to 'pipe:':
  Metadata:
    encoder         : Lavf62.3.100
  Stream #0:0: Video: wrapped_avframe, yuv411p(bottom coded first (swapped)), 720x480 [SAR 8:9 DAR 4:3], q=2-31, 200 kb/s, 29.97 fps, 29.97 tbn
    Metadata:
      encoder         : Lavc62.11.100 wrapped_avframe
[Parsed_idet_0 @ 0x600003bb4000] Repeated Fields: Neither:   501 Top:     0 Bottom:     0
[Parsed_idet_0 @ 0x600003bb4000] Single frame detection: TFF:     0 BFF:   501 Progressive:     0 Undetermined:     0
[Parsed_idet_0 @ 0x600003bb4000] Multi frame detection: TFF:     0 BFF:   501 Progressive:     0 Undetermined:     0
[out#0/null @ 0x600003bb0240] video:203KiB audio:0KiB subtitle:0KiB other streams:0KiB global headers:0KiB muxing overhead: unknown
frame=  500 fps=0.0 q=-0.0 Lsize=N/A time=00:00:16.68 bitrate=N/A speed=37.6x elapsed=0:00:00.44`,
			wantDetected: true,
			wantField:    "bff",
		},
		{
			name: "TFF interlaced content",
			output: `[Parsed_idet_0 @ 0x1234] Multi frame detection: TFF:     0 BFF:     0 Progressive:     0 Undetermined:     0
[Parsed_idet_0 @ 0x5678] Multi frame detection: TFF:   400 BFF:     0 Progressive:    50 Undetermined:    50`,
			wantDetected: true,
			wantField:    "tff",
		},
		{
			name: "progressive content",
			output: `[Parsed_idet_0 @ 0x1234] Multi frame detection: TFF:     0 BFF:     0 Progressive:     0 Undetermined:     0
[Parsed_idet_0 @ 0x5678] Multi frame detection: TFF:     0 BFF:     0 Progressive:   480 Undetermined:    20`,
			wantDetected: false,
			wantField:    "",
		},
		{
			name: "mixed content below threshold",
			output: `[Parsed_idet_0 @ 0x1234] Multi frame detection: TFF:     0 BFF:     0 Progressive:     0 Undetermined:     0
[Parsed_idet_0 @ 0x5678] Multi frame detection: TFF:    50 BFF:     0 Progressive:   400 Undetermined:    50`,
			wantDetected: false,
			wantField:    "",
		},
		{
			name: "mixed content above threshold",
			output: `[Parsed_idet_0 @ 0x1234] Multi frame detection: TFF:     0 BFF:     0 Progressive:     0 Undetermined:     0
[Parsed_idet_0 @ 0x5678] Multi frame detection: TFF:   200 BFF:     0 Progressive:   250 Undetermined:    50`,
			wantDetected: true,
			wantField:    "tff",
		},
		{
			name:    "no idet output at all",
			output:  `ffmpeg version 8.0.1\nsome other output`,
			wantErr: true,
		},
		{
			name: "only zeroed initial summary",
			output: `[Parsed_idet_0 @ 0x1234] Multi frame detection: TFF:     0 BFF:     0 Progressive:     0 Undetermined:     0`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, fieldOrder, err := parseIdetOutput(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIdetOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if detected != tt.wantDetected {
				t.Errorf("parseIdetOutput() detected = %v, want %v", detected, tt.wantDetected)
			}
			if fieldOrder != tt.wantField {
				t.Errorf("parseIdetOutput() fieldOrder = %q, want %q", fieldOrder, tt.wantField)
			}
		})
	}
}

func TestExtractIdetCount(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		label string
		want  int
	}{
		{
			name:  "BFF with extra whitespace",
			line:  "[Parsed_idet_0 @ 0x5678] Multi frame detection: TFF:     0 BFF:   501 Progressive:     0 Undetermined:     0",
			label: "BFF:",
			want:  501,
		},
		{
			name:  "TFF value",
			line:  "[Parsed_idet_0 @ 0x5678] Multi frame detection: TFF:   400 BFF:     0 Progressive:    50 Undetermined:    50",
			label: "TFF:",
			want:  400,
		},
		{
			name:  "label not present",
			line:  "some random line",
			label: "TFF:",
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractIdetCount(tt.line, tt.label)
			if got != tt.want {
				t.Errorf("extractIdetCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsVideoWebReady(t *testing.T) {
	vp := &VideoProcessor{
		cfg: &config.Config{
			MaxVideoWidth:  1920,
			MaxVideoHeight: 1080,
		},
		logger: zerolog.Nop(),
	}

	tests := []struct {
		name      string
		info      *VideoInfo
		inputPath string
		want      bool
	}{
		{
			name:      "web-ready 1080p landscape",
			info:      &VideoInfo{Codec: "h264", Width: 1920, Height: 1080, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      true,
		},
		{
			name:      "web-ready 720p",
			info:      &VideoInfo{Codec: "h264", Width: 1280, Height: 720, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      true,
		},
		{
			name:      "portrait within limits",
			info:      &VideoInfo{Codec: "h264", Width: 1080, Height: 1920, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      true,
		},
		{
			name:      "wrong codec hevc",
			info:      &VideoInfo{Codec: "hevc", Width: 1920, Height: 1080, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      false,
		},
		{
			name:      "wrong container mov",
			info:      &VideoInfo{Codec: "h264", Width: 1920, Height: 1080, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mov",
			want:      false,
		},
		{
			name:      "wrong container avi",
			info:      &VideoInfo{Codec: "h264", Width: 1920, Height: 1080, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.avi",
			want:      false,
		},
		{
			name:      "oversized width",
			info:      &VideoInfo{Codec: "h264", Width: 2560, Height: 1440, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      false,
		},
		{
			name:      "oversized portrait",
			info:      &VideoInfo{Codec: "h264", Width: 1080, Height: 2160, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      false,
		},
		{
			name:      "interlaced",
			info:      &VideoInfo{Codec: "h264", Width: 1920, Height: 1080, IsInterlaced: true, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      false,
		},
		{
			name:      "wrong pix_fmt yuv422p",
			info:      &VideoInfo{Codec: "h264", Width: 1920, Height: 1080, PixFmt: "yuv422p"},
			inputPath: "/tmp/video.mp4",
			want:      false,
		},
		{
			name:      "wrong pix_fmt yuv420p10le",
			info:      &VideoInfo{Codec: "h264", Width: 1920, Height: 1080, PixFmt: "yuv420p10le"},
			inputPath: "/tmp/video.mp4",
			want:      false,
		},
		{
			name:      "all wrong",
			info:      &VideoInfo{Codec: "hevc", Width: 3840, Height: 2160, IsInterlaced: true, PixFmt: "yuv422p"},
			inputPath: "/tmp/video.mov",
			want:      false,
		},
		{
			name:      "small video",
			info:      &VideoInfo{Codec: "h264", Width: 640, Height: 480, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.mp4",
			want:      true,
		},
		{
			name:      "uppercase extension",
			info:      &VideoInfo{Codec: "h264", Width: 1920, Height: 1080, PixFmt: "yuv420p"},
			inputPath: "/tmp/video.MP4",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vp.isVideoWebReady(tt.info, tt.inputPath)
			if got != tt.want {
				t.Errorf("isVideoWebReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsInterlacedFieldOrder(t *testing.T) {
	tests := []struct {
		fieldOrder string
		want       bool
	}{
		{"tt", true},
		{"tb", true},
		{"bt", true},
		{"bb", true},
		{"progressive", false},
		{"", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.fieldOrder, func(t *testing.T) {
			got := isInterlacedFieldOrder(tt.fieldOrder)
			if got != tt.want {
				t.Errorf("isInterlacedFieldOrder(%q) = %v, want %v", tt.fieldOrder, got, tt.want)
			}
		})
	}
}
