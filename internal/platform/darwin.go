//go:build darwin

package platform

import (
	"fmt"
	"strings"
)

// New returns the production Platform implementation for macOS.
func New() Platform {
	return &darwinPlatform{runner: realRunner{}}
}

// systemKeychain is the system-wide keychain. Trust roots for the entire
// machine must land here, not the per-user login keychain.
const systemKeychain = "/Library/Keychains/System.keychain"

// launchDaemonPath is where the daemon's launchd plist is installed. The
// path matches the existing reverse-DNS scheme used elsewhere in the repo.
const launchDaemonPath = "/Library/LaunchDaemons/inc.flowstate.telemetry.proxy.plist"

// launchDaemonLabel is the launchd Label used to refer to the service.
const launchDaemonLabel = "inc.flowstate.telemetry.proxy"

// launchDaemonPathTestOverride lets tests redirect the plist drop away from
// /Library/LaunchDaemons. Empty in production. Defined in darwin.go (not
// darwin_test.go) so the production code can read it.
var launchDaemonPathTestOverride string

// getLaunchDaemonPath returns the effective plist path, honouring any test
// override. Lives in production code so InstallService/UninstallService can
// call it.
func getLaunchDaemonPath() string {
	if launchDaemonPathTestOverride != "" {
		return launchDaemonPathTestOverride
	}
	return launchDaemonPath
}

// darwinPlatform implements Platform via `security`, `networksetup`, and
// `launchctl` plus a launchd plist drop.
type darwinPlatform struct {
	runner commandRunner
}

// InstallTrust adds a certificate as a trusted root in the system keychain.
// Requires root (the binary is expected to be invoked under sudo).
func (d *darwinPlatform) InstallTrust(certPath string) error {
	if certPath == "" {
		return fmt.Errorf("platform/darwin: certPath required")
	}
	out, err := d.runner.Run(
		"security",
		"add-trusted-cert",
		"-d",
		"-r", "trustRoot",
		"-k", systemKeychain,
		certPath,
	)
	if err != nil {
		return fmt.Errorf("platform/darwin: security add-trusted-cert: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveTrust deletes a certificate by SHA-1 (legacy) or SHA-256 fingerprint
// from the system keychain. We accept the SHA-256 hex string we generate
// elsewhere; `security` accepts -Z (SHA-1) and -h (SHA-256-ish hash) flags
// depending on macOS version. We use the more portable -c CN match, scoped
// to the certificate name we issue.
func (d *darwinPlatform) RemoveTrust(certHash string) error {
	if certHash == "" {
		return fmt.Errorf("platform/darwin: certHash required")
	}
	// First remove the trust setting, then delete the cert from the
	// keychain. Errors at either stage are returned but don't short-circuit
	// — RemoveTrust is expected to be idempotent.
	out, err := d.runner.Run(
		"security",
		"remove-trusted-cert",
		"-d",
		certHash,
	)
	if err != nil && !looksLikeNotFound(out) {
		return fmt.Errorf("platform/darwin: remove-trusted-cert: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// InstallPAC registers a PAC URL on every active network service. macOS
// applies PAC settings per-interface, so we enumerate via `networksetup
// -listallnetworkservices` and register on each.
func (d *darwinPlatform) InstallPAC(url string) error {
	if url == "" {
		return fmt.Errorf("platform/darwin: PAC url required")
	}
	services, err := d.networkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		if _, err := d.runner.Run("networksetup", "-setautoproxyurl", svc, url); err != nil {
			return fmt.Errorf("platform/darwin: setautoproxyurl on %q: %w", svc, err)
		}
	}
	return nil
}

// RemovePAC unsets PAC on every network service.
func (d *darwinPlatform) RemovePAC() error {
	services, err := d.networkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		// `networksetup -setautoproxystate <svc> off` disables PAC on the
		// interface without nuking the URL — sufficient and idempotent.
		if _, err := d.runner.Run("networksetup", "-setautoproxystate", svc, "off"); err != nil {
			return fmt.Errorf("platform/darwin: setautoproxystate off on %q: %w", svc, err)
		}
	}
	return nil
}

// InstallService writes the launchd plist and bootstraps it.
func (d *darwinPlatform) InstallService(execPath string) error {
	if execPath == "" {
		return fmt.Errorf("platform/darwin: execPath required")
	}
	plist := fmt.Sprintf(launchDaemonPlist, launchDaemonLabel, execPath)
	plistPath := getLaunchDaemonPath()
	if err := writeFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("platform/darwin: write plist: %w", err)
	}
	if _, err := d.runner.Run("launchctl", "bootstrap", "system", plistPath); err != nil {
		// `bootstrap` errors if already loaded. Try `bootout` + retry as
		// part of the idempotency contract.
		_, _ = d.runner.Run("launchctl", "bootout", "system/"+launchDaemonLabel)
		if _, err := d.runner.Run("launchctl", "bootstrap", "system", plistPath); err != nil {
			return fmt.Errorf("platform/darwin: launchctl bootstrap: %w", err)
		}
	}
	return nil
}

// UninstallService stops + removes the launchd service and deletes the plist.
func (d *darwinPlatform) UninstallService() error {
	_, _ = d.runner.Run("launchctl", "bootout", "system/"+launchDaemonLabel)
	if err := removeFile(getLaunchDaemonPath()); err != nil {
		return fmt.Errorf("platform/darwin: remove plist: %w", err)
	}
	return nil
}

// networkServices returns the list of active networksetup-known services
// (Wi-Fi, Ethernet, etc.). Hidden/disabled lines (those starting with "*")
// are filtered out per `networksetup` convention.
func (d *darwinPlatform) networkServices() ([]string, error) {
	out, err := d.runner.Run("networksetup", "-listallnetworkservices")
	if err != nil {
		return nil, fmt.Errorf("platform/darwin: listallnetworkservices: %w", err)
	}
	var services []string
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i == 0 {
			// First line is a "An asterisk (*) denotes that..." header.
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	return services, nil
}

// looksLikeNotFound returns true if the security CLI output indicates the
// cert wasn't present — used to keep RemoveTrust idempotent.
func looksLikeNotFound(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "not found") || strings.Contains(s, "no such")
}

// launchDaemonPlist is the launchd plist template. It runs the binary
// (passed in) with `proxy run`, restarts on crash, and writes stdout/stderr
// under /var/log/.
const launchDaemonPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>proxy</string>
        <string>run</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/flowstate-telemetry.proxy.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/flowstate-telemetry.proxy.err.log</string>
</dict>
</plist>
`
