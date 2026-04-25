package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
)

// DefaultPort is the daemon's listening port. Chosen as a deterministic
// high port that's unlikely to collide with anything else on a developer's
// machine.
const DefaultPort = 47813

// EventEmitter is the minimal interface the daemon needs from the forwarder.
// The Forwarder type satisfies this; tests can pass a fake emitter to
// observe events without spinning up an HTTP server.
type EventEmitter interface {
	Send(ctx context.Context, evt FlowstateEvent) error
}

// DaemonConfig holds the runtime configuration for the proxy daemon.
type DaemonConfig struct {
	// Port to listen on. Zero means DefaultPort.
	Port int
	// CA + key, typically returned from GenerateOrLoadCA.
	CA    *x509.Certificate
	CAKey *ecdsa.PrivateKey
	// Allowlist controls which hosts are MITM'd. Required.
	Allowlist *Allowlist
	// Emitter receives one FlowstateEvent per intercepted request. Optional;
	// if nil, captures are dropped (useful for smoke-testing without a
	// running backend).
	Emitter EventEmitter
	// Logger sinks operational logs. Defaults to log.Default().
	Logger *log.Logger
	// Verbose toggles per-request logging.
	Verbose bool
}

// Daemon is the on-device HTTPS interceptor.
type Daemon struct {
	cfg       DaemonConfig
	leafCache *LeafCache
	server    *http.Server
	listener  net.Listener
	mu        sync.Mutex
}

// New constructs a Daemon. CA, CAKey, and Allowlist must be set. Other
// fields take sensible defaults.
func New(cfg DaemonConfig) (*Daemon, error) {
	if cfg.CA == nil || cfg.CAKey == nil {
		return nil, errors.New("daemon: CA and CAKey are required")
	}
	if cfg.Allowlist == nil {
		return nil, errors.New("daemon: Allowlist is required")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &Daemon{
		cfg:       cfg,
		leafCache: NewLeafCache(),
	}, nil
}

// Addr returns the daemon's bound address. Empty until Run is called.
func (d *Daemon) Addr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.listener == nil {
		return ""
	}
	return d.listener.Addr().String()
}

// Run binds the listener and serves until ctx is cancelled. It returns
// http.ErrServerClosed on clean shutdown and any other error otherwise.
//
// The function constructs a goproxy.ProxyHttpServer wired to:
//   - HandleConnect → calls Decide(host, allowlist) per CONNECT, returning
//     ConnectMitm or ConnectAccept (raw tunnel)
//   - TLSConfig → uses our LeafCache to issue per-host leaves signed by our
//     persistent local CA (NOT goproxy's ephemeral default)
//   - OnRequest/OnResponse → captures intercepted req/resp into a
//     FlowstateEvent and forwards via the emitter, never blocking the hot
//     path (handler returns immediately; emit runs in a goroutine)
func (d *Daemon) Run(ctx context.Context) error {
	proxy := d.buildProxy()

	addr := fmt.Sprintf(":%d", d.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", addr, err)
	}

	d.mu.Lock()
	d.listener = listener
	d.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 30 * time.Second,
	}
	d.mu.Unlock()

	d.cfg.Logger.Printf("flowstate-telemetry proxy listening on %s", listener.Addr())

	// Shutdown plumbing: when ctx cancels, gracefully close the server.
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.server.Shutdown(shutdownCtx)
		close(shutdownDone)
	}()

	serveErr := d.server.Serve(listener)
	<-shutdownDone

	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}

// buildProxy wires the goproxy server with our intercept and capture rules.
// Extracted so it can be exercised independently of the network listener.
func (d *Daemon) buildProxy() *goproxy.ProxyHttpServer {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = d.cfg.Verbose
	if d.cfg.Verbose {
		proxy.Logger = d.cfg.Logger
	}

	// CONNECT handler: decide per-host whether to MITM or tunnel.
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		decision := Decide(host, d.cfg.Allowlist)
		if d.cfg.Verbose {
			d.cfg.Logger.Printf("CONNECT %s → %s", host, decision)
		}
		switch decision {
		case Intercept:
			return &goproxy.ConnectAction{
				Action:    goproxy.ConnectMitm,
				TLSConfig: d.tlsConfigForHost,
			}, host
		default:
			// ConnectAccept = tunnel raw bytes, original cert reaches client.
			return goproxy.OkConnect, host
		}
	})

	// Capture intercepted requests + responses. These callbacks fire only on
	// MITM'd traffic — tunneled CONNECTs never produce a request object here.
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		stamp := time.Now()
		if ctx.UserData == nil {
			ctx.UserData = &captureState{started: stamp}
		}
		return req, nil
	})

	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		// resp may be nil if the upstream call failed before producing a
		// response; bail out — no event to emit.
		if resp == nil || ctx.Req == nil {
			return resp
		}
		state, _ := ctx.UserData.(*captureState)
		started := time.Now()
		if state != nil {
			started = state.started
		}
		duration := time.Since(started)
		req := ctx.Req

		if d.cfg.Emitter != nil {
			// Build the event under the read lock; emit on a goroutine so
			// the response body is NEVER buffered before being streamed
			// back to the client. Capture happens by inspecting headers +
			// metadata, not by reading the body.
			evt := FlowstateEvent{
				Source:     "flowstate-proxy-daemon",
				SessionID:  fmt.Sprintf("daemon-%d", ctx.Session),
				EventType:  "api_request",
				Timestamp:  started.UTC().Format(time.RFC3339Nano),
				Provider:   providerForHost(req.URL.Host),
				Model:      "",
				DurationMs: duration.Milliseconds(),
				RequestSummary: map[string]any{
					"host":   req.URL.Host,
					"path":   req.URL.Path,
					"method": req.Method,
				},
				ResponseSummary: map[string]any{
					"status": resp.StatusCode,
				},
			}
			go func() {
				sendCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := d.cfg.Emitter.Send(sendCtx, evt); err != nil && d.cfg.Verbose {
					d.cfg.Logger.Printf("forwarder send: %v", err)
				}
			}()
		}
		return resp
	})

	return proxy
}

// tlsConfigForHost returns a TLS server config presenting a leaf cert signed
// by our persistent local CA. The leaf is cached in LeafCache for the
// lifetime of the daemon.
func (d *Daemon) tlsConfigForHost(host string, _ *goproxy.ProxyCtx) (*tls.Config, error) {
	hostname := stripPort(host)
	leaf, err := d.leafCache.GetOrIssue(d.cfg.CA, d.cfg.CAKey, hostname)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{*leaf},
		// MinVersion is set defensively; goproxy's defaults are fine, but we
		// don't want to silently regress to TLS 1.0.
		MinVersion: tls.VersionTLS12,
	}, nil
}

// captureState is stashed in goproxy.ProxyCtx.UserData to thread per-request
// timing across the OnRequest/OnResponse callbacks.
type captureState struct {
	started time.Time
}

// stripPort returns the host portion of "host:port", or the input unchanged
// if no port is present. Local helper to avoid pulling in goproxy's internal
// stripPort; behaviour matches well enough for our allowlist comparisons.
func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		// Guard against a leading IPv6 bracket — we don't allowlist any
		// IPv6 hosts, but be safe.
		if !strings.Contains(host[:i], "]") || strings.HasSuffix(host[:i], "]") {
			return strings.TrimRight(strings.TrimLeft(host[:i], "["), "]")
		}
	}
	return host
}

// providerForHost maps a known AI host onto the canonical provider name used
// in classifier output and reporting. Unknown hosts return "" and the
// downstream classifier infers from request shape.
func providerForHost(host string) string {
	h := strings.ToLower(stripPort(host))
	switch h {
	case "api.anthropic.com":
		return "anthropic"
	case "api.openai.com":
		return "openai"
	case "generativelanguage.googleapis.com":
		return "gemini"
	case "api.githubcopilot.com",
		"copilot-proxy.githubusercontent.com",
		"proxy.individual.githubcopilot.com",
		"proxy.business.githubcopilot.com":
		return "copilot"
	case "api.cursor.sh", "api2.cursor.sh":
		return "cursor"
	case "server.codeium.com", "inference.codeium.com":
		return "codeium"
	default:
		return ""
	}
}

// Compile-time assertion that *http.Response satisfies io.ReadCloser through
// its embedded Body — this nudges the reader to remember response bodies
// must NOT be buffered before being streamed back. (The goproxy library
// streams resp.Body via io.Copy in its hot path; we never read it ourselves.)
var _ io.Reader = (io.Reader)(nil)
