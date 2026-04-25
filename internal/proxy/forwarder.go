package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// FlowstateEvent is the wire envelope shipped to the Flowstate hosted
// telemetry endpoint. It mirrors the canonical TS schema in
// ai-telemetry/src/collector/schema.ts but only includes the fields the
// daemon actually populates today. Additional fields live in the TS schema
// (filePaths, linesAdded/Removed, etc.); they are absent here intentionally.
//
// JSON shape is camelCase to match the TS schema exactly.
type FlowstateEvent struct {
	// Source is always "flowstate-proxy-daemon" for events emitted here.
	Source string `json:"source"`
	// SessionID is a daemon-assigned identifier for grouping events from a
	// single CONNECT-MITM session. The classifier uses this to roll
	// individual API requests into a session.
	SessionID string `json:"sessionId"`
	// EventType is one of the values in FlowstateEventSchema.eventType. The
	// daemon emits "api_request" for every captured upstream call.
	EventType string `json:"eventType"`
	// Timestamp is ISO 8601 / RFC3339Nano UTC.
	Timestamp string `json:"timestamp"`
	// Provider is the canonical provider name ("anthropic", "openai", etc.)
	// derived from the request's upstream host.
	Provider string `json:"provider"`
	// Model is the provider model identifier extracted from the request.
	// Nullable on the wire — empty string serialises as "" which downstream
	// treats as null.
	Model string `json:"model,omitempty"`
	// RequestSummary is a redacted view of the upstream request — enough for
	// the classifier to grade it, never enough to recover provider keys or
	// raw secrets. Free-form JSON object; consult ai-telemetry for the
	// canonical shape.
	RequestSummary map[string]any `json:"requestSummary,omitempty"`
	// ResponseSummary is the analogous view of the upstream response.
	ResponseSummary map[string]any `json:"responseSummary,omitempty"`
	// CostUsd is the parsed cost of the request in USD. Zero when unknown.
	CostUsd float64 `json:"costUsd,omitempty"`
	// DurationMs is the wall-clock duration of the upstream call.
	DurationMs int64 `json:"durationMs,omitempty"`
}

// telemetryPath is the ingest endpoint suffix on the Flowstate hosted
// backend. The full URL is <endpoint>/api/v1/ai-telemetry/ingest.
const telemetryPath = "/api/v1/ai-telemetry/ingest"

// keyHeader is the header carrying the customer's telemetry key. Matches the
// existing pattern from scripts/hooks/flowstate-cursor.sh.
const keyHeader = "x-flowstate-key"

// httpDoer is the minimal interface the forwarder needs from an HTTP client.
// Tests inject a doer that records requests; production uses *http.Client.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Forwarder ships captured events to Flowstate's hosted telemetry endpoint.
// It is intentionally fire-and-forget at the top: the daemon's MITM hot path
// must never block on an upload, so callers should run Send in a goroutine
// (or a small worker pool — TODO once load justifies it).
type Forwarder struct {
	endpoint string
	key      string
	client   httpDoer
}

// ForwarderConfig holds construction parameters.
type ForwarderConfig struct {
	// Endpoint is the Flowstate hosted base URL (no trailing slash needed).
	Endpoint string
	// Key is the telemetry API key authenticating the org.
	Key string
	// Client is optional. If nil, a sensible default with a short timeout is
	// used. Tests pass an *httptest.Server-backed client.
	Client httpDoer
}

// NewForwarder constructs a Forwarder. Endpoint must be non-empty; key may
// be empty for dev/dry-run setups but production callers are expected to
// supply one.
func NewForwarder(cfg ForwarderConfig) (*Forwarder, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("forwarder: endpoint must not be empty")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Forwarder{
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
		key:      cfg.Key,
		client:   client,
	}, nil
}

// Send POSTs the event to <endpoint>/api/v1/ai-telemetry/ingest with the
// telemetry key in the x-flowstate-key header. Returns an error if the
// upstream responded with a non-2xx — callers in the hot path should log and
// drop, not retry inline.
func (f *Forwarder) Send(ctx context.Context, evt FlowstateEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("forwarder: marshal event: %w", err)
	}

	url := f.endpoint + telemetryPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("forwarder: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if f.key != "" {
		req.Header.Set(keyHeader, f.key)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("forwarder: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("forwarder: upstream returned %d", resp.StatusCode)
	}
	return nil
}

// Endpoint returns the configured base endpoint. Used by status output.
func (f *Forwarder) Endpoint() string { return f.endpoint }
