package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lowreyarchives/media-processor/internal/config"
	"github.com/lowreyarchives/media-processor/internal/db"
	"github.com/lowreyarchives/media-processor/internal/queue"
	"github.com/lowreyarchives/media-processor/internal/storage"
	"github.com/lowreyarchives/media-processor/internal/webhook"
	"github.com/lowreyarchives/media-processor/internal/worker"
	"github.com/rs/zerolog"
)

func main() {
	// Initialize logger
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load configuration")
	}

	// Set log level
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	logger.Info().
		Int("worker_count", cfg.WorkerCount).
		Str("s3_endpoint", cfg.S3Endpoint).
		Str("ingest_bucket", cfg.S3IngestBucket).
		Str("media_bucket", cfg.S3MediaBucket).
		Msg("starting media processor")

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize database connection
	database, err := db.New(ctx, cfg)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer database.Close()

	// Run migrations
	//if err := database.RunMigrations(ctx); err != nil {
	//	logger.Fatal().Err(err).Msg("failed to run migrations")
	//}
	//logger.Info().Msg("database migrations completed")

	// Initialize S3 client
	s3Client, err := storage.NewS3Client(cfg)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create S3 client")
	}

	// Create repositories
	jobRepo := db.NewJobRepository(database)
	mediaRepo := db.NewMediaRepository(database)
	variantRepo := db.NewVariantRepository(database)

	// Create webhook client for cache invalidation
	webhookClient := webhook.NewClient(cfg.WebhookURL, cfg.WebhookToken, cfg.WebhookTimeout, logger)
	if webhookClient.Enabled() {
		logger.Info().Str("webhook", webhookClient.String()).Msg("cache invalidation webhook enabled")
	} else {
		logger.Info().Msg("cache invalidation webhook disabled (no WEBHOOK_URL configured)")
	}

	// Create and start worker pool
	pool := worker.NewPool(cfg, jobRepo, mediaRepo, variantRepo, s3Client, webhookClient, logger)
	if err := pool.Start(ctx); err != nil {
		logger.Fatal().Err(err).Msg("failed to start worker pool")
	}

	// Create and start scanner
	scanner := queue.NewScanner(cfg, jobRepo, mediaRepo, s3Client, logger)
	go scanner.Run(ctx)

	// Start health check server
	healthServer := startHealthServer(cfg, jobRepo, logger)

	// Wait for shutdown signal
	sig := <-sigChan
	logger.Info().Str("signal", sig.String()).Msg("received shutdown signal")

	// Cancel context to stop workers
	cancel()

	// Shutdown health server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn().Err(err).Msg("health server shutdown error")
	}

	// Wait for workers to finish
	pool.Wait()

	logger.Info().Msg("media processor stopped")
}

// startHealthServer starts the health check HTTP server
func startHealthServer(cfg *config.Config, jobRepo *db.JobRepository, logger zerolog.Logger) *http.Server {
	mux := http.NewServeMux()

	// Liveness probe - just checks if the service is running
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Readiness probe - checks if the service can process jobs
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		// Check if we can query the database
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		count, err := jobRepo.GetPendingCount(ctx)
		if err != nil {
			logger.Error().Err(err).Msg("readiness check failed")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("database unavailable"))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf("ok, pending jobs: %d", count)))
	})

	// Metrics endpoint (basic)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		count, _ := jobRepo.GetPendingCount(ctx)

		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "# HELP media_processor_pending_jobs Number of pending processing jobs\n")
		fmt.Fprintf(w, "# TYPE media_processor_pending_jobs gauge\n")
		fmt.Fprintf(w, "media_processor_pending_jobs %d\n", count)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HealthPort),
		Handler: mux,
	}

	go func() {
		logger.Info().Int("port", cfg.HealthPort).Msg("starting health server")
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("health server error")
		}
	}()

	return server
}
