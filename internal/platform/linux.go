//go:build linux

package platform

// New returns the Linux Platform implementation. Linux trust/service/PAC
// integration is intentionally a follow-up branch — this stub exists so the
// build stays green when goreleaser produces linux binaries.
func New() Platform { return stubPlatform{name: "linux"} }
