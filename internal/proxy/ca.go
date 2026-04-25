// Package proxy implements the on-device HTTPS interceptor daemon.
//
// The daemon binds on a high local port, exposes itself via PAC, and selectively
// MITMs traffic destined for known AI provider hosts. Non-AI hosts are tunneled
// raw via CONNECT, preserving the upstream certificate chain end-to-end. AI
// hosts are decrypted using a per-machine self-signed CA, observed for telemetry,
// and then re-encrypted with on-the-fly leaf certs back to the client.
package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// caRootValidity is the lifetime of the per-machine root CA.
	caRootValidity = 10 * 365 * 24 * time.Hour
	// caLeafValidity is the lifetime of an on-the-fly leaf certificate.
	caLeafValidity = 365 * 24 * time.Hour
	// caCertFilename is the on-disk filename for the PEM-encoded root cert.
	caCertFilename = "ca.crt"
	// caKeyFilename is the on-disk filename for the PEM-encoded root key.
	caKeyFilename = "ca.key"
)

// GenerateOrLoadCA returns the persistent per-machine root CA, generating one
// on first call. Subsequent calls load the existing files at <dir>/ca.crt
// (0644) and <dir>/ca.key (0600).
//
// The root is an ECDSA P-256 keypair, valid for 10 years, with the subject
// CN "Flowstate Telemetry Local CA — <hostname>".
func GenerateOrLoadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if dir == "" {
		return nil, nil, errors.New("ca: directory must not be empty")
	}

	certPath := filepath.Join(dir, caCertFilename)
	keyPath := filepath.Join(dir, caKeyFilename)

	if cert, key, err := loadCA(certPath, keyPath); err == nil {
		return cert, key, nil
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("ca: load existing: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("ca: mkdir %s: %w", dir, err)
	}

	cert, key, err := generateCA()
	if err != nil {
		return nil, nil, fmt.Errorf("ca: generate: %w", err)
	}

	if err := persistCA(certPath, keyPath, cert, key); err != nil {
		return nil, nil, fmt.Errorf("ca: persist: %w", err)
	}

	return cert, key, nil
}

// IssueLeafCert signs a leaf certificate for the given host using the supplied
// CA. The returned tls.Certificate carries both the parsed leaf and the
// matching private key, suitable for direct use as a TLS server certificate.
//
// Leaf certs are valid for 1 year. SANs include the host as both DNS name and,
// where parseable, IP address. The leaf uses its own ECDSA P-256 keypair so
// the CA private key is never shipped over the wire.
func IssueLeafCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, host string) (*tls.Certificate, error) {
	if ca == nil || caKey == nil {
		return nil, errors.New("ca: ca cert and key must be provided")
	}
	if host == "" {
		return nil, errors.New("ca: host must not be empty")
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca: leaf keygen: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("ca: leaf serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"Flowstate Telemetry Local"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(caLeafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("ca: create leaf: %w", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("ca: parse leaf: %w", err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{der, ca.Raw},
		PrivateKey:  leafKey,
		Leaf:        leaf,
	}, nil
}

// CertFingerprintSHA256 returns the lowercase hex SHA-256 fingerprint of the
// supplied certificate's DER bytes. Used by status/verify to confirm the same
// CA is installed in the OS trust store.
func CertFingerprintSHA256(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// LeafCache is an in-memory cache of issued leaf certificates keyed by host.
// It is safe for concurrent use and intended to live for the daemon's
// lifetime — leaf certs are cheap to issue but cheaper to reuse, especially
// under sustained load against the same provider.
type LeafCache struct {
	mu    sync.RWMutex
	certs map[string]*tls.Certificate
}

// NewLeafCache returns an empty cache.
func NewLeafCache() *LeafCache {
	return &LeafCache{certs: make(map[string]*tls.Certificate)}
}

// GetOrIssue returns a cached leaf for host or issues a new one and caches it.
// Issuance failures are not cached; callers retry naturally on the next
// CONNECT.
func (c *LeafCache) GetOrIssue(ca *x509.Certificate, caKey *ecdsa.PrivateKey, host string) (*tls.Certificate, error) {
	c.mu.RLock()
	if cert, ok := c.certs[host]; ok {
		c.mu.RUnlock()
		return cert, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check after acquiring write lock.
	if cert, ok := c.certs[host]; ok {
		return cert, nil
	}

	cert, err := IssueLeafCert(ca, caKey, host)
	if err != nil {
		return nil, err
	}
	c.certs[host] = cert
	return cert, nil
}

// generateCA produces a fresh ECDSA P-256 root certificate.
func generateCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("keygen: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("Flowstate Telemetry Local CA — %s", hostname),
			Organization: []string{"Flowstate Telemetry"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(caRootValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create cert: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse cert: %w", err)
	}
	return cert, key, nil
}

// loadCA reads and parses the PEM-encoded cert and key on disk.
func loadCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, errors.New("ca: invalid certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, errors.New("ca: invalid key PEM")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		// Some older installs may have written EC SEC1; fall through.
		ecKey, ecErr := x509.ParseECPrivateKey(keyBlock.Bytes)
		if ecErr != nil {
			return nil, nil, fmt.Errorf("parse key: %w", err)
		}
		return cert, ecKey, nil
	}

	key, ok := parsedKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("ca: key is not ECDSA")
	}
	return cert, key, nil
}

// persistCA writes the cert (0644) and key (0600) PEM files atomically.
func persistCA(certPath, keyPath string, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := writeFileAtomic(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file + rename, applying the
// requested mode. Used to avoid leaving a half-written CA on disk.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ca-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
