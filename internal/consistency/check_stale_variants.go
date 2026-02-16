package consistency

import (
	"context"
)

// checkStaleVariants purges optimized variants where the parent media's media_url
// starts with archive/ (meaning the media is served directly from the archive).
func (c *Checker) checkStaleVariants(ctx context.Context) (CheckResult, error) {
	result := CheckResult{Check: "stale_variants"}

	stale, err := c.variantRepo.GetStaleOptimizedVariants(ctx, c.batchSize)
	if err != nil {
		result.Errors++
		return result, err
	}

	result.OrphansFound = len(stale)
	if len(stale) == 0 {
		c.logger.Debug().Msg("no stale optimized variants found")
		return result, nil
	}

	c.logger.Info().Int("count", len(stale)).Bool("dry_run", c.dryRun).Msg("found stale optimized variants")

	for _, v := range stale {
		log := c.logger.With().
			Int64("variant_id", v.ID).
			Int64("media_id", v.MediaID).
			Str("storage_path", v.StoragePath).
			Logger()

		if c.dryRun {
			log.Info().Msg("[dry-run] would purge stale optimized variant")
			continue
		}

		// Delete S3 object
		if err := c.s3Client.DeleteFromMedia(ctx, v.StoragePath); err != nil {
			log.Warn().Err(err).Msg("failed to delete S3 object for stale variant")
			// Continue — still delete the DB row
		}

		// Delete variant row
		if err := c.variantRepo.DeleteVariantByID(ctx, v.ID); err != nil {
			log.Error().Err(err).Msg("failed to delete stale variant row")
			result.Errors++
			continue
		}

		log.Info().Msg("purged stale optimized variant")
		result.OrphansRemoved++
	}

	return result, nil
}
