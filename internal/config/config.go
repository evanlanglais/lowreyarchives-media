package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	// Database
	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`

	// S3/Garage Configuration
	S3Endpoint     string `envconfig:"S3_ENDPOINT" required:"true"`
	S3AccessKey    string `envconfig:"S3_ACCESS_KEY" required:"true"`
	S3SecretKey    string `envconfig:"S3_SECRET_KEY" required:"true"`
	S3Region       string `envconfig:"S3_REGION" default:"garage"`
	S3IngestBucket string `envconfig:"S3_INGEST_BUCKET" default:"fa-dmz"`
	S3IngestPath   string `envconfig:"S3_INGEST_PATH" default:"ingest"`
	S3MediaBucket  string `envconfig:"S3_MEDIA_BUCKET" default:"fa-media"`
	S3ArchivePath  string `envconfig:"S3_ARCHIVE_PATH" default:"archive"`

	// Worker Configuration
	WorkerCount       int `envconfig:"WORKER_COUNT" default:"4"`
	PollIntervalSec   int `envconfig:"POLL_INTERVAL_SEC" default:"10"`
	MaxRetries        int `envconfig:"MAX_RETRIES" default:"3"`
	ProcessingTimeout int `envconfig:"PROCESSING_TIMEOUT_SEC" default:"3600"`

	// FFmpeg Configuration
	FFmpegPath         string `envconfig:"FFMPEG_PATH" default:"ffmpeg"`
	FFprobePath        string `envconfig:"FFPROBE_PATH" default:"ffprobe"`
	VideoOutputFormat  string `envconfig:"VIDEO_OUTPUT_FORMAT" default:"mp4"`
	VideoCodec         string `envconfig:"VIDEO_CODEC" default:"libx264"`
	VideoPreset        string `envconfig:"VIDEO_PRESET" default:"medium"`
	VideoCRF           string `envconfig:"VIDEO_CRF" default:"23"`
	AudioCodec         string `envconfig:"AUDIO_CODEC" default:"aac"`
	MaxVideoWidth      int    `envconfig:"MAX_VIDEO_WIDTH" default:"1920"`
	MaxVideoHeight     int    `envconfig:"MAX_VIDEO_HEIGHT" default:"1080"`

	// Image Processing
	ImageMaxWidth  int `envconfig:"IMAGE_MAX_WIDTH" default:"2048"`
	ImageMaxHeight int `envconfig:"IMAGE_MAX_HEIGHT" default:"2048"`
	ImageQuality   int `envconfig:"IMAGE_QUALITY" default:"85"`

	// Local Processing Directory
	TempDir string `envconfig:"TEMP_DIR" default:"/tmp/media-processor"`

	// Health Check
	HealthPort int `envconfig:"HEALTH_PORT" default:"8080"`

	// Logging
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &cfg, nil
}
