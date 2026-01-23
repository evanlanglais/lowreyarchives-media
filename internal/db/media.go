package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lowreyarchives/media-processor/internal/models"
)

// MediaRepository handles database operations for media entries
type MediaRepository struct {
	db *DB
}

// NewMediaRepository creates a new media repository
func NewMediaRepository(db *DB) *MediaRepository {
	return &MediaRepository{db: db}
}

// GetByID retrieves a media entry by its ID
func (r *MediaRepository) GetByID(ctx context.Context, id int64) (*models.Media, error) {
	query := `
		SELECT id, media_url, media_type, description, created_at, created_by
		FROM media WHERE id = $1`

	media := &models.Media{}
	err := r.db.pool.QueryRow(ctx, query, id).Scan(
		&media.ID, &media.MediaURL, &media.MediaType,
		&media.Description, &media.CreatedAt, &media.CreatedBy,
	)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get media: %w", err)
	}
	return media, nil
}

// UpdateMediaURL updates the media URL and type after processing
func (r *MediaRepository) UpdateMediaURL(ctx context.Context, id int64, newURL string, mediaType models.MediaType) error {
	query := `UPDATE media SET media_url = $1, media_type = $2 WHERE id = $3`
	_, err := r.db.pool.Exec(ctx, query, newURL, mediaType, id)
	if err != nil {
		return fmt.Errorf("failed to update media URL: %w", err)
	}
	return nil
}

// GetUnprocessedMedia finds media entries that need processing
// These are media with ingest bucket paths that don't have corresponding processing jobs
func (r *MediaRepository) GetUnprocessedMedia(ctx context.Context, ingestBucketPrefix string) ([]*models.Media, error) {
	query := `
		SELECT m.id, m.media_url, m.media_type, m.description, m.created_at, m.created_by
		FROM media m
		WHERE m.media_url LIKE $1
		AND NOT EXISTS (
			SELECT 1 FROM processing_jobs pj
			WHERE pj.media_id = m.id AND pj.status NOT IN ('failed')
		)`

	rows, err := r.db.pool.Query(ctx, query, ingestBucketPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("failed to get unprocessed media: %w", err)
	}
	defer rows.Close()

	var mediaList []*models.Media
	for rows.Next() {
		media := &models.Media{}
		err := rows.Scan(
			&media.ID, &media.MediaURL, &media.MediaType,
			&media.Description, &media.CreatedAt, &media.CreatedBy,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan media: %w", err)
		}
		mediaList = append(mediaList, media)
	}
	return mediaList, nil
}

// GetEventIDForMedia retrieves the event ID associated with a media entry
func (r *MediaRepository) GetEventIDForMedia(ctx context.Context, mediaID int64) (*int64, error) {
	var eventID int64
	err := r.db.pool.QueryRow(ctx,
		`SELECT event_id FROM "event-media" WHERE media_id = $1 LIMIT 1`,
		mediaID,
	).Scan(&eventID)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get event for media: %w", err)
	}
	return &eventID, nil
}
