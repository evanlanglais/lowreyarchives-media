package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Client handles sending cache invalidation webhooks to the Nuxt frontend.
type Client struct {
	url        string
	token      string
	httpClient *http.Client
	logger     zerolog.Logger
}

// MediaProcessedPayload is the payload sent to the cache invalidation webhook.
type MediaProcessedPayload struct {
	MediaID int64  `json:"mediaId"`
	EventID *int64 `json:"eventId,omitempty"`
	Status  string `json:"status"` // "processing", "ready", "failed"
}

// NewClient creates a new webhook client.
// If url is empty, the client will be disabled and all calls will be no-ops.
func NewClient(url, token string, timeoutSec int, logger zerolog.Logger) *Client {
	return &Client{
		url:   url,
		token: token,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
		logger: logger.With().Str("component", "webhook").Logger(),
	}
}

// Enabled returns true if the webhook client is configured.
func (c *Client) Enabled() bool {
	return c.url != ""
}

// NotifyMediaProcessed sends a cache invalidation webhook for a media status change.
// This is a fire-and-forget operation - errors are logged but not returned.
func (c *Client) NotifyMediaProcessed(ctx context.Context, mediaID int64, eventID *int64, status string) {
	if !c.Enabled() {
		return
	}

	payload := MediaProcessedPayload{
		MediaID: mediaID,
		EventID: eventID,
		Status:  status,
	}

	go c.sendWebhook(ctx, payload)
}

// sendWebhook sends the webhook request asynchronously.
func (c *Client) sendWebhook(ctx context.Context, payload MediaProcessedPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		c.logger.Error().Err(err).Msg("Failed to marshal webhook payload")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		c.logger.Error().Err(err).Msg("Failed to create webhook request")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("x-nuxt-multi-cache-token", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn().
			Err(err).
			Int64("mediaId", payload.MediaID).
			Str("status", payload.Status).
			Msg("Failed to send cache invalidation webhook")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.logger.Debug().
			Int64("mediaId", payload.MediaID).
			Str("status", payload.Status).
			Int("statusCode", resp.StatusCode).
			Msg("Cache invalidation webhook sent successfully")
	} else {
		c.logger.Warn().
			Int64("mediaId", payload.MediaID).
			Str("status", payload.Status).
			Int("statusCode", resp.StatusCode).
			Msg("Cache invalidation webhook returned non-success status")
	}
}

// NotifyMediaReady is a convenience method for when media processing completes successfully.
func (c *Client) NotifyMediaReady(ctx context.Context, mediaID int64, eventID *int64) {
	c.NotifyMediaProcessed(ctx, mediaID, eventID, "ready")
}

// NotifyMediaFailed is a convenience method for when media processing fails.
func (c *Client) NotifyMediaFailed(ctx context.Context, mediaID int64, eventID *int64) {
	c.NotifyMediaProcessed(ctx, mediaID, eventID, "failed")
}

// NotifyMediaProcessing is a convenience method for when media processing starts.
func (c *Client) NotifyMediaProcessing(ctx context.Context, mediaID int64, eventID *int64) {
	c.NotifyMediaProcessed(ctx, mediaID, eventID, "processing")
}

// String returns a string representation of the client for logging.
func (c *Client) String() string {
	if !c.Enabled() {
		return "webhook.Client(disabled)"
	}
	return fmt.Sprintf("webhook.Client(url=%s)", c.url)
}