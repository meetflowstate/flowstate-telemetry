package proxy

import "testing"

// TestDecide is a table-driven check on the central intercept rule. If this
// table grows or changes, the daemon's MITM surface is changing — review
// carefully.
func TestDecide(t *testing.T) {
	allow := NewAllowlist(SeedAllowlist)

	cases := []struct {
		name string
		host string
		want Decision
	}{
		{"anthropic api → intercept", "api.anthropic.com", Intercept},
		{"openai api → intercept", "api.openai.com", Intercept},
		{"gemini api → intercept", "generativelanguage.googleapis.com", Intercept},
		{"copilot api → intercept", "api.githubcopilot.com", Intercept},
		{"copilot inline ind → intercept", "proxy.individual.githubcopilot.com", Intercept},
		{"cursor api → intercept", "api2.cursor.sh", Intercept},
		{"codeium server → intercept", "server.codeium.com", Intercept},

		{"github.com → tunnel", "github.com", Tunnel},
		{"google.com → tunnel", "google.com", Tunnel},
		{"random.example → tunnel", "example.com", Tunnel},
		{"empty host → tunnel", "", Tunnel},
		{"port-suffixed allowlisted → intercept", "api.anthropic.com:443", Intercept},
		{"port-suffixed non-AI → tunnel", "github.com:443", Tunnel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.host, allow)
			if got != c.want {
				t.Errorf("Decide(%q) = %s, want %s", c.host, got, c.want)
			}
		})
	}
}

// TestDecide_NilAllowlist asserts the fail-closed behaviour. A missing
// allowlist must NOT cause us to MITM traffic.
func TestDecide_NilAllowlist(t *testing.T) {
	if got := Decide("api.anthropic.com", nil); got != Tunnel {
		t.Errorf("nil allowlist: got %s, want tunnel", got)
	}
}

// TestDecision_String covers the trivial stringer for log readability.
func TestDecision_String(t *testing.T) {
	if Intercept.String() != "intercept" {
		t.Fatal("Intercept stringer broken")
	}
	if Tunnel.String() != "tunnel" {
		t.Fatal("Tunnel stringer broken")
	}
}
