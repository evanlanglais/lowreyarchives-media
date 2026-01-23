package db

import (
	"context"
	"fmt"
)

// Migration SQL for the processing_jobs table
// This table tracks media processing jobs and their state
const createProcessingJobsTable = `
CREATE TABLE IF NOT EXISTS processing_jobs (
    id BIGSERIAL PRIMARY KEY,
    media_id BIGINT NOT NULL,
    event_id BIGINT,
    job_type VARCHAR(50) NOT NULL CHECK (job_type IN ('video', 'photo')),
    status VARCHAR(50) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'retrying')),
    ingest_path TEXT NOT NULL,
    archive_path TEXT,
    optimized_path TEXT,
    thumbnail_path TEXT,
    retry_count INT NOT NULL DEFAULT 0,
    error_message TEXT,
    worker_id VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    processing_details JSONB,

    CONSTRAINT fk_media FOREIGN KEY (media_id) REFERENCES media(id) ON DELETE CASCADE,
    CONSTRAINT fk_event FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE SET NULL
);

-- Index for fetching pending jobs
CREATE INDEX IF NOT EXISTS idx_processing_jobs_status ON processing_jobs(status);

-- Index for fetching jobs by media_id
CREATE INDEX IF NOT EXISTS idx_processing_jobs_media_id ON processing_jobs(media_id);

-- Index for worker assignment queries
CREATE INDEX IF NOT EXISTS idx_processing_jobs_worker ON processing_jobs(worker_id, status);

-- Index for cleanup queries (completed jobs)
CREATE INDEX IF NOT EXISTS idx_processing_jobs_completed ON processing_jobs(status, completed_at) WHERE status = 'completed';
`

// RunMigrations runs the database migrations
func (db *DB) RunMigrations(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, createProcessingJobsTable)
	if err != nil {
		return fmt.Errorf("failed to create processing_jobs table: %w", err)
	}
	return nil
}
