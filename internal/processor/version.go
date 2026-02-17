package processor

import "github.com/lowreyarchives/media-processor/internal/models"

// Engine versions are bumped when processing logic changes materially.
// Media processed with an older version will be automatically reprocessed.
const (
	// VideoEngineVersion 4: skip transcode for web-ready H.264/MP4 videos
	VideoEngineVersion = 4

	// PhotoEngineVersion 3: fix EXIF orientation handling (iPhone portrait photos)
	PhotoEngineVersion = 3
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
