package db

import (
	"context"
	"fmt"

	"github.com/lowreyarchives/media-processor/internal/models"
)

// VariantRepository handles database operations for media variants
type VariantRepository struct {
	db *DB
}

// NewVariantRepository creates a new variant repository
func NewVariantRepository(db *DB) *VariantRepository {
	return &VariantRepository{db: db}
}

// UpsertMediaVariant inserts or updates a media variant record
// Uses ON CONFLICT to handle re-processing of media
func (r *VariantRepository) UpsertMediaVariant(ctx context.Context, v *models.MediaVariant) error {
	query := `
		INSERT INTO media_variants (
			media_id, variant_type, bucket, storage_path,
			file_size, mime_type, width, height, duration_seconds
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (media_id, variant_type) DO UPDATE SET
			storage_path = EXCLUDED.storage_path,
			file_size = EXCLUDED.file_size,
			mime_type = EXCLUDED.mime_type,
			width = EXCLUDED.width,
			height = EXCLUDED.height,
			duration_seconds = EXCLUDED.duration_seconds,
			created_at = NOW()
		RETURNING id`

	err := r.db.pool.QueryRow(ctx, query,
		v.MediaID, v.VariantType, v.Bucket, v.StoragePath,
		v.FileSize, v.MimeType, v.Width, v.Height, v.DurationSeconds,
	).Scan(&v.ID)

	if err != nil {
		return fmt.Errorf("failed to upsert media variant: %w", err)
	}
	return nil
}

// UpsertMediaVariants inserts or updates multiple media variant records
func (r *VariantRepository) UpsertMediaVariants(ctx context.Context, variants []*models.MediaVariant) error {
	for _, v := range variants {
		if err := r.UpsertMediaVariant(ctx, v); err != nil {
			return err
		}
	}
	return nil
}

// GetVariantsForMedia retrieves all variants for a media item
func (r *VariantRepository) GetVariantsForMedia(ctx context.Context, mediaID int64) ([]*models.MediaVariant, error) {
	query := `
		SELECT id, media_id, variant_type, bucket, storage_path,
			   file_size, mime_type, width, height, duration_seconds, created_at
		FROM media_variants
		WHERE media_id = $1
		ORDER BY variant_type`

	rows, err := r.db.pool.Query(ctx, query, mediaID)
	if err != nil {
		return nil, fmt.Errorf("failed to get variants for media: %w", err)
	}
	defer rows.Close()

	var variants []*models.MediaVariant
	for rows.Next() {
		v := &models.MediaVariant{}
		err := rows.Scan(
			&v.ID, &v.MediaID, &v.VariantType, &v.Bucket, &v.StoragePath,
			&v.FileSize, &v.MimeType, &v.Width, &v.Height, &v.DurationSeconds, &v.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan variant: %w", err)
		}
		variants = append(variants, v)
	}

	return variants, nil
}

// DeleteVariantsForMedia deletes all variants for a media item
func (r *VariantRepository) DeleteVariantsForMedia(ctx context.Context, mediaID int64) error {
	_, err := r.db.pool.Exec(ctx,
		"DELETE FROM media_variants WHERE media_id = $1",
		mediaID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete variants: %w", err)
	}
	return nil
}
