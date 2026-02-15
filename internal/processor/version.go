package processor

import "github.com/lowreyarchives/media-processor/internal/models"

// Engine versions are bumped when processing logic changes materially.
// Media processed with an older version will be automatically reprocessed.
const (
	// VideoEngineVersion 2: improved interlace detection via idet filter
	VideoEngineVersion = 2

	// PhotoEngineVersion 1: initial release
	PhotoEngineVersion = 1
)

// CurrentEngineVersion returns the current engine version for a given job type.
func CurrentEngineVersion(jobType models.JobType) int {
	switch jobType {
	case models.JobTypeVideo:
		return VideoEngineVersion
	case models.JobTypePhoto:
		return PhotoEngineVersion
	default:
		return 1
	}
}
