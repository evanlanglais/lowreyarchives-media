# LowreyArchives Media Processor

Go application for processing media files (videos and photos) for the LowreyArchives platform.

## Architecture

```
cmd/processor/          - Main application entry point
internal/
  config/               - Environment-based configuration
  db/                   - PostgreSQL database layer (Supabase compatible)
  models/               - Data models (Media, ProcessingJob, Event)
  processor/            - Video (ffmpeg) and image processing
  queue/                - Job scanner that creates processing jobs
  storage/              - S3 client for Garage integration
  worker/               - Worker pool that processes jobs
deployments/k8s/        - Kubernetes manifests for k3s deployment
```

## Processing Flow

1. Media uploaded to `fa-dmz` (ingest) bucket via Nuxt app
2. Scanner detects new media entries in database with ingest paths
3. Creates `ProcessingJob` entries in `processing_jobs` table
4. Workers claim jobs atomically (using `FOR UPDATE SKIP LOCKED`)
5. Worker downloads file from ingest bucket to local temp storage
6. Processes video (ffmpeg transcode) or image (resize/optimize)
7. Archives original to `fa-media/archive/`
8. Uploads optimized version to `fa-media/optimized/`
9. Updates `media` table with new URL
10. Deletes original from ingest bucket
11. Marks job as completed

## Database Tables

Uses existing Supabase tables:
- `media` - Media entries with URLs and types
- `events` - Event entries
- `event-media` - Junction table

Adds new table:
- `processing_jobs` - Tracks processing state and retry logic

## Configuration

All config via environment variables (see `internal/config/config.go`):

```bash
DATABASE_URL=postgres://...
S3_ENDPOINT=https://garage-media-s3.lowreyarchives.com
S3_ACCESS_KEY=...
S3_SECRET_KEY=...
S3_INGEST_BUCKET=fa-dmz
S3_MEDIA_BUCKET=fa-media
WORKER_COUNT=4
```

## Building

```bash
go build -o media-processor ./cmd/processor
```

## Docker

```bash
docker build -t lowreyarchives/media-processor:latest .
```

## Kubernetes Deployment

```bash
# Update secrets in deployments/k8s/secret.yaml first
kubectl apply -k deployments/k8s/
```

## Local Development

```bash
# Set environment variables
export DATABASE_URL="postgres://..."
export S3_ENDPOINT="https://garage-media-s3.lowreyarchives.com"
export S3_ACCESS_KEY="..."
export S3_SECRET_KEY="..."

# Run
go run ./cmd/processor
```
