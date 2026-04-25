//go:build !darwin

package platform

import "fmt"

// stubPlatform returns errNotImplemented for every operation. Used by the
// linux/windows builds until their concrete implementations land in
// dedicated follow-up branches.
type stubPlatform struct {
	name string
}

func (s stubPlatform) errf(op string) error {
	return fmt.Errorf("platform/%s: %s: %w", s.name, op, errNotImplemented)
}

func (s stubPlatform) InstallTrust(string) error  { return s.errf("InstallTrust") }
func (s stubPlatform) RemoveTrust(string) error   { return s.errf("RemoveTrust") }
func (s stubPlatform) InstallPAC(string) error    { return s.errf("InstallPAC") }
func (s stubPlatform) RemovePAC() error           { return s.errf("RemovePAC") }
func (s stubPlatform) InstallService(string) error { return s.errf("InstallService") }
func (s stubPlatform) UninstallService() error    { return s.errf("UninstallService") }
