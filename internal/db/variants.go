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

// DeleteVariantByID deletes a single variant row by ID
func (r *VariantRepository) DeleteVariantByID(ctx context.Context, variantID int64) error {
	_, err := r.db.pool.Exec(ctx,
		"DELETE FROM media_variants WHERE id = $1",
		variantID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete variant: %w", err)
	}
	return nil
}

// GetStaleOptimizedVariants returns optimized variants where the parent media's
// media_url starts with 'archive/' (meaning the optimized variant is no longer needed).
func (r *VariantRepository) GetStaleOptimizedVariants(ctx context.Context, limit int) ([]*models.MediaVariant, error) {
	query := `
		SELECT mv.id, mv.media_id, mv.variant_type, mv.bucket, mv.storage_path,
			   mv.file_size, mv.mime_type, mv.width, mv.height, mv.duration_seconds, mv.created_at
		FROM media_variants mv
		JOIN media m ON m.id = mv.media_id
		WHERE mv.variant_type = 'optimized'
		AND m.media_url LIKE 'archive/%'
		ORDER BY mv.id
		LIMIT $1`

	rows, err := r.db.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get stale optimized variants: %w", err)
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
			return nil, fmt.Errorf("failed to scan stale variant: %w", err)
		}
		variants = append(variants, v)
	}
	return variants, nil
}

// GetAllVariantStoragePaths returns a set of all storage_path values across all variants.
func (r *VariantRepository) GetAllVariantStoragePaths(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.db.pool.Query(ctx, "SELECT storage_path FROM media_variants")
	if err != nil {
		return nil, fmt.Errorf("failed to get variant storage paths: %w", err)
	}
	defer rows.Close()

	paths := make(map[string]struct{})
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan storage path: %w", err)
		}
		paths[path] = struct{}{}
	}
	return paths, nil
}

// GetAllVariantsWithMediaInfo returns a paginated list of all variants.
func (r *VariantRepository) GetAllVariantsWithMediaInfo(ctx context.Context, limit, offset int) ([]*models.MediaVariant, error) {
	query := `
		SELECT id, media_id, variant_type, bucket, storage_path,
			   file_size, mime_type, width, height, duration_seconds, created_at
		FROM media_variants
		ORDER BY id
		LIMIT $1 OFFSET $2`

	rows, err := r.db.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get variants with media info: %w", err)
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
