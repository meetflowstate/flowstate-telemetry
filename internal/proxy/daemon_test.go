package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNew_RequiredFields exercises the construction guards.
func TestNew_RequiredFields(t *testing.T) {
	dir := t.TempDir()
	ca, key, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	allow := NewAllowlist(SeedAllowlist)

	if _, err := New(DaemonConfig{Allowlist: allow}); err == nil {
		t.Fatal("expected error without CA")
	}
	if _, err := New(DaemonConfig{CA: ca, CAKey: key}); err == nil {
		t.Fatal("expected error without allowlist")
	}
	d, err := New(DaemonConfig{CA: ca, CAKey: key, Allowlist: allow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.cfg.Port != DefaultPort {
		t.Errorf("expected default port %d, got %d", DefaultPort, d.cfg.Port)
	}
}

// TestProviderForHost spot-checks the host → provider mapping that lands on
// the wire.
func TestProviderForHost(t *testing.T) {
	cases := map[string]string{
		"api.anthropic.com":                      "anthropic",
		"api.openai.com":                         "openai",
		"generativelanguage.googleapis.com":      "gemini",
		"api.githubcopilot.com":                  "copilot",
		"proxy.individual.githubcopilot.com":     "copilot",
		"api.cursor.sh":                          "cursor",
		"api2.cursor.sh":                         "cursor",
		"server.codeium.com":                     "codeium",
		"inference.codeium.com":                  "codeium",
		"github.com":                             "",
		"API.ANTHROPIC.COM":                      "anthropic",
		"api.anthropic.com:443":                  "anthropic",
	}
	for in, want := range cases {
		if got := providerForHost(in); got != want {
			t.Errorf("providerForHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStripPort exercises the helper used by tlsConfigForHost.
func TestStripPort(t *testing.T) {
	cases := map[string]string{
		"api.anthropic.com:443": "api.anthropic.com",
		"api.anthropic.com":     "api.anthropic.com",
		"127.0.0.1:8080":        "127.0.0.1",
	}
	for in, want := range cases {
		if got := stripPort(in); got != want {
			t.Errorf("stripPort(%q) = %q, want %q", in, got, want)
		}
	}
}

// captureEmitter records every event sent to the forwarder.
type captureEmitter struct {
	mu     sync.Mutex
	events []FlowstateEvent
	wg     sync.WaitGroup
}

func (c *captureEmitter) Send(_ context.Context, evt FlowstateEvent) error {
	c.mu.Lock()
	c.events = append(c.events, evt)
	c.mu.Unlock()
	c.wg.Done()
	return nil
}

func (c *captureEmitter) Expect(n int) {
	c.wg.Add(n)
}

func (c *captureEmitter) Wait(timeout time.Duration) error {
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.New("timeout waiting for events")
	}
}

func (c *captureEmitter) Snapshot() []FlowstateEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]FlowstateEvent, len(c.events))
	copy(out, c.events)
	return out
}

// startDaemon brings up a daemon on an OS-assigned free port for tests. It
// returns the proxy URL and a cancel func that gracefully stops the daemon.
func startDaemon(t *testing.T, allowlist *Allowlist, emitter EventEmitter) (proxyURL *url.URL, ca *x509.Certificate, cancel func()) {
	t.Helper()
	dir := t.TempDir()
	caCert, caKey, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}

	// Pick a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	d, err := New(DaemonConfig{
		Port:      port,
		CA:        caCert,
		CAKey:     caKey,
		Allowlist: allowlist,
		Emitter:   emitter,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancelFn := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(ctx) }()

	// Wait until the listener is up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	pURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	cleanup := func() {
		cancelFn()
		select {
		case <-doneCh:
		case <-time.After(3 * time.Second):
			t.Error("daemon did not shut down within 3s")
		}
	}
	return pURL, caCert, cleanup
}

// TestDaemon_TunnelsNonAIHosts confirms that traffic destined for a host NOT
// on the allowlist is tunneled raw — the upstream cert reaches the client
// unchanged, which means the client's TLS chain verification sees the
// original CA, not our local CA.
func TestDaemon_TunnelsNonAIHosts(t *testing.T) {
	// Spin up a real TLS server with a self-signed cert that is NOT signed
	// by our local CA. After tunneling, the client should see that
	// upstream cert when it inspects the connection.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamHost := upstreamURL.Hostname()
	upstreamPort := upstreamURL.Port()

	// Empty allowlist → everything tunnels.
	allow := NewAllowlist(nil)
	emitter := &captureEmitter{}
	proxyURL, _, cancel := startDaemon(t, allow, emitter)
	t.Cleanup(cancel)

	// Build a client that trusts the upstream's self-signed cert (so the
	// test can assert the tunneled bytes are end-to-end TLS to upstream).
	upstreamPool := x509.NewCertPool()
	upstreamPool.AddCert(upstream.Certificate())

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs:    upstreamPool,
				ServerName: upstreamHost,
			},
		},
		Timeout: 5 * time.Second,
	}

	target := fmt.Sprintf("https://%s:%s/", upstreamHost, upstreamPort)
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET via tunnel: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upstream-ok" {
		t.Fatalf("body = %q, want upstream-ok", body)
	}
	// Crucially: no event captured for tunneled traffic.
	if len(emitter.Snapshot()) != 0 {
		t.Fatalf("expected zero events for tunneled host, got %v", emitter.Snapshot())
	}
}

// TestDaemon_InterceptsAIHosts confirms that traffic to an allowlisted host is
// MITM'd: the client sees a cert signed by our local CA (not the upstream's
// real cert), the request reaches upstream successfully, and a FlowstateEvent
// is emitted with the right metadata.
func TestDaemon_InterceptsAIHosts(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"path":%q}`, r.URL.Path)
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamHostPort := upstreamURL.Host

	// Allowlist the upstream's hostname (httptest uses 127.0.0.1).
	allow := NewAllowlist([]string{upstreamURL.Hostname()})
	emitter := &captureEmitter{}
	emitter.Expect(1)

	proxyURL, ca, cancel := startDaemon(t, allow, emitter)
	t.Cleanup(cancel)

	// Trust BOTH our local CA (so MITM verifies) AND the upstream's cert
	// (so the daemon's onward connection verifies).
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	pool.AddCert(upstream.Certificate())

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				ServerName: upstreamURL.Hostname(),
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("https://%s/v1/messages", upstreamHostPort))
	if err != nil {
		t.Fatalf("GET via intercept: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("unexpected body: %s", body)
	}

	// Verify the cert presented to the client was signed by OUR CA (i.e.
	// MITM happened).
	state := resp.TLS
	if state == nil || len(state.PeerCertificates) == 0 {
		t.Fatal("expected TLS peer cert info")
	}
	peer := state.PeerCertificates[0]
	if err := peer.CheckSignatureFrom(ca); err != nil {
		t.Errorf("expected leaf to be signed by local CA, got: %v", err)
	}

	// Wait for the async forwarder send.
	if err := emitter.Wait(2 * time.Second); err != nil {
		t.Fatalf("waiting for event: %v", err)
	}
	events := emitter.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.EventType != "api_request" {
		t.Errorf("eventType = %s, want api_request", evt.EventType)
	}
	if evt.Source != "flowstate-proxy-daemon" {
		t.Errorf("source = %s, want flowstate-proxy-daemon", evt.Source)
	}
	if evt.RequestSummary["path"] != "/v1/messages" {
		t.Errorf("requestSummary.path = %v, want /v1/messages", evt.RequestSummary["path"])
	}
	if evt.RequestSummary["method"] != "GET" {
		t.Errorf("requestSummary.method = %v, want GET", evt.RequestSummary["method"])
	}
	if status, _ := evt.ResponseSummary["status"].(int); status != http.StatusOK {
		t.Errorf("responseSummary.status = %v, want 200", evt.ResponseSummary["status"])
	}
	if evt.SessionID == "" {
		t.Error("expected non-empty SessionID")
	}
}

// TestDaemon_ShutdownIsClean ensures cancellation of the run context drains
// in-flight connections promptly (the listener closes within the timeout).
func TestDaemon_ShutdownIsClean(t *testing.T) {
	allow := NewAllowlist(nil)
	_, _, cancel := startDaemon(t, allow, nil)
	cancel() // exits immediately if shutdown is clean
}
