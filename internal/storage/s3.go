package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Client wraps the MinIO client for Garage compatibility
type S3Client struct {
	client       *minio.Client
	ingestBucket string
	mediaBucket  string
	archivePath  string
}

// NewS3Client creates a new S3 client configured for Garage
func NewS3Client(cfg *config.Config) (*S3Client, error) {
	// Parse endpoint URL to get host and determine if SSL
	endpointURL, err := url.Parse(cfg.S3Endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse S3 endpoint: %w", err)
	}

	useSSL := endpointURL.Scheme == "https"
	endpoint := endpointURL.Host

	// Create MinIO client
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		Secure: useSSL,
		Region: cfg.S3Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	return &S3Client{
		client:       client,
		ingestBucket: cfg.S3IngestBucket,
		mediaBucket:  cfg.S3MediaBucket,
		archivePath:  cfg.S3ArchivePath,
	}, nil
}

// Download downloads a file from the ingest bucket
func (s *S3Client) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.ingestBucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	return obj, nil
}

// DownloadFromMedia downloads a file from the media bucket (used for reprocessing from archive)
func (s *S3Client) DownloadFromMedia(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.mediaBucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to download %s from media bucket: %w", key, err)
	}
	return obj, nil
}

// UploadToMedia uploads a processed file to the media bucket
// For large files, use UploadFileToMedia instead which handles multipart automatically
func (s *S3Client) UploadToMedia(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, s.mediaBucket, key, body, -1, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload to media bucket: %w", err)
	}
	return nil
}

// UploadFileToMedia uploads a file from disk to the media bucket
// Uses FPutObject which automatically handles multipart uploads for large files
func (s *S3Client) UploadFileToMedia(ctx context.Context, key string, filePath string, contentType string) error {
	_, err := s.client.FPutObject(ctx, s.mediaBucket, key, filePath, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload file to media bucket: %w", err)
	}
	return nil
}

// ArchiveOriginal moves the original file to the archive path in the media bucket
// Uses ComposeObject for server-side copy which handles files of any size
func (s *S3Client) ArchiveOriginal(ctx context.Context, ingestKey string) (string, error) {
	// Construct archive path: archive/{original_filename}
	filename := path.Base(ingestKey)
	archiveKey := path.Join(s.archivePath, filename)

	// Get source object info to check size
	srcInfo, err := s.client.StatObject(ctx, s.ingestBucket, ingestKey, minio.StatObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to stat source object: %w", err)
	}

	// For files <= 5GB, use simple CopyObject (more efficient)
	// For files > 5GB, use ComposeObject which handles multipart copy
	const maxSingleCopySize = 5 * 1024 * 1024 * 1024 // 5GB

	if srcInfo.Size <= maxSingleCopySize {
		// Simple copy for smaller files
		src := minio.CopySrcOptions{
			Bucket: s.ingestBucket,
			Object: ingestKey,
		}
		dst := minio.CopyDestOptions{
			Bucket: s.mediaBucket,
			Object: archiveKey,
		}
		_, err := s.client.CopyObject(ctx, dst, src)
		if err != nil {
			return "", fmt.Errorf("failed to archive original: %w", err)
		}
	} else {
		// Use ComposeObject for large files (handles multipart copy internally)
		src := minio.CopySrcOptions{
			Bucket: s.ingestBucket,
			Object: ingestKey,
		}
		dst := minio.CopyDestOptions{
			Bucket: s.mediaBucket,
			Object: archiveKey,
		}
		_, err := s.client.ComposeObject(ctx, dst, src)
		if err != nil {
			return "", fmt.Errorf("failed to archive large original: %w", err)
		}
	}

	return archiveKey, nil
}

// DeleteFromIngest deletes a file from the ingest bucket
func (s *S3Client) DeleteFromIngest(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.ingestBucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete from ingest: %w", err)
	}
	return nil
}

// GetObjectInfo retrieves metadata about an object
func (s *S3Client) GetObjectInfo(ctx context.Context, bucket, key string) (*ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object info: %w", err)
	}

	return &ObjectInfo{
		Size:        info.Size,
		ContentType: info.ContentType,
		ETag:        info.ETag,
	}, nil
}

// ListIngestObjects lists objects in the ingest bucket with optional prefix
func (s *S3Client) ListIngestObjects(ctx context.Context, prefix string) ([]string, error) {
	var keys []string

	objectCh := s.client.ListObjects(ctx, s.ingestBucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	for obj := range objectCh {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", obj.Err)
		}
		keys = append(keys, obj.Key)
	}

	return keys, nil
}

// IngestObject represents an object in the ingest bucket with metadata
type IngestObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// ListIngestObjectsWithInfo lists objects in the ingest bucket with metadata
func (s *S3Client) ListIngestObjectsWithInfo(ctx context.Context, prefix string) ([]IngestObject, error) {
	var objects []IngestObject

	objectCh := s.client.ListObjects(ctx, s.ingestBucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	for obj := range objectCh {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", obj.Err)
		}
		objects = append(objects, IngestObject{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		})
	}

	return objects, nil
}

// ObjectInfo contains metadata about an S3 object
type ObjectInfo struct {
	Size        int64
	ContentType string
	ETag        string
}

// MediaObject represents an object in the media bucket with metadata
type MediaObject struct {
	Key          string
	Size         int64
	LastModified time.Time
	Err          error
}

// ListMediaObjects streams objects from the media bucket under the given prefix.
// Returns a channel for memory efficiency when listing large buckets.
func (s *S3Client) ListMediaObjects(ctx context.Context, prefix string) <-chan MediaObject {
	ch := make(chan MediaObject, 100)
	go func() {
		defer close(ch)
		objectCh := s.client.ListObjects(ctx, s.mediaBucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		})
		for obj := range objectCh {
			if obj.Err != nil {
				ch <- MediaObject{Err: fmt.Errorf("failed to list media objects: %w", obj.Err)}
				return
			}
			ch <- MediaObject{
				Key:          obj.Key,
				Size:         obj.Size,
				LastModified: obj.LastModified,
			}
		}
	}()
	return ch
}

// DeleteFromMedia deletes a file from the media bucket
func (s *S3Client) DeleteFromMedia(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.mediaBucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete from media: %w", err)
	}
	return nil
}

// ObjectExistsInMedia checks if an object exists in the media bucket via HEAD request.
// Returns false on NoSuchKey errors.
func (s *S3Client) ObjectExistsInMedia(ctx context.Context, key string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.mediaBucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check object existence: %w", err)
	}
	return true, nil
}

// GetMediaBucket returns the media bucket name
func (s *S3Client) GetMediaBucket() string {
	return s.mediaBucket
}

// GetIngestBucket returns the ingest bucket name
func (s *S3Client) GetIngestBucket() string {
	return s.ingestBucket
}

// IsVideoFile checks if a file is a video based on extension
func IsVideoFile(key string) bool {
	ext := strings.ToLower(path.Ext(key))
	videoExts := map[string]bool{
		".mp4": true, ".mov": true, ".avi": true, ".mkv": true,
		".webm": true, ".m4v": true, ".wmv": true, ".flv": true,
		".mpeg": true, ".mpg": true, ".3gp": true,
	}
	return videoExts[ext]
}

// IsImageFile checks if a file is an image based on extension
func IsImageFile(key string) bool {
	ext := strings.ToLower(path.Ext(key))
	imageExts := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
		".webp": true, ".bmp": true, ".tiff": true, ".heic": true,
		".heif": true, ".raw": true, ".cr2": true, ".nef": true,
	}
	return imageExts[ext]
}
