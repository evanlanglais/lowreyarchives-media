package models

import (
	"time"
)

// MediaType represents the type of media stored
type MediaType string

const (
	MediaTypeYoutubeURL      MediaType = "youtube_url"
	MediaTypeVideoURL        MediaType = "video_url"
	MediaTypePhotoURL        MediaType = "photo_url"
	MediaTypeBucketVideo     MediaType = "bucket_video"
	MediaTypeCloudflareVideo MediaType = "cloudflare_video"
	MediaTypeBucketPhoto     MediaType = "bucket_photo"
)

// MediaStatus represents the processing status of media
type MediaStatus string

const (
	MediaStatusPending    MediaStatus = "pending"
	MediaStatusProcessing MediaStatus = "processing"
	MediaStatusReady      MediaStatus = "ready"
	MediaStatusFailed     MediaStatus = "failed"
)

// Media represents a media entry in the database
type Media struct {
	ID          int64        `db:"id" json:"id"`
	MediaURL    string       `db:"media_url" json:"media_url"`
	MediaType   MediaType    `db:"media_type" json:"media_type"`
	Description *string      `db:"description" json:"description,omitempty"`
	Status      *MediaStatus `db:"status" json:"status,omitempty"`
	CreatedAt   time.Time    `db:"created_at" json:"created_at"`
	CreatedBy   *string      `db:"created_by" json:"created_by,omitempty"`
}

// EventMedia represents the junction table linking events to media
type EventMedia struct {
	EventID  int64     `db:"event_id" json:"event_id"`
	MediaID  int64     `db:"media_id" json:"media_id"`
	LinkedAt time.Time `db:"linked_at" json:"linked_at"`
}

// Event represents an event entry in the database
type Event struct {
	ID          int64     `db:"id" json:"id"`
	Title       string    `db:"title" json:"title"`
	Description *string   `db:"description" json:"description,omitempty"`
	StartDate   string    `db:"start_date" json:"start_date"`
	EndDate     string    `db:"end_date" json:"end_date"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	CreatedBy   *string   `db:"created_by" json:"created_by,omitempty"`
	Location    *string   `db:"location" json:"location,omitempty"`
}

// VariantType represents the type of media variant
type VariantType string

const (
	VariantTypeOriginal  VariantType = "original"
	VariantTypeOptimized VariantType = "optimized"
	VariantTypeThumbnail VariantType = "thumbnail"
)

// MediaVariant represents a variant of a media item (original, optimized, thumbnail, etc.)
type MediaVariant struct {
	ID              int64       `db:"id" json:"id"`
	MediaID         int64       `db:"media_id" json:"media_id"`
	VariantType     VariantType `db:"variant_type" json:"variant_type"`
	Bucket          string      `db:"bucket" json:"bucket"`
	StoragePath     string      `db:"storage_path" json:"storage_path"`
	FileSize        *int64      `db:"file_size" json:"file_size,omitempty"`
	MimeType        *string     `db:"mime_type" json:"mime_type,omitempty"`
	Width           *int        `db:"width" json:"width,omitempty"`
	Height          *int        `db:"height" json:"height,omitempty"`
	DurationSeconds *float64    `db:"duration_seconds" json:"duration_seconds,omitempty"`
	CreatedAt       time.Time   `db:"created_at" json:"created_at"`
}

// NewMediaVariant creates a new MediaVariant with required fields
func NewMediaVariant(mediaID int64, variantType VariantType, bucket, storagePath string) *MediaVariant {
	return &MediaVariant{
		MediaID:     mediaID,
		VariantType: variantType,
		Bucket:      bucket,
		StoragePath: storagePath,
		CreatedAt:   time.Now(),
	}
}
