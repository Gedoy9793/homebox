package imagesearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	defaultHTTPTimeout = 60 * time.Second
)

// SearchHit is one result from the sidecar search endpoint.
type SearchHit struct {
	AttachmentID uuid.UUID `json:"attachment_id"`
	EntityID     uuid.UUID `json:"entity_id"`
	Score        float64   `json:"score"`
}

// Client talks to the Python image-search sidecar over HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
	topK       int
}

// NewClient builds a client for the given config. Caller must ensure Enabled().
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL: cfg.URL,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		topK: cfg.TopK,
	}
}

// TopK returns the configured default search result limit.
func (c *Client) TopK() int {
	if c == nil || c.topK <= 0 {
		return defaultTopK
	}
	return c.topK
}

// Index upserts one attachment image into the sidecar index.
func (c *Client) Index(ctx context.Context, groupID, attachmentID, entityID uuid.UUID, filename string, content io.Reader) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fields := map[string]string{
		"group_id":      groupID.String(),
		"attachment_id": attachmentID.String(),
		"entity_id":     entityID.String(),
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return fmt.Errorf("image-search index field %s: %w", k, err)
		}
	}

	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("image-search index form file: %w", err)
	}
	if _, err := io.Copy(part, content); err != nil {
		return fmt.Errorf("image-search index copy: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("image-search index close: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/index", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("image-search index: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// Delete removes one attachment vector from the sidecar index.
func (c *Client) Delete(ctx context.Context, groupID, attachmentID uuid.UUID) error {
	u := c.baseURL + path.Join("/v1/index", url.PathEscape(groupID.String()), url.PathEscape(attachmentID.String()))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("image-search delete: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// ListIDs returns attachment IDs already indexed for a group.
func (c *Client) ListIDs(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	u := c.baseURL + path.Join("/v1/index", url.PathEscape(groupID.String()), "ids")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("image-search list ids: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var payload struct {
		IDs           []uuid.UUID `json:"ids"`
		AttachmentIDs []uuid.UUID `json:"attachment_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("image-search list ids decode: %w", err)
	}
	if len(payload.IDs) > 0 {
		return payload.IDs, nil
	}
	return payload.AttachmentIDs, nil
}

// Search finds nearest neighbors for an uploaded image.
func (c *Client) Search(ctx context.Context, groupID uuid.UUID, filename string, content io.Reader, topK int) ([]SearchHit, error) {
	if topK <= 0 {
		topK = c.TopK()
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if err := w.WriteField("group_id", groupID.String()); err != nil {
		return nil, err
	}
	if err := w.WriteField("top_k", strconv.Itoa(topK)); err != nil {
		return nil, err
	}

	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, content); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/search", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("image-search search: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var payload struct {
		Results []SearchHit `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("image-search search decode: %w", err)
	}
	return payload.Results, nil
}

// Healthz checks sidecar liveness.
func (c *Client) Healthz(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("image-search healthz: status %d", resp.StatusCode)
	}
	return nil
}
