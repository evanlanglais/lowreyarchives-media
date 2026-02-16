package consistency

import (
	"context"
)

// checkMissingObjects purges variant rows whose storage_path points to an S3 object
// that no longer exists. Gated by CONSISTENCY_FULL_SCAN.
func (c *Checker) checkMissingObjects(ctx context.Context) (CheckResult, error) {
	result := CheckResult{Check: "missing_objects"}

	offset := 0
	for {
		variants, err := c.variantRepo.GetAllVariantsWithMediaInfo(ctx, c.batchSize, offset)
		if err != nil {
			result.Errors++
			return result, err
		}
		if len(variants) == 0 {
			break
		}

		for _, v := range variants {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			default:
			}

			exists, err := c.s3Client.ObjectExistsInMedia(ctx, v.StoragePath)
			if err != nil {
				c.logger.Warn().Err(err).
					Int64("variant_id", v.ID).
					Str("storage_path", v.StoragePath).
					Msg("failed to HEAD object, skipping")
				result.Errors++
				continue
			}

			if exists {
				continue
			}

			result.OrphansFound++
			log := c.logger.With().
				Int64("variant_id", v.ID).
				Int64("media_id", v.MediaID).
				Str("storage_path", v.StoragePath).
				Logger()

			if c.dryRun {
				log.Info().Msg("[dry-run] would delete variant row for missing S3 object")
				continue
			}

			if err := c.variantRepo.DeleteVariantByID(ctx, v.ID); err != nil {
				log.Error().Err(err).Msg("failed to delete variant row")
				result.Errors++
				continue
			}

			log.Info().Msg("deleted variant row for missing S3 object")
			result.OrphansRemoved++
		}

		offset += len(variants)
	}

	return result, nil
}
