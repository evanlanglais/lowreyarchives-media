package models

import (
	"time"
)

// JobStatus represents the current state of a processing job
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusRetrying   JobStatus = "retrying"
)

// JobType represents the type of processing to perform
type JobType string

const (
	JobTypeVideo JobType = "video"
	JobTypePhoto JobType = "photo"
)

// ProcessingJob represents a media processing job in the queue
type ProcessingJob struct {
	ID                int64      `db:"id" json:"id"`
	MediaID           int64      `db:"media_id" json:"media_id"`
	EventID           *int64     `db:"event_id" json:"event_id,omitempty"`
	JobType           JobType    `db:"job_type" json:"job_type"`
	Status            JobStatus  `db:"status" json:"status"`
	IngestPath        string     `db:"ingest_path" json:"ingest_path"`
	ArchivePath       *string    `db:"archive_path" json:"archive_path,omitempty"`
	OptimizedPath     *string    `db:"optimized_path" json:"optimized_path,omitempty"`
	ThumbnailPath     *string    `db:"thumbnail_path" json:"thumbnail_path,omitempty"`
	RetryCount        int        `db:"retry_count" json:"retry_count"`
	ErrorMessage      *string    `db:"error_message" json:"error_message,omitempty"`
	WorkerID          *string    `db:"worker_id" json:"worker_id,omitempty"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`
	StartedAt         *time.Time `db:"started_at" json:"started_at,omitempty"`
	CompletedAt       *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	ProcessingDetails *string    `db:"processing_details" json:"processing_details,omitempty"`
	LastHeartbeat     *time.Time `db:"last_heartbeat" json:"last_heartbeat,omitempty"`
	EngineVersion     int        `db:"engine_version" json:"engine_version"`
	IsReprocess       bool       `db:"is_reprocess" json:"is_reprocess"`
}

// NewProcessingJob creates a new processing job with default values
func NewProcessingJob(mediaID int64, eventID *int64, jobType JobType, ingestPath string, engineVersion int) *ProcessingJob {
	now := time.Now()
	return &ProcessingJob{
		MediaID:       mediaID,
		EventID:       eventID,
		JobType:       jobType,
		Status:        JobStatusPending,
		IngestPath:    ingestPath,
		RetryCount:    0,
		EngineVersion: engineVersion,
		IsReprocess:   false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// NewReprocessingJob creates a reprocessing job that downloads from the archive path
func NewReprocessingJob(mediaID int64, eventID *int64, jobType JobType, archivePath string, engineVersion int) *ProcessingJob {
	now := time.Now()
	return &ProcessingJob{
		MediaID:       mediaID,
		EventID:       eventID,
		JobType:       jobType,
		Status:        JobStatusPending,
		IngestPath:    archivePath, // reprocess jobs use the archive path as their source
		RetryCount:    0,
		EngineVersion: engineVersion,
		IsReprocess:   true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// CanRetry checks if the job can be retried based on max retries
func (j *ProcessingJob) CanRetry(maxRetries int) bool {
	return j.RetryCount < maxRetries
}

// IsTerminal checks if the job is in a terminal state
func (j *ProcessingJob) IsTerminal() bool {
	return j.Status == JobStatusCompleted || j.Status == JobStatusFailed
}
