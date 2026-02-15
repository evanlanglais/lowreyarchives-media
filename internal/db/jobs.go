package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lowreyarchives/media-processor/internal/models"
)

// JobRepository handles database operations for processing jobs
type JobRepository struct {
	db *DB
}

// NewJobRepository creates a new job repository
func NewJobRepository(db *DB) *JobRepository {
	return &JobRepository{db: db}
}

// Create inserts a new processing job
func (r *JobRepository) Create(ctx context.Context, job *models.ProcessingJob) error {
	query := `
		INSERT INTO processing_jobs (
			media_id, event_id, job_type, status, ingest_path,
			retry_count, engine_version, is_reprocess, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`

	err := r.db.pool.QueryRow(ctx, query,
		job.MediaID, job.EventID, job.JobType, job.Status, job.IngestPath,
		job.RetryCount, job.EngineVersion, job.IsReprocess, job.CreatedAt, job.UpdatedAt,
	).Scan(&job.ID)

	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}
	return nil
}

// GetByID retrieves a job by its ID
func (r *JobRepository) GetByID(ctx context.Context, id int64) (*models.ProcessingJob, error) {
	query := `
		SELECT id, media_id, event_id, job_type, status, ingest_path,
			   archive_path, optimized_path, thumbnail_path, retry_count,
			   error_message, worker_id, created_at, updated_at,
			   started_at, completed_at, processing_details, last_heartbeat,
			   engine_version, is_reprocess
		FROM processing_jobs WHERE id = $1`

	job := &models.ProcessingJob{}
	err := r.db.pool.QueryRow(ctx, query, id).Scan(
		&job.ID, &job.MediaID, &job.EventID, &job.JobType, &job.Status, &job.IngestPath,
		&job.ArchivePath, &job.OptimizedPath, &job.ThumbnailPath, &job.RetryCount,
		&job.ErrorMessage, &job.WorkerID, &job.CreatedAt, &job.UpdatedAt,
		&job.StartedAt, &job.CompletedAt, &job.ProcessingDetails, &job.LastHeartbeat,
		&job.EngineVersion, &job.IsReprocess,
	)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}
	return job, nil
}

// ClaimPendingJob atomically claims a pending job for a worker
// Uses row-level locking to prevent race conditions
func (r *JobRepository) ClaimPendingJob(ctx context.Context, workerID string) (*models.ProcessingJob, error) {
	query := `
		UPDATE processing_jobs
		SET status = $1, worker_id = $2, started_at = $3, updated_at = $3, last_heartbeat = $3
		WHERE id = (
			SELECT id FROM processing_jobs
			WHERE status = $4 OR (status = $5 AND retry_count < 3)
			ORDER BY created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, media_id, event_id, job_type, status, ingest_path,
				  archive_path, optimized_path, thumbnail_path, retry_count,
				  error_message, worker_id, created_at, updated_at,
				  started_at, completed_at, processing_details, last_heartbeat,
				  engine_version, is_reprocess`

	now := time.Now()
	job := &models.ProcessingJob{}
	err := r.db.pool.QueryRow(ctx, query,
		models.JobStatusProcessing, workerID, now,
		models.JobStatusPending, models.JobStatusRetrying,
	).Scan(
		&job.ID, &job.MediaID, &job.EventID, &job.JobType, &job.Status, &job.IngestPath,
		&job.ArchivePath, &job.OptimizedPath, &job.ThumbnailPath, &job.RetryCount,
		&job.ErrorMessage, &job.WorkerID, &job.CreatedAt, &job.UpdatedAt,
		&job.StartedAt, &job.CompletedAt, &job.ProcessingDetails, &job.LastHeartbeat,
		&job.EngineVersion, &job.IsReprocess,
	)

	if err == pgx.ErrNoRows {
		return nil, nil // No jobs available
	}
	if err != nil {
		return nil, fmt.Errorf("failed to claim job: %w", err)
	}
	return job, nil
}

// MarkCompleted marks a job as completed with the output paths
func (r *JobRepository) MarkCompleted(ctx context.Context, job *models.ProcessingJob) error {
	now := time.Now()
	query := `
		UPDATE processing_jobs
		SET status = $1, archive_path = $2, optimized_path = $3,
			thumbnail_path = $4, completed_at = $5, updated_at = $5,
			processing_details = $6, engine_version = $7
		WHERE id = $8`

	_, err := r.db.pool.Exec(ctx, query,
		models.JobStatusCompleted, job.ArchivePath, job.OptimizedPath,
		job.ThumbnailPath, now, job.ProcessingDetails, job.EngineVersion, job.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark job completed: %w", err)
	}
	return nil
}

// MarkFailed marks a job as failed with an error message
func (r *JobRepository) MarkFailed(ctx context.Context, jobID int64, errMsg string, maxRetries int) error {
	now := time.Now()

	// First, get the current retry count
	var retryCount int
	err := r.db.pool.QueryRow(ctx,
		"SELECT retry_count FROM processing_jobs WHERE id = $1", jobID,
	).Scan(&retryCount)
	if err != nil {
		return fmt.Errorf("failed to get retry count: %w", err)
	}

	// Determine new status based on retry count
	newStatus := models.JobStatusRetrying
	if retryCount >= maxRetries-1 {
		newStatus = models.JobStatusFailed
	}

	query := `
		UPDATE processing_jobs
		SET status = $1, error_message = $2, retry_count = retry_count + 1,
			updated_at = $3, worker_id = NULL
		WHERE id = $4`

	_, err = r.db.pool.Exec(ctx, query, newStatus, errMsg, now, jobID)
	if err != nil {
		return fmt.Errorf("failed to mark job failed: %w", err)
	}
	return nil
}

// GetPendingCount returns the count of pending jobs
func (r *JobRepository) GetPendingCount(ctx context.Context) (int, error) {
	var count int
	err := r.db.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM processing_jobs WHERE status IN ($1, $2)",
		models.JobStatusPending, models.JobStatusRetrying,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get pending count: %w", err)
	}
	return count, nil
}

// UpdateHeartbeat updates the last_heartbeat timestamp for a job
// This signals that the worker is still actively processing
func (r *JobRepository) UpdateHeartbeat(ctx context.Context, jobID int64) error {
	_, err := r.db.pool.Exec(ctx,
		"UPDATE processing_jobs SET last_heartbeat = $1 WHERE id = $2",
		time.Now(), jobID,
	)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}
	return nil
}

// GetStaleJobs finds jobs that have stopped sending heartbeats
// A job is considered stale if its last_heartbeat is older than the timeout
func (r *JobRepository) GetStaleJobs(ctx context.Context, timeout time.Duration) ([]*models.ProcessingJob, error) {
	cutoff := time.Now().Add(-timeout)
	query := `
		SELECT id, media_id, event_id, job_type, status, ingest_path,
			   archive_path, optimized_path, thumbnail_path, retry_count,
			   error_message, worker_id, created_at, updated_at,
			   started_at, completed_at, processing_details, last_heartbeat,
			   engine_version, is_reprocess
		FROM processing_jobs
		WHERE status = $1 AND (last_heartbeat IS NULL OR last_heartbeat < $2)`

	rows, err := r.db.pool.Query(ctx, query, models.JobStatusProcessing, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to get stale jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*models.ProcessingJob
	for rows.Next() {
		job := &models.ProcessingJob{}
		err := rows.Scan(
			&job.ID, &job.MediaID, &job.EventID, &job.JobType, &job.Status, &job.IngestPath,
			&job.ArchivePath, &job.OptimizedPath, &job.ThumbnailPath, &job.RetryCount,
			&job.ErrorMessage, &job.WorkerID, &job.CreatedAt, &job.UpdatedAt,
			&job.StartedAt, &job.CompletedAt, &job.ProcessingDetails, &job.LastHeartbeat,
			&job.EngineVersion, &job.IsReprocess,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan stale job: %w", err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// ResetStaleJob resets a stale job to pending status
// Does NOT increment retry_count since heartbeat timeouts are not processing failures
// (could be user cancellation, system restart, network issues, etc.)
func (r *JobRepository) ResetStaleJob(ctx context.Context, jobID int64) error {
	query := `
		UPDATE processing_jobs
		SET status = $1, worker_id = NULL, started_at = NULL,
			updated_at = $2, last_heartbeat = NULL
		WHERE id = $3`

	_, err := r.db.pool.Exec(ctx, query, models.JobStatusPending, time.Now(), jobID)
	if err != nil {
		return fmt.Errorf("failed to reset stale job: %w", err)
	}
	return nil
}

// ReprocessCandidate represents a media item that needs reprocessing
type ReprocessCandidate struct {
	MediaID        int64
	JobType        models.JobType
	CurrentVersion int
	ArchivePath    string
	EventID        *int64
}

// GetMediaNeedingReprocess finds media that was processed with an outdated engine version
func (r *JobRepository) GetMediaNeedingReprocess(ctx context.Context, videoVersion, photoVersion, limit int) ([]ReprocessCandidate, error) {
	query := `
		WITH latest_completed AS (
			SELECT DISTINCT ON (media_id)
				media_id, job_type, engine_version, archive_path, event_id
			FROM processing_jobs
			WHERE status = 'completed' AND archive_path IS NOT NULL
			ORDER BY media_id, completed_at DESC
		)
		SELECT lc.media_id, lc.job_type, lc.engine_version, lc.archive_path, lc.event_id
		FROM latest_completed lc
		JOIN media m ON m.id = lc.media_id
		WHERE m.status = 'ready'
		AND (
			(lc.job_type = 'video' AND lc.engine_version < $1)
			OR (lc.job_type = 'photo' AND lc.engine_version < $2)
		)
		AND NOT EXISTS (
			SELECT 1 FROM processing_jobs pj2
			WHERE pj2.media_id = lc.media_id
			AND pj2.status IN ('pending', 'processing', 'retrying')
		)
		LIMIT $3`

	rows, err := r.db.pool.Query(ctx, query, videoVersion, photoVersion, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get media needing reprocess: %w", err)
	}
	defer rows.Close()

	var candidates []ReprocessCandidate
	for rows.Next() {
		var c ReprocessCandidate
		err := rows.Scan(&c.MediaID, &c.JobType, &c.CurrentVersion, &c.ArchivePath, &c.EventID)
		if err != nil {
			return nil, fmt.Errorf("failed to scan reprocess candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	return candidates, nil
}

// DeleteCompletedJobs removes completed jobs older than the given duration
func (r *JobRepository) DeleteCompletedJobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := r.db.pool.Exec(ctx,
		"DELETE FROM processing_jobs WHERE status = $1 AND completed_at < $2",
		models.JobStatusCompleted, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to delete completed jobs: %w", err)
	}
	return result.RowsAffected(), nil
}
