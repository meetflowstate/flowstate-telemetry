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

// proxyCmd is the parent command grouping install/run/uninstall/status.
var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "On-device HTTPS interceptor for AI provider traffic",
	Long: `proxy manages the local HTTPS interceptor daemon. The daemon binds a
high local port, registers itself via PAC, and selectively MITMs traffic
destined for known AI provider hosts. Non-AI hosts are tunneled raw with
their original certificate chain intact.

Subcommands:
  install      Generate CA, install trust, install service, register PAC, start daemon (sudo)
  run          Run the daemon in the foreground (what the launchd service invokes)
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

// runProxyInstall performs the full install sequence: generate CA, install
// trust, install service, register PAC. It does NOT start the daemon
// directly — that's launchd's job once the service plist is bootstrapped.
func runProxyInstall(cmd *cobra.Command, args []string) error {
	cfg := config.FromEnv(flagEndpoint, flagKey)
	cfg.Verbose = flagVerbose

	caDir := filepath.Join(flagProxyDataDir, "ca")
	cert, _, err := proxy.GenerateOrLoadCA(caDir)
	if err != nil {
		return fmt.Errorf("ca: %w", err)
	}

	certPath := filepath.Join(caDir, "ca.crt")
	fmt.Printf("CA at %s\n", certPath)
	fmt.Printf("CA fingerprint (SHA-256): %s\n", proxy.CertFingerprintSHA256(cert))

	plat := platform.New()

	if err := plat.InstallTrust(certPath); err != nil {
		return fmt.Errorf("install trust: %w", err)
	}
	fmt.Println("CA installed in system trust store.")

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	if err := plat.InstallService(exe); err != nil {
		return fmt.Errorf("install service: %w", err)
	}
	fmt.Println("Daemon service installed.")

	pacURL := fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", flagProxyPort)
	if err := plat.InstallPAC(pacURL); err != nil {
		return fmt.Errorf("install PAC: %w", err)
	}
	fmt.Printf("PAC registered: %s\n", pacURL)

	fmt.Println("Install complete. The daemon is running under launchd.")
	fmt.Printf("Endpoint: %s\n", cfg.Endpoint)
	if cfg.Key == "" {
		fmt.Fprintln(os.Stderr, "Warning: no telemetry key set (FLOWSTATE_OTLP_KEY). Daemon will run but events will not be authenticated.")
	}
	return nil
}

// runProxyRun is what launchd invokes. It runs the daemon in the foreground
// until interrupted. Logs to stdout/stderr — launchd captures them.
func runProxyRun(cmd *cobra.Command, args []string) error {
	cfg := config.FromEnv(flagEndpoint, flagKey)
	cfg.Verbose = flagVerbose

	caDir := filepath.Join(flagProxyDataDir, "ca")
	cert, key, err := proxy.GenerateOrLoadCA(caDir)
	if err != nil {
		return fmt.Errorf("ca: %w", err)
	}

	allow := proxy.NewAllowlist(proxy.SeedAllowlist)

	var emitter proxy.EventEmitter
	if cfg.Endpoint != "" {
		fwd, err := proxy.NewForwarder(proxy.ForwarderConfig{
			Endpoint: cfg.Endpoint,
			Key:      cfg.Key,
		})
		if err != nil {
			return fmt.Errorf("forwarder: %w", err)
		}
		emitter = fwd
	}

	d, err := proxy.New(proxy.DaemonConfig{
		Port:      flagProxyPort,
		CA:        cert,
		CAKey:     key,
		Allowlist: allow,
		Emitter:   emitter,
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
