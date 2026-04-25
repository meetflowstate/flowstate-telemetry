package proxy

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGenerateOrLoadCA_Idempotent asserts that calling GenerateOrLoadCA twice
// against the same directory returns the byte-identical certificate (no
// regeneration on the second call).
func TestGenerateOrLoadCA_Idempotent(t *testing.T) {
	dir := t.TempDir()

	cert1, key1, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("first GenerateOrLoadCA: %v", err)
	}
	cert2, key2, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("second GenerateOrLoadCA: %v", err)
	}

	if CertFingerprintSHA256(cert1) != CertFingerprintSHA256(cert2) {
		t.Fatalf("expected identical fingerprint across loads, got %s vs %s",
			CertFingerprintSHA256(cert1), CertFingerprintSHA256(cert2))
	}
	if !key1.PublicKey.Equal(&key2.PublicKey) {
		t.Fatalf("expected identical public keys across loads")
	}
}

// TestGenerateOrLoadCA_Permissions asserts the on-disk perms match the spec
// (0600 for the key, 0644 for the cert).
func TestGenerateOrLoadCA_Permissions(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := GenerateOrLoadCA(dir); err != nil {
		t.Fatalf("generate: %v", err)
	}

	keyInfo, err := os.Stat(filepath.Join(dir, caKeyFilename))
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("expected key 0600, got %o", keyInfo.Mode().Perm())
	}

	certInfo, err := os.Stat(filepath.Join(dir, caCertFilename))
	if err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	if certInfo.Mode().Perm() != 0o644 {
		t.Fatalf("expected cert 0644, got %o", certInfo.Mode().Perm())
	}
}

// TestGenerateOrLoadCA_SubjectAndExpiry asserts the root has the expected CN
// shape and ~10y validity.
func TestGenerateOrLoadCA_SubjectAndExpiry(t *testing.T) {
	dir := t.TempDir()

	cert, _, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(cert.Subject.CommonName, "Flowstate Telemetry Local CA — ") {
		t.Fatalf("unexpected CN: %s", cert.Subject.CommonName)
	}
	if !cert.IsCA {
		t.Fatal("expected IsCA=true")
	}
	validity := cert.NotAfter.Sub(cert.NotBefore)
	// Allow a comfortable window around the 10-year target.
	if validity < 9*365*24*time.Hour || validity > 11*365*24*time.Hour {
		t.Fatalf("unexpected validity: %s", validity)
	}
}

// TestIssueLeafCert verifies the CA actually signs the leaf and that SANs land
// in the right slot for hostnames vs IPs.
func TestIssueLeafCert(t *testing.T) {
	dir := t.TempDir()
	ca, key, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("generate ca: %v", err)
	}

	leaf, err := IssueLeafCert(ca, key, "api.anthropic.com")
	if err != nil {
		t.Fatalf("issue leaf: %v", err)
	}
	if leaf.Leaf == nil {
		t.Fatal("expected parsed leaf")
	}
	if leaf.Leaf.Subject.CommonName != "api.anthropic.com" {
		t.Fatalf("unexpected leaf CN: %s", leaf.Leaf.Subject.CommonName)
	}
	if len(leaf.Leaf.DNSNames) != 1 || leaf.Leaf.DNSNames[0] != "api.anthropic.com" {
		t.Fatalf("expected DNS SAN api.anthropic.com, got %v", leaf.Leaf.DNSNames)
	}
	if len(leaf.Leaf.IPAddresses) != 0 {
		t.Fatalf("did not expect IP SANs, got %v", leaf.Leaf.IPAddresses)
	}

	// Verify the chain.
	if err := leaf.Leaf.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("leaf not signed by CA: %v", err)
	}

	// IP SAN form.
	ipLeaf, err := IssueLeafCert(ca, key, "127.0.0.1")
	if err != nil {
		t.Fatalf("issue ip leaf: %v", err)
	}
	if len(ipLeaf.Leaf.IPAddresses) != 1 {
		t.Fatalf("expected IP SAN, got %v", ipLeaf.Leaf.IPAddresses)
	}
	if len(ipLeaf.Leaf.DNSNames) != 0 {
		t.Fatalf("did not expect DNS SAN for IP host, got %v", ipLeaf.Leaf.DNSNames)
	}
}

// TestIssueLeafCert_Validity asserts the leaf has roughly a 1-year window.
func TestIssueLeafCert_Validity(t *testing.T) {
	dir := t.TempDir()
	ca, key, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("generate ca: %v", err)
	}
	leaf, err := IssueLeafCert(ca, key, "example.com")
	if err != nil {
		t.Fatalf("issue leaf: %v", err)
	}
	v := leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore)
	if v < 360*24*time.Hour || v > 370*24*time.Hour {
		t.Fatalf("unexpected leaf validity: %s", v)
	}
}

// TestLeafCache_Reuse asserts the same *tls.Certificate pointer is returned on
// repeat lookups for the same host (no re-issuance).
func TestLeafCache_Reuse(t *testing.T) {
	dir := t.TempDir()
	ca, key, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("generate ca: %v", err)
	}
	cache := NewLeafCache()

	c1, err := cache.GetOrIssue(ca, key, "api.openai.com")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	c2, err := cache.GetOrIssue(ca, key, "api.openai.com")
	if err != nil {
		t.Fatalf("re-issue: %v", err)
	}
	if c1 != c2 {
		t.Fatal("expected cache to return identical pointer")
	}

	c3, err := cache.GetOrIssue(ca, key, "api.anthropic.com")
	if err != nil {
		t.Fatalf("issue different host: %v", err)
	}
	if c3 == c1 {
		t.Fatal("expected distinct cert for different host")
	}
}

// TestLeafCache_Concurrent stresses the cache with parallel issuance for the
// same host. Only one underlying cert should be persisted; all callers should
// observe it.
func TestLeafCache_Concurrent(t *testing.T) {
	dir := t.TempDir()
	ca, key, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("generate ca: %v", err)
	}
	cache := NewLeafCache()

	var wg sync.WaitGroup
	results := make([]*tls.Certificate, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := cache.GetOrIssue(ca, key, "api.cursor.sh")
			if err != nil {
				t.Errorf("issue: %v", err)
				return
			}
			results[i] = c
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == nil {
		t.Fatal("first result nil")
	}
	for i, r := range results {
		if r != first {
			t.Fatalf("result %d differed from first; cache leaked cert", i)
		}
	}
}

// TestIssueLeafCert_RejectsZeroInputs makes sure the function fails fast with
// helpful errors rather than producing a junk cert.
func TestIssueLeafCert_RejectsZeroInputs(t *testing.T) {
	if _, err := IssueLeafCert(nil, nil, "example.com"); err == nil {
		t.Fatal("expected error with nil ca")
	}
	dir := t.TempDir()
	ca, key, err := GenerateOrLoadCA(dir)
	if err != nil {
		t.Fatalf("generate ca: %v", err)
	}
	if _, err := IssueLeafCert(ca, key, ""); err == nil {
		t.Fatal("expected error with empty host")
	}
}
