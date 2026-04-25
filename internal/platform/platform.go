// Package platform abstracts OS-specific install operations needed by the
// proxy daemon: trust-store install/remove, PAC registration, and service
// management. Concrete implementations live in build-tagged files
// (darwin.go, linux.go, windows.go).
//
// All public methods take side-effect verbs (Install/Remove/Uninstall) and
// are expected to be idempotent — calling InstallTrust on an already-trusted
// CA must succeed without error.
package platform

import (
	"errors"
	"os/exec"
)

// Platform is the interface a concrete OS implementation must satisfy.
type Platform interface {
	// InstallTrust adds the certificate at certPath to the OS trust store.
	// On macOS this lands in /Library/Keychains/System.keychain via
	// `security`. The operation requires root.
	InstallTrust(certPath string) error

	// RemoveTrust removes a certificate from the OS trust store, identified
	// by its SHA-256 fingerprint (lowercase hex). Returning nil for a cert
	// that is not present is acceptable.
	RemoveTrust(certHash string) error

	// InstallPAC registers a Proxy Auto-Config URL at the OS level.
	InstallPAC(url string) error

	// RemovePAC unregisters any previously installed PAC URL. Idempotent.
	RemovePAC() error

	// InstallService installs a system service (launchd plist on macOS,
	// systemd unit on Linux, Windows Service via sc.exe on Windows) that
	// launches `<execPath> proxy run` at boot. The operation requires root.
	InstallService(execPath string) error

	// UninstallService stops + removes the service. Idempotent.
	UninstallService() error
}

// commandRunner is the abstraction over exec.Command used by every platform
// implementation. Tests inject a fake to avoid actually shelling out to
// `security`, `networksetup`, `launchctl`, etc.
type commandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

// realRunner is the production implementation: a thin wrapper around
// exec.Command that returns combined stdout+stderr.
type realRunner struct{}

func (realRunner) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, err
	}
	return out, nil
}

// errNotImplemented is returned by stub platforms (Linux + Windows) until
// their concrete implementations land.
var errNotImplemented = errors.New("platform: not yet implemented for this OS")
