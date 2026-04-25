package proxy

import (
	"strings"
	"testing"
)

// TestGeneratePAC_ContainsHostsAndProxy is a string-level sanity check on the
// generated PAC. We don't run a JS engine — instead we assert the structural
// markers a PAC engine relies on.
func TestGeneratePAC_ContainsHostsAndProxy(t *testing.T) {
	allow := NewAllowlist([]string{"api.anthropic.com", "api.openai.com"})
	pac := GeneratePAC("127.0.0.1:47813", allow)

	for _, want := range []string{
		"function FindProxyForURL(url, host)",
		"\"api.anthropic.com\"",
		"\"api.openai.com\"",
		"\"PROXY 127.0.0.1:47813\"",
		"\"DIRECT\"",
	} {
		if !strings.Contains(pac, want) {
			t.Errorf("PAC missing %q\n--PAC--\n%s", want, pac)
		}
	}
}

// TestGeneratePAC_FallthroughDirect verifies the literal fallthrough is
// `return "DIRECT"`. If this string moves the safety guarantee changes —
// the test catches it.
func TestGeneratePAC_FallthroughDirect(t *testing.T) {
	allow := NewAllowlist([]string{"api.anthropic.com"})
	pac := GeneratePAC("127.0.0.1:47813", allow)
	// Should appear after the loop closer and be the final return.
	if !strings.Contains(pac, "}\n  return \"DIRECT\";\n}") {
		t.Errorf("expected DIRECT fallthrough at end of FindProxyForURL\n%s", pac)
	}
}

// TestGeneratePAC_Determinism asserts identical inputs produce byte-identical
// output. This matters for OS-level PAC caching and for diffing.
func TestGeneratePAC_Determinism(t *testing.T) {
	a := NewAllowlist([]string{"b.com", "a.com", "c.com"})
	pac1 := GeneratePAC("127.0.0.1:47813", a)
	pac2 := GeneratePAC("127.0.0.1:47813", a)
	if pac1 != pac2 {
		t.Fatal("expected identical PAC across calls")
	}

	// Permutations of the input should still yield the same PAC because
	// hosts are deduped + sorted before emission.
	b := NewAllowlist([]string{"c.com", "a.com", "b.com", "a.com"})
	pacB := GeneratePAC("127.0.0.1:47813", b)
	if pacB != pac1 {
		t.Fatal("expected dedupe+sort to make PAC stable across input permutations")
	}
}

// TestGeneratePAC_NilAllowlist returns a valid PAC that always goes DIRECT.
// Useful for the "allowlist not yet loaded" startup window.
func TestGeneratePAC_NilAllowlist(t *testing.T) {
	pac := GeneratePAC("127.0.0.1:47813", nil)
	if !strings.Contains(pac, "function FindProxyForURL(url, host)") {
		t.Fatal("expected valid PAC even with nil allowlist")
	}
	// No host array entries.
	if strings.Contains(pac, "\"api.anthropic.com\"") {
		t.Fatal("nil allowlist must not include any AI hosts")
	}
}

// TestGeneratePAC_HostMatchSemantics confirms the emitted JS uses an exact
// hostname match (===) rather than substring/dnsDomainIs — important so
// "anthropic.com" doesn't accidentally route random subdomains.
func TestGeneratePAC_HostMatchSemantics(t *testing.T) {
	allow := NewAllowlist([]string{"api.anthropic.com"})
	pac := GeneratePAC("127.0.0.1:47813", allow)
	if !strings.Contains(pac, "host === aiHosts[i]") {
		t.Fatalf("expected exact-match comparison in PAC; got\n%s", pac)
	}
}
