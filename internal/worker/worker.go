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
	variantRepo    *db.VariantRepository
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
	variantRepo *db.VariantRepository,
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
		variantRepo:    variantRepo,
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

	// Update media status to processing
	if err := w.mediaRepo.UpdateStatus(ctx, job.MediaID, models.MediaStatusProcessing); err != nil {
		w.logger.Warn().Err(err).Int64("media_id", job.MediaID).Msg("failed to update media status to processing")
	}

	// Create job-specific directory
	jobDir := filepath.Join(w.workDir, fmt.Sprintf("job-%d", job.ID))
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return w.handleJobError(ctx, job, fmt.Errorf("failed to create job directory: %w", err))
	}
	defer os.RemoveAll(jobDir)

	// Start heartbeat goroutine to signal we're still alive
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go w.sendHeartbeats(heartbeatCtx, job.ID, heartbeatDone)

	// Process the job
	err = w.processJob(ctx, job, jobDir)

	// Stop heartbeat goroutine
	cancelHeartbeat()
	<-heartbeatDone

	if err != nil {
		return w.handleJobError(ctx, job, err)
	}

	return nil
}

// sendHeartbeats periodically updates the job's heartbeat timestamp
func (w *Worker) sendHeartbeats(ctx context.Context, jobID int64, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(time.Duration(w.cfg.HeartbeatIntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.jobRepo.UpdateHeartbeat(context.Background(), jobID); err != nil {
				w.logger.Warn().Err(err).Int64("job_id", jobID).Msg("failed to update heartbeat")
			} else {
				w.logger.Debug().Int64("job_id", jobID).Msg("heartbeat sent")
			}
		}
	}
}

// processJob handles the full processing pipeline for a job
func (w *Worker) processJob(ctx context.Context, job *models.ProcessingJob, jobDir string) error {
	// Step 1: Download file from ingest bucket
	localPath, err := w.downloadFile(ctx, job.IngestPath, jobDir)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Get original file size
	originalFileInfo, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("failed to stat original file: %w", err)
	}
	originalFileSize := originalFileInfo.Size()

	// Step 2: Process based on job type
	var optimizedPath, thumbnailPath string
	var processingDetails interface{}
	var width, height int
	var duration float64
	var mimeType string

	switch job.JobType {
	case models.JobTypeVideo:
		result, err := w.videoProcessor.Process(ctx, localPath, jobDir)
		if err != nil {
			return fmt.Errorf("video processing failed: %w", err)
		}
		optimizedPath = result.OptimizedPath
		thumbnailPath = result.ThumbnailPath
		processingDetails = result.Info
		if result.Info != nil {
			width = result.Info.Width
			height = result.Info.Height
			duration = result.Info.Duration
		}
		mimeType = "video/mp4"

	case models.JobTypePhoto:
		result, err := w.imageProcessor.Process(ctx, localPath, jobDir)
		if err != nil {
			return fmt.Errorf("image processing failed: %w", err)
		}
		optimizedPath = result.OptimizedPath
		thumbnailPath = result.ThumbnailPath
		processingDetails = result.Info
		if result.Info != nil {
			width = result.Info.Width
			height = result.Info.Height
		}
		mimeType = "image/jpeg"

	default:
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}

	// Get optimized file size
	var optimizedFileSize int64
	if optimizedPath != "" {
		if info, err := os.Stat(optimizedPath); err == nil {
			optimizedFileSize = info.Size()
		}
	}

	// Get thumbnail file size
	var thumbnailFileSize int64
	if thumbnailPath != "" {
		if info, err := os.Stat(thumbnailPath); err == nil {
			thumbnailFileSize = info.Size()
		}
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

	// Step 6: Insert media variants
	mediaBucket := w.s3Client.GetMediaBucket()
	variants := w.buildMediaVariants(
		job.MediaID, mediaBucket,
		archivePath, optimizedS3Key, thumbnailS3Key,
		originalFileSize, optimizedFileSize, thumbnailFileSize,
		width, height, duration, mimeType,
	)

	if err := w.variantRepo.UpsertMediaVariants(ctx, variants); err != nil {
		w.logger.Error().Err(err).Int64("media_id", job.MediaID).Msg("failed to upsert media variants")
		// Don't fail the job for variant insert errors - the core processing succeeded
	} else {
		w.logger.Info().Int64("media_id", job.MediaID).Int("variant_count", len(variants)).Msg("inserted media variants")
	}

	// Step 7: Update media entry with new URL
	mediaType := models.MediaTypeBucketVideo
	if job.JobType == models.JobTypePhoto {
		mediaType = models.MediaTypeBucketPhoto
	}
	if err := w.mediaRepo.UpdateMediaURL(ctx, job.MediaID, optimizedS3Key, mediaType); err != nil {
		return fmt.Errorf("failed to update media: %w", err)
	}

	// Step 8: Mark job as completed
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

	// Step 9: Delete original from ingest bucket
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

// buildMediaVariants creates MediaVariant records for the processed media
func (w *Worker) buildMediaVariants(
	mediaID int64, bucket string,
	archivePath, optimizedPath, thumbnailPath string,
	originalSize, optimizedSize, thumbnailSize int64,
	width, height int, duration float64, mimeType string,
) []*models.MediaVariant {
	var variants []*models.MediaVariant

	// Determine original mime type from extension
	originalMimeType := w.getMimeTypeFromPath(archivePath)

	// Original variant
	originalVariant := models.NewMediaVariant(mediaID, models.VariantTypeOriginal, bucket, archivePath)
	originalVariant.FileSize = &originalSize
	originalVariant.MimeType = &originalMimeType
	if width > 0 {
		originalVariant.Width = &width
	}
	if height > 0 {
		originalVariant.Height = &height
	}
	if duration > 0 {
		originalVariant.DurationSeconds = &duration
	}
	variants = append(variants, originalVariant)

	// Optimized variant
	if optimizedPath != "" {
		optimizedVariant := models.NewMediaVariant(mediaID, models.VariantTypeOptimized, bucket, optimizedPath)
		optimizedVariant.FileSize = &optimizedSize
		optimizedVariant.MimeType = &mimeType
		if width > 0 {
			optimizedVariant.Width = &width
		}
		if height > 0 {
			optimizedVariant.Height = &height
		}
		if duration > 0 {
			optimizedVariant.DurationSeconds = &duration
		}
		variants = append(variants, optimizedVariant)
	}

	// Thumbnail variant
	if thumbnailPath != "" {
		thumbnailMimeType := "image/jpeg"
		thumbnailVariant := models.NewMediaVariant(mediaID, models.VariantTypeThumbnail, bucket, thumbnailPath)
		thumbnailVariant.FileSize = &thumbnailSize
		thumbnailVariant.MimeType = &thumbnailMimeType
		// Thumbnail dimensions are different - we could calculate them but for now leave as nil
		variants = append(variants, thumbnailVariant)
	}

	return variants
}

// getMimeTypeFromPath determines mime type from file extension
func (w *Worker) getMimeTypeFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic", ".heif":
		return "image/heic"
	default:
		return "application/octet-stream"
	}
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
// Uses FPutObject internally which handles multipart uploads for large files
func (w *Worker) uploadProcessedFile(ctx context.Context, localPath, prefix string) (string, error) {
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

	if err := w.s3Client.UploadFileToMedia(ctx, s3Key, localPath, contentType); err != nil {
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

	// If we've exceeded max retries, mark media as failed
	if job.RetryCount+1 >= w.cfg.MaxRetries {
		if statusErr := w.mediaRepo.UpdateStatus(ctx, job.MediaID, models.MediaStatusFailed); statusErr != nil {
			w.logger.Warn().Err(statusErr).Int64("media_id", job.MediaID).Msg("failed to update media status to failed")
		}
	}

	return err
}

// cleanup removes the worker's temp directory
func (w *Worker) cleanup() {
	if err := os.RemoveAll(w.workDir); err != nil {
		w.logger.Warn().Err(err).Msg("failed to cleanup work directory")
	}
}
