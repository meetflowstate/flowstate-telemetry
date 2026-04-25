package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestForwarder_Send_WireShape spins up an httptest.Server, fires Send, and
// asserts the captured request matches the documented wire format end-to-end:
//   - method = POST
//   - path   = /api/v1/ai-telemetry/ingest
//   - header = x-flowstate-key: <key>
//   - body   = JSON-encoded FlowstateEvent with camelCase keys
func TestForwarder_Send_WireShape(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotHeader string
		gotBody   []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeader = r.Header.Get(keyHeader)
		b, _ := io.ReadAll(r.Body)
		gotBody = b
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	fwd, err := NewForwarder(ForwarderConfig{
		Endpoint: srv.URL,
		Key:      "tk_test_123",
		Client:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}

	evt := FlowstateEvent{
		Source:     "flowstate-proxy-daemon",
		SessionID:  "sess-1",
		EventType:  "api_request",
		Timestamp:  "2026-04-24T10:00:00Z",
		Provider:   "anthropic",
		Model:      "claude-opus-4-5",
		DurationMs: 1234,
		CostUsd:    0.012,
	}
	if err := fwd.Send(context.Background(), evt); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != telemetryPath {
		t.Errorf("path = %s, want %s", gotPath, telemetryPath)
	}
	if gotHeader != "tk_test_123" {
		t.Errorf("x-flowstate-key = %q, want tk_test_123", gotHeader)
	}

	var roundtrip map[string]any
	if err := json.Unmarshal(gotBody, &roundtrip); err != nil {
		t.Fatalf("unmarshal body: %v (raw=%s)", err, gotBody)
	}
	wantKeys := []string{"source", "sessionId", "eventType", "timestamp", "provider", "model", "costUsd", "durationMs"}
	for _, k := range wantKeys {
		if _, ok := roundtrip[k]; !ok {
			t.Errorf("body missing key %q (got %s)", k, gotBody)
		}
	}
}

// TestForwarder_Send_TrailingSlashEndpoint confirms we don't double-slash the
// path when the caller passes an endpoint with a trailing /.
func TestForwarder_Send_TrailingSlashEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	fwd, err := NewForwarder(ForwarderConfig{
		Endpoint: srv.URL + "/",
		Key:      "k",
		Client:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	if err := fwd.Send(context.Background(), FlowstateEvent{EventType: "api_request"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != telemetryPath {
		t.Errorf("path = %s, want %s", gotPath, telemetryPath)
	}
}

// TestForwarder_Send_EmptyKey covers the dev/dry-run path: no header should
// be sent if the key is unset.
func TestForwarder_Send_EmptyKey(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(keyHeader)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	fwd, err := NewForwarder(ForwarderConfig{Endpoint: srv.URL, Client: srv.Client()})
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	if err := fwd.Send(context.Background(), FlowstateEvent{EventType: "api_request"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotHeader != "" {
		t.Errorf("expected no x-flowstate-key when key empty, got %q", gotHeader)
	}
}

// TestForwarder_Send_Non2xx surfaces upstream errors so the caller can log.
func TestForwarder_Send_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	fwd, err := NewForwarder(ForwarderConfig{Endpoint: srv.URL, Client: srv.Client()})
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	err = fwd.Send(context.Background(), FlowstateEvent{EventType: "api_request"})
	if err == nil {
		t.Fatal("expected error on 4xx upstream")
	}
	if !strings.Contains(err.Error(), "418") {
		t.Errorf("error should mention status code: %v", err)
	}
}

// TestForwarder_Send_TransportError covers the case where the network call
// itself fails before any HTTP response is read.
func TestForwarder_Send_TransportError(t *testing.T) {
	doer := &errDoer{err: errors.New("dial timeout")}
	fwd, err := NewForwarder(ForwarderConfig{Endpoint: "http://example.invalid", Client: doer})
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	if err := fwd.Send(context.Background(), FlowstateEvent{}); err == nil {
		t.Fatal("expected transport error")
	}
}

// TestForwarder_New_Validation rejects empty endpoints.
func TestForwarder_New_Validation(t *testing.T) {
	if _, err := NewForwarder(ForwarderConfig{}); err == nil {
		t.Fatal("expected error on empty endpoint")
	}
}

// errDoer is an httpDoer that always errors. Used to drive the transport-
// failure branch.
type errDoer struct {
	mu  sync.Mutex
	err error
}

func (e *errDoer) Do(*http.Request) (*http.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return nil, e.err
}
