package proxy

import (
	"context"
	"errors"
	"testing"
)

// TestAllowlist_ShouldIntercept walks the seed list against expected
// decisions, including case-folding, port stripping, and explicit non-AI
// hosts that must NOT be intercepted.
func TestAllowlist_ShouldIntercept(t *testing.T) {
	a := NewAllowlist(SeedAllowlist)

	cases := []struct {
		host string
		want bool
	}{
		{"api.anthropic.com", true},
		{"API.ANTHROPIC.COM", true},
		{"api.anthropic.com:443", true},
		{"api.openai.com", true},
		{"api.openai.com:8443", true},
		{"generativelanguage.googleapis.com", true},
		{"api.githubcopilot.com", true},
		{"copilot-proxy.githubusercontent.com", true},
		{"api.cursor.sh", true},
		{"api2.cursor.sh", true},
		{"server.codeium.com", true},
		{"inference.codeium.com", true},

		// Explicit non-AI hosts that MUST tunnel raw.
		{"github.com", false},
		{"google.com", false},
		{"www.anthropic.com", false},               // marketing site, not API
		{"unknown.subdomain.anthropic.com", false}, // subdomains are NOT inherited
		{"localhost", false},
		{"", false},
	}

	for _, c := range cases {
		got := a.ShouldIntercept(c.host)
		if got != c.want {
			t.Errorf("ShouldIntercept(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestAllowlist_Hosts verifies snapshot stability and sortedness.
func TestAllowlist_Hosts(t *testing.T) {
	a := NewAllowlist([]string{"b.com", "a.com", "C.com"})
	got := a.Hosts()
	want := []string{"a.com", "b.com", "c.com"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Hosts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

type fakeFetcher struct {
	hosts []string
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(ctx context.Context) ([]string, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.hosts, nil
}

// TestAllowlist_Refresh_Success replaces the set and stamps LastUpdated.
func TestAllowlist_Refresh_Success(t *testing.T) {
	a := NewAllowlist([]string{"old.com"})
	if !a.LastUpdated().IsZero() {
		t.Fatal("expected zero LastUpdated before any refresh")
	}

	f := &fakeFetcher{hosts: []string{"new.com", "api.anthropic.com"}}
	if err := a.Refresh(context.Background(), f); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if a.ShouldIntercept("old.com") {
		t.Error("old.com should be gone after refresh")
	}
	if !a.ShouldIntercept("new.com") {
		t.Error("new.com should be present after refresh")
	}
	if !a.ShouldIntercept("api.anthropic.com") {
		t.Error("api.anthropic.com should be present after refresh")
	}
	if a.LastUpdated().IsZero() {
		t.Error("LastUpdated should be stamped after refresh")
	}
}

// TestAllowlist_Refresh_PreservesOnError keeps the existing list when the
// fetcher fails. A transient outage must NOT silently disable interception.
func TestAllowlist_Refresh_PreservesOnError(t *testing.T) {
	a := NewAllowlist([]string{"keep.com"})
	f := &fakeFetcher{err: errors.New("network down")}

	err := a.Refresh(context.Background(), f)
	if err == nil {
		t.Fatal("expected error from refresh")
	}
	if !a.ShouldIntercept("keep.com") {
		t.Error("existing host must survive a fetch failure")
	}
}

// TestAllowlist_Refresh_NilFetcher is a small guard against an obvious caller
// mistake (and exercises the only remaining branch).
func TestAllowlist_Refresh_NilFetcher(t *testing.T) {
	a := NewAllowlist(nil)
	if err := a.Refresh(context.Background(), nil); err == nil {
		t.Fatal("expected error with nil fetcher")
	}
}
