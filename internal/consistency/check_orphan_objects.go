package consistency

import (
	"context"
	"time"
)

// checkOrphanObjects purges S3 objects in the media bucket that have no corresponding
// variant row. Scans archive/, optimized/, and thumbnails/ prefixes.
func (c *Checker) checkOrphanObjects(ctx context.Context) (CheckResult, error) {
	result := CheckResult{Check: "orphan_objects"}

	// Load all known variant storage paths into a set
	knownPaths, err := c.variantRepo.GetAllVariantStoragePaths(ctx)
	if err != nil {
		result.Errors++
		return result, err
	}

	c.logger.Info().Int("known_paths", len(knownPaths)).Msg("loaded variant storage paths")

	cutoff := time.Now().Add(-c.gracePeriod)
	prefixes := []string{"archive/", "optimized/", "thumbnails/"}

	for _, prefix := range prefixes {
		objCh := c.s3Client.ListMediaObjects(ctx, prefix)
		for obj := range objCh {
			if obj.Err != nil {
				c.logger.Error().Err(obj.Err).Str("prefix", prefix).Msg("error listing media objects")
				result.Errors++
				break
			}

			select {
			case <-ctx.Done():
				return result, ctx.Err()
			default:
			}

			// Skip objects within the grace period
			if obj.LastModified.After(cutoff) {
				continue
			}

			// Check if this object is tracked by a variant
			if _, tracked := knownPaths[obj.Key]; tracked {
				continue
			}

			result.OrphansFound++
			log := c.logger.With().
				Str("key", obj.Key).
				Time("last_modified", obj.LastModified).
				Int64("size", obj.Size).
				Logger()

			if c.dryRun {
				log.Info().Msg("[dry-run] would delete orphan S3 object")
				continue
			}

			if err := c.s3Client.DeleteFromMedia(ctx, obj.Key); err != nil {
				log.Error().Err(err).Msg("failed to delete orphan S3 object")
				result.Errors++
				continue
			}

			log.Info().Msg("deleted orphan S3 object")
			result.OrphansRemoved++
		}
	}

	return result, nil
}
