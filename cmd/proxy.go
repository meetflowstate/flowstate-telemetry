package cmd

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/platform"
	"github.com/meetflowstate/flowstate-telemetry/internal/proxy"
	"github.com/spf13/cobra"
)

var (
	flagProxyPort    int
	flagProxyDataDir string
)

// defaultDataDir is where the proxy daemon persists its CA + state. Matches
// the layout from the workstream-A plan.
const defaultDataDir = "/var/lib/flowstate-telemetry"

// proxyDeprecation explains why the proxy is retired. The daemon posted to
// <endpoint>/api/v1/ai-telemetry/ingest, which the OTLP/HTTP receiver at
// otel.flowstate.host does not serve (it serves only /v1/logs, /v1/metrics
// and /v1/traces).
const proxyDeprecation = "the Flowstate receiver no longer accepts proxy events; " +
	"tools send OpenTelemetry directly (flowstate-telemetry install --all). " +
	"Remove an existing proxy with: sudo flowstate-telemetry proxy uninstall"

// proxyCmd is the parent command grouping install/run/uninstall/status.
var proxyCmd = &cobra.Command{
	Use:        "proxy",
	Short:      "Deprecated: on-device HTTPS interceptor for AI provider traffic",
	Deprecated: proxyDeprecation,
	Long: `DEPRECATED: ` + proxyDeprecation + `

proxy manages the local HTTPS interceptor daemon. The daemon binds a
high local port, registers itself via PAC, and selectively MITMs traffic
destined for known AI provider hosts. Non-AI hosts are tunneled raw with
their original certificate chain intact.

Subcommands:
  install      Disabled (deprecated)
  run          Run the daemon in the foreground (what the launchd service invokes);
               it now only tunnels, intercepting and sending nothing
  uninstall    Reverse install: stop service, remove trust, unregister PAC, delete data (sudo)
  status       Report daemon health, allowlist version, and trust verification`,
}

var proxyInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the proxy daemon (requires sudo on macOS)",
	RunE:  runProxyInstall,
}

var proxyRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the proxy daemon in the foreground",
	RunE:  runProxyRun,
}

var proxyUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the proxy daemon (requires sudo on macOS)",
	RunE:  runProxyUninstall,
}

var proxyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report proxy daemon status",
	RunE:  runProxyStatus,
}

func init() {
	proxyCmd.PersistentFlags().IntVar(&flagProxyPort, "port", proxy.DefaultPort, "TCP port for the daemon to bind")
	proxyCmd.PersistentFlags().StringVar(&flagProxyDataDir, "data-dir", defaultDataDir, "Directory for CA + state persistence")

	proxyCmd.AddCommand(proxyInstallCmd)
	proxyCmd.AddCommand(proxyRunCmd)
	proxyCmd.AddCommand(proxyUninstallCmd)
	proxyCmd.AddCommand(proxyStatusCmd)
	rootCmd.AddCommand(proxyCmd)
}

// runProxyInstall refuses: the proxy is deprecated (proxyDeprecation).
func runProxyInstall(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("proxy install is disabled: %s", proxyDeprecation)
}

// runProxyRun is what launchd invokes for an install made before the proxy
// was deprecated. Machines still route traffic through it (PAC), so it keeps
// running, but only as a plain tunnel: the allowlist is empty, so nothing is
// intercepted, and no events are sent (the ingest endpoint is gone).
func runProxyRun(cmd *cobra.Command, args []string) error {
	fmt.Fprintln(os.Stderr, "flowstate-telemetry proxy is deprecated and now only tunnels traffic: "+proxyDeprecation)

	caDir := filepath.Join(flagProxyDataDir, "ca")
	cert, key, err := proxy.GenerateOrLoadCA(caDir)
	if err != nil {
		return fmt.Errorf("ca: %w", err)
	}

	d, err := proxy.New(proxy.DaemonConfig{
		Port:      flagProxyPort,
		CA:        cert,
		CAKey:     key,
		Allowlist: proxy.NewAllowlist(nil),
		Verbose:   flagVerbose,
	})
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return d.Run(ctx)
}

// runProxyUninstall reverses install. Each step is best-effort; we collect
// errors and report them at the end so a partial uninstall doesn't leave
// other artifacts behind.
func runProxyUninstall(cmd *cobra.Command, args []string) error {
	plat := platform.New()
	var firstErr error

	if err := plat.RemovePAC(); err != nil {
		fmt.Fprintf(os.Stderr, "remove PAC: %v\n", err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		fmt.Println("PAC unregistered.")
	}

	if err := plat.UninstallService(); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall service: %v\n", err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		fmt.Println("Daemon service removed.")
	}

	caDir := filepath.Join(flagProxyDataDir, "ca")
	certPath := filepath.Join(caDir, "ca.crt")
	if cert, err := readCert(certPath); err == nil {
		hash := proxy.CertFingerprintSHA256(cert)
		if err := plat.RemoveTrust(hash); err != nil {
			fmt.Fprintf(os.Stderr, "remove trust: %v\n", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			fmt.Println("CA trust removed.")
		}
	}

	if err := os.RemoveAll(flagProxyDataDir); err != nil {
		fmt.Fprintf(os.Stderr, "remove data dir: %v\n", err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		fmt.Printf("Data dir %s removed.\n", flagProxyDataDir)
	}
	return firstErr
}

// runProxyStatus reports the current state without requiring root. It
// includes daemon reachability, the CA fingerprint, allowlist contents, and
// the configured endpoint.
func runProxyStatus(cmd *cobra.Command, args []string) error {
	cfg := config.FromEnv(flagEndpoint, flagKey)

	caDir := filepath.Join(flagProxyDataDir, "ca")
	certPath := filepath.Join(caDir, "ca.crt")
	cert, err := readCert(certPath)
	if err != nil {
		fmt.Printf("CA: not installed (%s missing)\n", certPath)
	} else {
		fmt.Printf("CA: %s\n", cert.Subject.CommonName)
		fmt.Printf("CA fingerprint (SHA-256): %s\n", proxy.CertFingerprintSHA256(cert))
		fmt.Printf("CA expires: %s\n", cert.NotAfter.Format("2006-01-02"))
	}

	fmt.Printf("Daemon port: %d\n", flagProxyPort)
	fmt.Printf("Endpoint: %s\n", cfg.Endpoint)
	fmt.Printf("Telemetry key: %s\n", maskKey(cfg.Key))

	allow := proxy.NewAllowlist(proxy.SeedAllowlist)
	fmt.Printf("Allowlist (%d hosts):\n", len(allow.Hosts()))
	for _, h := range allow.Hosts() {
		fmt.Printf("  - %s\n", h)
	}

	return nil
}

// readCert loads a PEM-encoded certificate from disk. Used by status +
// uninstall to derive the SHA-256 fingerprint without re-running CA gen.
func readCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid PEM at %s", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

// maskKey shows the first 4 + last 4 chars of a telemetry key, hiding the
// middle. Empty input renders as "<unset>".
func maskKey(k string) string {
	if k == "" {
		return "<unset>"
	}
	if len(k) <= 8 {
		return "***"
	}
	return k[:4] + "..." + k[len(k)-4:]
}
