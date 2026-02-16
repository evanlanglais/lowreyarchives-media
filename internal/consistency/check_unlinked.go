package consistency

import (
	"context"
)

// checkUnlinkedMedia purges media records not linked to any event.
func (c *Checker) checkUnlinkedMedia(ctx context.Context) (CheckResult, error) {
	result := CheckResult{Check: "unlinked_media"}

	orphans, err := c.mediaRepo.GetOrphanedMedia(ctx, c.gracePeriod, c.batchSize)
	if err != nil {
		result.Errors++
		return result, err
	}

	result.OrphansFound = len(orphans)
	if len(orphans) == 0 {
		c.logger.Debug().Msg("no unlinked media found")
		return result, nil
	}

	c.logger.Info().Int("count", len(orphans)).Bool("dry_run", c.dryRun).Msg("found unlinked media")

	for _, media := range orphans {
		log := c.logger.With().Int64("media_id", media.ID).Str("media_url", media.MediaURL).Logger()

		if c.dryRun {
			log.Info().Msg("[dry-run] would purge unlinked media")
			continue
		}

		// Delete S3 objects for all variants
		variants, err := c.variantRepo.GetVariantsForMedia(ctx, media.ID)
		if err != nil {
			log.Error().Err(err).Msg("failed to get variants for media")
			result.Errors++
			continue
		}

		for _, v := range variants {
			if err := c.s3Client.DeleteFromMedia(ctx, v.StoragePath); err != nil {
				log.Warn().Err(err).Str("path", v.StoragePath).Msg("failed to delete S3 object for variant")
				// Continue — best effort
			}
		}

		// Delete variant rows
		if err := c.variantRepo.DeleteVariantsForMedia(ctx, media.ID); err != nil {
			log.Error().Err(err).Msg("failed to delete variants")
			result.Errors++
			continue
		}

		// Delete media row (FK cascades handle processing_jobs)
		if err := c.mediaRepo.DeleteMedia(ctx, media.ID); err != nil {
			log.Error().Err(err).Msg("failed to delete media")
			result.Errors++
			continue
		}

		log.Info().Msg("purged unlinked media")
		result.OrphansRemoved++
	}

	return result, nil
}
