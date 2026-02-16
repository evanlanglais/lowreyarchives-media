package db

import (
	"context"
	"fmt"
	"time"

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
		SELECT id, media_url, media_type, description, status, created_at, created_by
		FROM media WHERE id = $1`

	media := &models.Media{}
	err := r.db.pool.QueryRow(ctx, query, id).Scan(
		&media.ID, &media.MediaURL, &media.MediaType,
		&media.Description, &media.Status, &media.CreatedAt, &media.CreatedBy,
	)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get media: %w", err)
	}
	return media, nil
}

// UpdateMediaURL updates the media URL, type, and sets status to ready after processing
func (r *MediaRepository) UpdateMediaURL(ctx context.Context, id int64, newURL string, mediaType models.MediaType) error {
	query := `UPDATE media SET media_url = $1, media_type = $2, status = $3 WHERE id = $4`
	_, err := r.db.pool.Exec(ctx, query, newURL, mediaType, models.MediaStatusReady, id)
	if err != nil {
		return fmt.Errorf("failed to update media URL: %w", err)
	}
	return nil
}

// UpdateStatus updates the media status
func (r *MediaRepository) UpdateStatus(ctx context.Context, id int64, status models.MediaStatus) error {
	query := `UPDATE media SET status = $1 WHERE id = $2`
	_, err := r.db.pool.Exec(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update media status: %w", err)
	}
	return nil
}

// GetUnprocessedMedia finds media entries that need processing
// These are media with ingest paths (media_url starts with 'ingest/') that don't have corresponding processing jobs
// and have status 'pending' or null
func (r *MediaRepository) GetUnprocessedMedia(ctx context.Context, ingestPathPrefix string) ([]*models.Media, error) {
	query := `
		SELECT m.id, m.media_url, m.media_type, m.description, m.status, m.created_at, m.created_by
		FROM media m
		WHERE m.media_url LIKE $1
		AND (m.status IS NULL OR m.status = 'pending')
		AND NOT EXISTS (
			SELECT 1 FROM processing_jobs pj
			WHERE pj.media_id = m.id AND pj.status NOT IN ('failed')
		)`

	rows, err := r.db.pool.Query(ctx, query, ingestPathPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("failed to get unprocessed media: %w", err)
	}
	defer rows.Close()

	var mediaList []*models.Media
	for rows.Next() {
		media := &models.Media{}
		err := rows.Scan(
			&media.ID, &media.MediaURL, &media.MediaType,
			&media.Description, &media.Status, &media.CreatedAt, &media.CreatedBy,
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

// ExistsWithIngestPath checks if a media record exists with the given ingest path
func (r *MediaRepository) ExistsWithIngestPath(ctx context.Context, ingestPath string) (bool, error) {
	var exists bool
	err := r.db.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM media WHERE media_url = $1)`,
		ingestPath,
	).Scan(&exists)

	if err != nil {
		return false, fmt.Errorf("failed to check media existence: %w", err)
	}
	return exists, nil
}

// GetOrphanedMedia returns media with status 'ready', created before the grace period,
// that have no row in the "event-media" junction table.
func (r *MediaRepository) GetOrphanedMedia(ctx context.Context, gracePeriod time.Duration, limit int) ([]*models.Media, error) {
	cutoff := time.Now().Add(-gracePeriod)
	query := `
		SELECT m.id, m.media_url, m.media_type, m.description, m.status, m.created_at, m.created_by
		FROM media m
		WHERE m.status = 'ready'
		AND m.created_at < $1
		AND NOT EXISTS (
			SELECT 1 FROM "event-media" em WHERE em.media_id = m.id
		)
		ORDER BY m.id
		LIMIT $2`

	rows, err := r.db.pool.Query(ctx, query, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get orphaned media: %w", err)
	}
	defer rows.Close()

	var mediaList []*models.Media
	for rows.Next() {
		media := &models.Media{}
		err := rows.Scan(
			&media.ID, &media.MediaURL, &media.MediaType,
			&media.Description, &media.Status, &media.CreatedAt, &media.CreatedBy,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan orphaned media: %w", err)
		}
		mediaList = append(mediaList, media)
	}
	return mediaList, nil
}

// DeleteMedia deletes a media row by ID.
// Cascade deletes handle processing_jobs; variants should be deleted explicitly first.
func (r *MediaRepository) DeleteMedia(ctx context.Context, mediaID int64) error {
	_, err := r.db.pool.Exec(ctx, `DELETE FROM media WHERE id = $1`, mediaID)
	if err != nil {
		return fmt.Errorf("failed to delete media: %w", err)
	}
	return nil
}
