# LowreyArchives Media Processor

A Go-based media processing service for the LowreyArchives platform. It automatically processes uploaded videos and images—transcoding, optimizing, generating thumbnails, and archiving originals—as part of an event-driven media management system.

## Overview

When users upload media through the LowreyArchives web application (Nuxt), files land in an S3-compatible ingest bucket. This processor monitors the database for new media entries, claims processing jobs, and handles the full pipeline:

1. Download from ingest bucket
2. Transcode video (ffmpeg) or optimize images
3. Generate thumbnails
4. Archive the original file
5. Upload optimized versions
6. Update database with new URLs
7. Clean up ingest bucket

The system uses a worker pool architecture with atomic job claiming (`FOR UPDATE SKIP LOCKED`) to support horizontal scaling without processing conflicts.

## Architecture

```
cmd/processor/          Main entry point
internal/
├── config/             Environment-based configuration (envconfig)
├── db/                 PostgreSQL repositories (pgx)
│   ├── jobs.go         Processing job CRUD with atomic claiming
│   ├── media.go        Media entry updates
│   └── variants.go     Media variant tracking (original, optimized, thumbnail)
├── models/             Domain models
├── processor/          Media processing
│   ├── video.go        FFmpeg transcoding, deinterlacing, thumbnails
│   └── image.go        Image resizing and optimization
├── queue/              Job scanner - detects new media needing processing
├── storage/            S3 client (minio-go) for Garage/S3-compatible storage
└── worker/             Worker pool with heartbeat monitoring
```

## Processing Pipeline

### Video Processing

- Extracts metadata via `ffprobe` (dimensions, duration, codec, interlacing)
- Detects and handles interlaced content with yadif deinterlacing
- Transcodes to H.264/AAC in MP4 container
- Applies configurable CRF quality and resolution limits
- Generates thumbnail from 10% into the video
- Enables `faststart` for web streaming

### Image Processing

- Resizes to configurable maximum dimensions
- Converts to optimized JPEG
- Preserves aspect ratio
- Generates smaller thumbnail variant

### Storage Strategy

```
fa-dmz (ingest bucket)
└── ingest/                    Uploaded files land here

fa-media (media bucket)
├── archive/                   Original files preserved
├── optimized/                 Transcoded/optimized versions
└── thumbnails/                Generated thumbnails
```

## Technologies

| Component | Technology |
|-----------|------------|
| Language | Go 1.25 |
| Database | PostgreSQL (Supabase compatible) |
| DB Driver | pgx/v5 |
| Object Storage | S3-compatible (Garage) via minio-go |
| Video Processing | FFmpeg |
| Image Processing | Go stdlib (golang.org/x/image) |
| Logging | zerolog |
| Configuration | envconfig |

## Database Schema

Uses existing Supabase tables:

- `media` - Media entries with URLs, types, and processing status
- `events` - Event entries
- `event-media` - Junction table linking events to media

Adds:

- `processing_jobs` - Job queue with status, retry logic, heartbeat tracking
- `media_variants` - Tracks all variants (original, optimized, thumbnail) with metadata

## Configuration

All configuration via environment variables:

```bash
# Database
DATABASE_URL=postgres://user:password@host:5432/dbname

# S3/Garage
S3_ENDPOINT=https://garage-media-s3.lowreyarchives.com
S3_REGION=garage
S3_ACCESS_KEY=...
S3_SECRET_KEY=...
S3_INGEST_BUCKET=fa-dmz
S3_INGEST_PATH=ingest
S3_MEDIA_BUCKET=fa-media
S3_ARCHIVE_PATH=archive

# Workers
WORKER_COUNT=4
POLL_INTERVAL_SEC=10
MAX_RETRIES=3
HEARTBEAT_INTERVAL_SEC=30
HEARTBEAT_TIMEOUT_SEC=120

# Video Processing
FFMPEG_PATH=/usr/bin/ffmpeg
FFPROBE_PATH=/usr/bin/ffprobe
VIDEO_OUTPUT_FORMAT=mp4
VIDEO_CODEC=libx264
VIDEO_PRESET=medium
VIDEO_CRF=20
AUDIO_CODEC=aac
MAX_VIDEO_WIDTH=1920
MAX_VIDEO_HEIGHT=1080

# Image Processing
IMAGE_MAX_WIDTH=2048
IMAGE_MAX_HEIGHT=2048
IMAGE_QUALITY=85

# Runtime
TEMP_DIR=/data/processing
HEALTH_PORT=8080
LOG_LEVEL=info
```

## Deployment

### macOS (Native)

For deployments where native ffmpeg performance is required (e.g., home server):

```bash
# Prerequisites
brew install go ffmpeg

# Clone and install
git clone https://github.com/evanlanglais/lowreyarchives-media.git
cd lowreyarchives-media
sudo ./deployments/macos/deploy.sh install

# Configure
sudo nano /etc/media-processor/media-processor.env

# Start
sudo ./deployments/macos/deploy.sh start
```

Management commands:

```bash
sudo ./deployments/macos/deploy.sh status    # Check status
sudo ./deployments/macos/deploy.sh logs      # View logs
sudo ./deployments/macos/deploy.sh update    # Pull and rebuild
sudo ./deployments/macos/deploy.sh restart   # Restart service
```

Logs: `/var/log/media-processor/`

### Kubernetes (ArgoCD)

For cluster deployments with GitOps:

```bash
# Install ArgoCD
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

# Apply secrets manually (not in git)
kubectl apply -f deployments/k8s/secret.yaml

# Register application
kubectl apply -f deployments/argocd/media-processor.yaml
```

The ArgoCD application will sync from `deployments/k8s/` on push to master.

### Docker

```bash
docker build -t lowreyarchives/media-processor:latest .

docker run -d \
  --name media-processor \
  -e DATABASE_URL="postgres://..." \
  -e S3_ENDPOINT="https://..." \
  -e S3_ACCESS_KEY="..." \
  -e S3_SECRET_KEY="..." \
  -v /tmp/processing:/data/processing \
  lowreyarchives/media-processor:latest
```

## Development

```bash
# Clone
git clone https://github.com/evanlanglais/lowreyarchives-media.git
cd lowreyarchives-media

# Install dependencies
go mod download

# Set environment (or use .env file)
export DATABASE_URL="postgres://..."
export S3_ENDPOINT="..."
export S3_ACCESS_KEY="..."
export S3_SECRET_KEY="..."

# Run
go run ./cmd/processor

# Build
go build -o media-processor ./cmd/processor
```

## Health Endpoints

The processor exposes HTTP endpoints for monitoring:

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness probe - service is running |
| `GET /readyz` | Readiness probe - can connect to database |
| `GET /metrics` | Basic Prometheus-format metrics |

## Job States

```
pending → processing → completed
              ↓
           failed (retries until max_retries)
```

Jobs include heartbeat tracking. If a worker dies mid-processing, the scanner will detect stale heartbeats and reset jobs for retry.

## License

Private - LowreyArchives
