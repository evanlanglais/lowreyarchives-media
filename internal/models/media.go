package models

import (
	"time"
)

// MediaType represents the type of media stored
type MediaType string

const (
	MediaTypeYoutubeURL    MediaType = "youtube_url"
	MediaTypeVideoURL      MediaType = "video_url"
	MediaTypePhotoURL      MediaType = "photo_url"
	MediaTypeBucketVideo   MediaType = "bucket_video"
	MediaTypeCloudflareVideo MediaType = "cloudflare_video"
	// New types for processed media
	MediaTypeBucketPhoto   MediaType = "bucket_photo"
)

// Media represents a media entry in the database
type Media struct {
	ID          int64      `db:"id" json:"id"`
	MediaURL    string     `db:"media_url" json:"media_url"`
	MediaType   MediaType  `db:"media_type" json:"media_type"`
	Description *string    `db:"description" json:"description,omitempty"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
	CreatedBy   *string    `db:"created_by" json:"created_by,omitempty"`
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
