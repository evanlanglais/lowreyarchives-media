package worker

import (
	"context"
	"sync"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/lowreyarchives/media-processor/internal/db"
	"github.com/lowreyarchives/media-processor/internal/storage"
	"github.com/rs/zerolog"
)

// Pool manages a pool of workers
type Pool struct {
	cfg         *config.Config
	jobRepo     *db.JobRepository
	mediaRepo   *db.MediaRepository
	variantRepo *db.VariantRepository
	s3Client    *storage.S3Client
	logger      zerolog.Logger
	workers     []*Worker
	wg          sync.WaitGroup
}

// NewPool creates a new worker pool
func NewPool(
	cfg *config.Config,
	jobRepo *db.JobRepository,
	mediaRepo *db.MediaRepository,
	variantRepo *db.VariantRepository,
	s3Client *storage.S3Client,
	logger zerolog.Logger,
) *Pool {
	return &Pool{
		cfg:         cfg,
		jobRepo:     jobRepo,
		mediaRepo:   mediaRepo,
		variantRepo: variantRepo,
		s3Client:    s3Client,
		logger:      logger.With().Str("component", "worker_pool").Logger(),
	}
}

// Start initializes and starts all workers
func (p *Pool) Start(ctx context.Context) error {
	p.logger.Info().Int("worker_count", p.cfg.WorkerCount).Msg("starting worker pool")

	for i := 0; i < p.cfg.WorkerCount; i++ {
		worker, err := NewWorker(p.cfg, p.jobRepo, p.mediaRepo, p.variantRepo, p.s3Client, p.logger)
		if err != nil {
			return err
		}
		p.workers = append(p.workers, worker)

		p.wg.Add(1)
		go func(w *Worker) {
			defer p.wg.Done()
			w.Run(ctx)
		}(worker)
	}

	return nil
}

// Wait blocks until all workers have stopped
func (p *Pool) Wait() {
	p.wg.Wait()
	p.logger.Info().Msg("all workers stopped")
}

// WorkerCount returns the number of workers in the pool
func (p *Pool) WorkerCount() int {
	return len(p.workers)
}
