package queue

import (
	"context"
	"strings"
	"time"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/lowreyarchives/media-processor/internal/db"
	"github.com/lowreyarchives/media-processor/internal/models"
	"github.com/lowreyarchives/media-processor/internal/storage"
	"github.com/rs/zerolog"
)

// Scanner watches for new media that needs processing and creates jobs
type Scanner struct {
	cfg       *config.Config
	jobRepo   *db.JobRepository
	mediaRepo *db.MediaRepository
	s3Client  *storage.S3Client
	logger    zerolog.Logger
}

// NewScanner creates a new media scanner
func NewScanner(
	cfg *config.Config,
	jobRepo *db.JobRepository,
	mediaRepo *db.MediaRepository,
	s3Client *storage.S3Client,
	logger zerolog.Logger,
) *Scanner {
	return &Scanner{
		cfg:       cfg,
		jobRepo:   jobRepo,
		mediaRepo: mediaRepo,
		s3Client:  s3Client,
		logger:    logger.With().Str("component", "scanner").Logger(),
	}
}

// Run starts the scanner loop
func (s *Scanner) Run(ctx context.Context) {
	s.logger.Info().Msg("scanner started")

	// Run immediately on start
	s.scan(ctx)

	ticker := time.NewTicker(time.Duration(s.cfg.PollIntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("scanner shutting down")
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

// scan looks for new media to process and creates jobs
func (s *Scanner) scan(ctx context.Context) {
	// Method 1: Scan database for unprocessed media entries
	if err := s.scanDatabaseMedia(ctx); err != nil {
		s.logger.Error().Err(err).Msg("failed to scan database media")
	}

	// Method 2: Scan S3 ingest bucket for orphaned files
	if err := s.scanIngestBucket(ctx); err != nil {
		s.logger.Error().Err(err).Msg("failed to scan ingest bucket")
	}

	// Method 3: Handle stale jobs
	if err := s.handleStaleJobs(ctx); err != nil {
		s.logger.Error().Err(err).Msg("failed to handle stale jobs")
	}
}

// scanDatabaseMedia finds media entries that need processing
func (s *Scanner) scanDatabaseMedia(ctx context.Context) error {
	// Find media with ingest paths (media_url like 'ingest/...') that don't have processing jobs
	ingestPrefix := s.cfg.S3IngestPath
	if ingestPrefix != "" && !strings.HasSuffix(ingestPrefix, "/") {
		ingestPrefix += "/"
	}
	mediaList, err := s.mediaRepo.GetUnprocessedMedia(ctx, ingestPrefix)
	if err != nil {
		return err
	}

	for _, media := range mediaList {
		if err := s.createJobForMedia(ctx, media); err != nil {
			s.logger.Error().
				Err(err).
				Int64("media_id", media.ID).
				Msg("failed to create job for media")
		}
	}

	return nil
}

// scanIngestBucket looks for orphaned files in the ingest bucket and cleans them up
func (s *Scanner) scanIngestBucket(ctx context.Context) error {
	if !s.cfg.OrphanCleanupEnable {
		return nil
	}

	// Use configured ingest path prefix (e.g., "ingest/")
	prefix := s.cfg.S3IngestPath
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	objects, err := s.s3Client.ListIngestObjectsWithInfo(ctx, prefix)
	if err != nil {
		return err
	}

	orphanAge := time.Duration(s.cfg.OrphanCleanupAgeSec) * time.Second
	cutoff := time.Now().Add(-orphanAge)
	orphanCount := 0
	deletedCount := 0

	for _, obj := range objects {
		// Skip non-media files
		if !storage.IsVideoFile(obj.Key) && !storage.IsImageFile(obj.Key) {
			continue
		}

		// Skip files that are too new (might still be uploading or about to be processed)
		if obj.LastModified.After(cutoff) {
			s.logger.Debug().
				Str("key", obj.Key).
				Time("last_modified", obj.LastModified).
				Msg("skipping recent file in ingest bucket")
			continue
		}

		// Check if this file has an associated media record
		exists, err := s.mediaRepo.ExistsWithIngestPath(ctx, obj.Key)
		if err != nil {
			s.logger.Warn().Err(err).Str("key", obj.Key).Msg("failed to check media existence")
			continue
		}

		if exists {
			// Has a media record, will be picked up by scanDatabaseMedia
			continue
		}

		// This is an orphaned file - no media record and old enough
		orphanCount++
		s.logger.Info().
			Str("key", obj.Key).
			Time("last_modified", obj.LastModified).
			Int64("size", obj.Size).
			Msg("found orphaned file in ingest bucket, deleting")

		if err := s.s3Client.DeleteFromIngest(ctx, obj.Key); err != nil {
			s.logger.Error().Err(err).Str("key", obj.Key).Msg("failed to delete orphaned file")
		} else {
			deletedCount++
		}
	}

	if orphanCount > 0 {
		s.logger.Info().
			Int("orphan_count", orphanCount).
			Int("deleted_count", deletedCount).
			Msg("orphan cleanup completed")
	}

	return nil
}

// createJobForMedia creates a processing job for a media entry
func (s *Scanner) createJobForMedia(ctx context.Context, media *models.Media) error {
	// Determine job type
	var jobType models.JobType
	if storage.IsVideoFile(media.MediaURL) {
		jobType = models.JobTypeVideo
	} else if storage.IsImageFile(media.MediaURL) {
		jobType = models.JobTypePhoto
	} else {
		s.logger.Warn().
			Int64("media_id", media.ID).
			Str("url", media.MediaURL).
			Msg("unknown media type, skipping")
		return nil
	}

	// Get event ID if associated
	eventID, err := s.mediaRepo.GetEventIDForMedia(ctx, media.ID)
	if err != nil {
		s.logger.Warn().Err(err).Int64("media_id", media.ID).Msg("failed to get event ID")
	}

	// Create the job
	job := models.NewProcessingJob(media.ID, eventID, jobType, media.MediaURL)
	if err := s.jobRepo.Create(ctx, job); err != nil {
		return err
	}

	s.logger.Info().
		Int64("job_id", job.ID).
		Int64("media_id", media.ID).
		Str("job_type", string(jobType)).
		Msg("created processing job")

	return nil
}

// handleStaleJobs resets jobs that have stopped sending heartbeats
func (s *Scanner) handleStaleJobs(ctx context.Context) error {
	timeout := time.Duration(s.cfg.HeartbeatTimeoutSec) * time.Second
	staleJobs, err := s.jobRepo.GetStaleJobs(ctx, timeout)
	if err != nil {
		return err
	}

	for _, job := range staleJobs {
		var lastHeartbeat time.Time
		if job.LastHeartbeat != nil {
			lastHeartbeat = *job.LastHeartbeat
		}

		s.logger.Warn().
			Int64("job_id", job.ID).
			Str("worker_id", stringPtrOrEmpty(job.WorkerID)).
			Time("started_at", timePtrOrZero(job.StartedAt)).
			Time("last_heartbeat", lastHeartbeat).
			Msg("resetting stale job (no heartbeat)")

		if err := s.jobRepo.ResetStaleJob(ctx, job.ID); err != nil {
			s.logger.Error().Err(err).Int64("job_id", job.ID).Msg("failed to reset stale job")
		}

		// Also reset the media status back to pending for consistency
		if err := s.mediaRepo.UpdateStatus(ctx, job.MediaID, models.MediaStatusPending); err != nil {
			s.logger.Warn().Err(err).Int64("media_id", job.MediaID).Msg("failed to reset media status")
		}
	}

	return nil
}

func stringPtrOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timePtrOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
