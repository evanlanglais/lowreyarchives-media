package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/lowreyarchives/media-processor/internal/db"
	"github.com/lowreyarchives/media-processor/internal/models"
	"github.com/lowreyarchives/media-processor/internal/processor"
	"github.com/lowreyarchives/media-processor/internal/storage"
	"github.com/rs/zerolog"
)

// Worker processes media jobs from the queue
type Worker struct {
	id             string
	cfg            *config.Config
	jobRepo        *db.JobRepository
	mediaRepo      *db.MediaRepository
	s3Client       *storage.S3Client
	videoProcessor *processor.VideoProcessor
	imageProcessor *processor.ImageProcessor
	logger         zerolog.Logger
	workDir        string
}

// NewWorker creates a new worker instance
func NewWorker(
	cfg *config.Config,
	jobRepo *db.JobRepository,
	mediaRepo *db.MediaRepository,
	s3Client *storage.S3Client,
	logger zerolog.Logger,
) (*Worker, error) {
	workerID := fmt.Sprintf("worker-%s", uuid.New().String()[:8])

	// Create worker-specific temp directory
	workDir := filepath.Join(cfg.TempDir, workerID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	return &Worker{
		id:             workerID,
		cfg:            cfg,
		jobRepo:        jobRepo,
		mediaRepo:      mediaRepo,
		s3Client:       s3Client,
		videoProcessor: processor.NewVideoProcessor(cfg, logger),
		imageProcessor: processor.NewImageProcessor(cfg, logger),
		logger:         logger.With().Str("worker_id", workerID).Logger(),
		workDir:        workDir,
	}, nil
}

// Run starts the worker loop
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info().Msg("worker started")

	ticker := time.NewTicker(time.Duration(w.cfg.PollIntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info().Msg("worker shutting down")
			w.cleanup()
			return
		case <-ticker.C:
			if err := w.processNextJob(ctx); err != nil {
				w.logger.Error().Err(err).Msg("error processing job")
			}
		}
	}
}

// processNextJob claims and processes the next available job
func (w *Worker) processNextJob(ctx context.Context) error {
	// Try to claim a job
	job, err := w.jobRepo.ClaimPendingJob(ctx, w.id)
	if err != nil {
		return fmt.Errorf("failed to claim job: %w", err)
	}

	if job == nil {
		// No jobs available
		return nil
	}

	w.logger.Info().
		Int64("job_id", job.ID).
		Int64("media_id", job.MediaID).
		Str("job_type", string(job.JobType)).
		Msg("processing job")

	// Create job-specific directory
	jobDir := filepath.Join(w.workDir, fmt.Sprintf("job-%d", job.ID))
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return w.handleJobError(ctx, job, fmt.Errorf("failed to create job directory: %w", err))
	}
	defer os.RemoveAll(jobDir)

	// Process the job
	if err := w.processJob(ctx, job, jobDir); err != nil {
		return w.handleJobError(ctx, job, err)
	}

	return nil
}

// processJob handles the full processing pipeline for a job
func (w *Worker) processJob(ctx context.Context, job *models.ProcessingJob, jobDir string) error {
	// Step 1: Download file from ingest bucket
	localPath, err := w.downloadFile(ctx, job.IngestPath, jobDir)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Step 2: Process based on job type
	var optimizedPath, thumbnailPath string
	var processingDetails interface{}

	switch job.JobType {
	case models.JobTypeVideo:
		result, err := w.videoProcessor.Process(ctx, localPath, jobDir)
		if err != nil {
			return fmt.Errorf("video processing failed: %w", err)
		}
		optimizedPath = result.OptimizedPath
		thumbnailPath = result.ThumbnailPath
		processingDetails = result.Info

	case models.JobTypePhoto:
		result, err := w.imageProcessor.Process(ctx, localPath, jobDir)
		if err != nil {
			return fmt.Errorf("image processing failed: %w", err)
		}
		optimizedPath = result.OptimizedPath
		thumbnailPath = result.ThumbnailPath
		processingDetails = result.Info

	default:
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}

	// Step 3: Archive original file
	archivePath, err := w.s3Client.ArchiveOriginal(ctx, job.IngestPath)
	if err != nil {
		return fmt.Errorf("archiving failed: %w", err)
	}

	// Step 4: Upload optimized file to media bucket
	optimizedS3Key, err := w.uploadProcessedFile(ctx, optimizedPath, "optimized")
	if err != nil {
		return fmt.Errorf("upload optimized failed: %w", err)
	}

	// Step 5: Upload thumbnail if exists
	var thumbnailS3Key string
	if thumbnailPath != "" {
		thumbnailS3Key, err = w.uploadProcessedFile(ctx, thumbnailPath, "thumbnails")
		if err != nil {
			w.logger.Warn().Err(err).Msg("failed to upload thumbnail, continuing")
		}
	}

	// Step 6: Update media entry with new URL
	mediaType := models.MediaTypeBucketVideo
	if job.JobType == models.JobTypePhoto {
		mediaType = models.MediaTypeBucketPhoto
	}
	if err := w.mediaRepo.UpdateMediaURL(ctx, job.MediaID, optimizedS3Key, mediaType); err != nil {
		return fmt.Errorf("failed to update media: %w", err)
	}

	// Step 7: Mark job as completed
	detailsJSON, _ := json.Marshal(processingDetails)
	detailsStr := string(detailsJSON)
	job.ArchivePath = &archivePath
	job.OptimizedPath = &optimizedS3Key
	if thumbnailS3Key != "" {
		job.ThumbnailPath = &thumbnailS3Key
	}
	job.ProcessingDetails = &detailsStr

	if err := w.jobRepo.MarkCompleted(ctx, job); err != nil {
		return fmt.Errorf("failed to mark completed: %w", err)
	}

	// Step 8: Delete original from ingest bucket
	if err := w.s3Client.DeleteFromIngest(ctx, job.IngestPath); err != nil {
		w.logger.Warn().Err(err).Str("key", job.IngestPath).Msg("failed to delete from ingest")
	}

	w.logger.Info().
		Int64("job_id", job.ID).
		Str("optimized", optimizedS3Key).
		Str("archive", archivePath).
		Msg("job completed successfully")

	return nil
}

// downloadFile downloads a file from S3 to local storage
func (w *Worker) downloadFile(ctx context.Context, key string, destDir string) (string, error) {
	reader, err := w.s3Client.Download(ctx, key)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	localPath := filepath.Join(destDir, filepath.Base(key))
	file, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to create local file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, reader); err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}

	return localPath, nil
}

// uploadProcessedFile uploads a processed file to the media bucket
func (w *Worker) uploadProcessedFile(ctx context.Context, localPath, prefix string) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Generate S3 key: {prefix}/{filename}
	filename := filepath.Base(localPath)
	s3Key := fmt.Sprintf("%s/%s", prefix, filename)

	// Determine content type
	contentType := "application/octet-stream"
	ext := strings.ToLower(filepath.Ext(localPath))
	switch ext {
	case ".mp4":
		contentType = "video/mp4"
	case ".webm":
		contentType = "video/webm"
	case ".jpg", ".jpeg":
		contentType = "image/jpeg"
	case ".png":
		contentType = "image/png"
	case ".webp":
		contentType = "image/webp"
	}

	if err := w.s3Client.UploadToMedia(ctx, s3Key, file, contentType); err != nil {
		return "", err
	}

	return s3Key, nil
}

// handleJobError marks the job as failed and logs the error
func (w *Worker) handleJobError(ctx context.Context, job *models.ProcessingJob, err error) error {
	w.logger.Error().
		Err(err).
		Int64("job_id", job.ID).
		Int("retry_count", job.RetryCount).
		Msg("job failed")

	if markErr := w.jobRepo.MarkFailed(ctx, job.ID, err.Error(), w.cfg.MaxRetries); markErr != nil {
		w.logger.Error().Err(markErr).Msg("failed to mark job as failed")
		return markErr
	}

	return err
}

// cleanup removes the worker's temp directory
func (w *Worker) cleanup() {
	if err := os.RemoveAll(w.workDir); err != nil {
		w.logger.Warn().Err(err).Msg("failed to cleanup work directory")
	}
}
