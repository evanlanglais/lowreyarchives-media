package consistency

import (
	"context"
	"time"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/lowreyarchives/media-processor/internal/db"
	"github.com/lowreyarchives/media-processor/internal/storage"
	"github.com/rs/zerolog"
)

// CheckResult holds the outcome of a single consistency check.
type CheckResult struct {
	Check          string
	OrphansFound   int
	OrphansRemoved int
	Errors         int
}

// Checker orchestrates all consistency checks.
type Checker struct {
	cfg         *config.Config
	mediaRepo   *db.MediaRepository
	variantRepo *db.VariantRepository
	s3Client    *storage.S3Client
	logger      zerolog.Logger
	gracePeriod time.Duration
	dryRun      bool
	batchSize   int
	fullScan    bool
}

// NewChecker creates a new consistency checker.
func NewChecker(
	cfg *config.Config,
	mediaRepo *db.MediaRepository,
	variantRepo *db.VariantRepository,
	s3Client *storage.S3Client,
	logger zerolog.Logger,
) *Checker {
	return &Checker{
		cfg:         cfg,
		mediaRepo:   mediaRepo,
		variantRepo: variantRepo,
		s3Client:    s3Client,
		logger:      logger.With().Str("component", "consistency").Logger(),
		gracePeriod: time.Duration(cfg.ConsistencyGraceAgeSec) * time.Second,
		dryRun:      cfg.ConsistencyDryRun,
		batchSize:   cfg.ConsistencyBatchSize,
		fullScan:    cfg.ConsistencyFullScan,
	}
}

// Run executes all consistency checks in order and returns results.
func (c *Checker) Run(ctx context.Context) ([]CheckResult, error) {
	c.logger.Info().
		Bool("dry_run", c.dryRun).
		Dur("grace_period", c.gracePeriod).
		Int("batch_size", c.batchSize).
		Bool("full_scan", c.fullScan).
		Msg("starting consistency check")

	var results []CheckResult

	// Check 1: Purge media not linked to any event
	r1, err := c.checkUnlinkedMedia(ctx)
	if err != nil {
		c.logger.Error().Err(err).Msg("check_unlinked failed")
	}
	results = append(results, r1)

	// Check 2: Purge stale optimized variants
	r2, err := c.checkStaleVariants(ctx)
	if err != nil {
		c.logger.Error().Err(err).Msg("check_stale_variants failed")
	}
	results = append(results, r2)

	// Check 3: Purge variant rows pointing to missing S3 objects (gated by full_scan)
	if c.fullScan {
		r3, err := c.checkMissingObjects(ctx)
		if err != nil {
			c.logger.Error().Err(err).Msg("check_missing_objects failed")
		}
		results = append(results, r3)
	}

	// Check 4: Purge S3 objects with no variant row
	r4, err := c.checkOrphanObjects(ctx)
	if err != nil {
		c.logger.Error().Err(err).Msg("check_orphan_objects failed")
	}
	results = append(results, r4)

	c.logger.Info().
		Int("checks_run", len(results)).
		Msg("consistency check completed")

	return results, nil
}
