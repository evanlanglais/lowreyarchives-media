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
	// Find media with ingest bucket paths that don't have processing jobs
	ingestPrefix := s.cfg.S3IngestBucket + "/"
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

// scanIngestBucket looks for files in the ingest bucket that may not be in the database
func (s *Scanner) scanIngestBucket(ctx context.Context) error {
	// Use configured ingest path prefix (e.g., "ingest/")
	prefix := s.cfg.S3IngestPath
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	keys, err := s.s3Client.ListIngestObjects(ctx, prefix)
	if err != nil {
		return err
	}

	for _, key := range keys {
		s.logger.Debug().Str("key", key).Msg("found file in ingest bucket")
		// Skip non-media files
		if !storage.IsVideoFile(key) && !storage.IsImageFile(key) {
			continue
		}

		s.logger.Debug().Str("key", key).Msg("found file in ingest bucket")

		// Here you could implement logic to:
		// 1. Check if this file is already being processed
		// 2. Create a media entry and processing job
		// This depends on your workflow - whether files should be
		// registered in DB before or during processing
	}

	s.logger.Debug().Msg("finished scanning ingest bucket")

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

// handleStaleJobs resets jobs that have been processing for too long
func (s *Scanner) handleStaleJobs(ctx context.Context) error {
	timeout := time.Duration(s.cfg.ProcessingTimeout) * time.Second
	staleJobs, err := s.jobRepo.GetStaleJobs(ctx, timeout)
	if err != nil {
		return err
	}

	for _, job := range staleJobs {
		s.logger.Warn().
			Int64("job_id", job.ID).
			Str("worker_id", *job.WorkerID).
			Time("started_at", *job.StartedAt).
			Msg("resetting stale job")

		if err := s.jobRepo.ResetStaleJob(ctx, job.ID); err != nil {
			s.logger.Error().Err(err).Int64("job_id", job.ID).Msg("failed to reset stale job")
		}
	}

	return nil
}
