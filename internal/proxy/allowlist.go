package proxy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// SeedAllowlist is the static set of hostnames the daemon MITMs. Hosts not on
// this list (or in subsequent hot-updated additions) are tunneled raw via
// CONNECT and never decrypted.
//
// The list is intentionally narrow: it is the set of canonical AI-provider
// hostnames the classifier and cost parser know how to handle. Adding a host
// here means we know the request shape, the streaming format, and the
// usage-accounting fields. Anything else passes through untouched.
var SeedAllowlist = []string{
	"api.anthropic.com",
	"api.openai.com",
	"generativelanguage.googleapis.com",
	"api.githubcopilot.com",
	"copilot-proxy.githubusercontent.com",
	"proxy.individual.githubcopilot.com",
	"proxy.business.githubcopilot.com",
	"api.cursor.sh",
	"api2.cursor.sh",
	"server.codeium.com",
	"inference.codeium.com",
}

// AllowlistFetcher returns the latest allowlist from a remote source. The
// daemon's Refresh path uses one of these to pull updates from
// flowstate's /api/v1/ai-telemetry/proxy-allowlist endpoint.
type AllowlistFetcher interface {
	Fetch(ctx context.Context) ([]string, error)
}

// Allowlist is the runtime hostname allowlist. Reads (ShouldIntercept) are
// concurrent-safe; the only writer is Refresh.
type Allowlist struct {
	mu       sync.RWMutex
	hosts    map[string]struct{}
	updated  time.Time
}

// NewAllowlist builds an Allowlist seeded from the supplied hosts. Hostnames
// are stored lowercase. Pass SeedAllowlist for the production default.
func NewAllowlist(seed []string) *Allowlist {
	a := &Allowlist{hosts: make(map[string]struct{}, len(seed))}
	a.replace(seed)
	return a
}

// ShouldIntercept reports whether the daemon should MITM CONNECTs targeting
// the given host. The host argument may include a port suffix (":443"); it is
// stripped before lookup. Comparison is case-insensitive.
//
// Match is exact on the registered hostname. Subdomains are NOT implicitly
// included — to add a subdomain, register it explicitly. This keeps the
// MITM surface auditable.
func (a *Allowlist) ShouldIntercept(host string) bool {
	if host == "" {
		return false
	}
	h := strings.ToLower(host)
	if i := strings.LastIndex(h, ":"); i >= 0 {
		// Drop port suffix ("api.anthropic.com:443" -> "api.anthropic.com").
		// Note: lastIndex handles edge cases with IPv6 brackets sufficiently
		// for our use (IPv6 hostnames are not on the allowlist anyway).
		h = h[:i]
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.hosts[h]
	return ok
}

// Hosts returns a snapshot of the current allowlist, sorted lexically.
func (a *Allowlist) Hosts() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.hosts))
	for h := range a.hosts {
		out = append(out, h)
	}
	// Stable order makes this useful for diffing in tests + status output.
	sortStrings(out)
	return out
}

// LastUpdated returns the timestamp of the last successful Refresh, or the
// zero value if the allowlist has only ever been seeded.
func (a *Allowlist) LastUpdated() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.updated
}

// Refresh pulls the latest hosts from fetcher and atomically replaces the
// in-memory set. Fetcher errors leave the existing set in place — refresh
// failures must not silently shrink the allowlist.
//
// TODO(workstream-A): wire this up to a periodic ticker in the daemon once
// the flowstate /api/v1/ai-telemetry/proxy-allowlist endpoint ships. For now
// this exists so the daemon can call it when explicitly told to and so tests
// can exercise the replacement path.
func (a *Allowlist) Refresh(ctx context.Context, fetcher AllowlistFetcher) error {
	if fetcher == nil {
		return errors.New("allowlist: nil fetcher")
	}
	hosts, err := fetcher.Fetch(ctx)
	if err != nil {
		return err
	}
	a.replace(hosts)
	a.mu.Lock()
	a.updated = time.Now()
	a.mu.Unlock()
	return nil
}

// replace swaps the underlying set in a single critical section.
func (a *Allowlist) replace(hosts []string) {
	next := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		next[h] = struct{}{}
	}
	a.mu.Lock()
	a.hosts = next
	a.mu.Unlock()
}

// sortStrings is a tiny dependency-free sort to avoid pulling in sort just
// for the snapshot helper. Insertion sort is fine for our list sizes (~tens).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
