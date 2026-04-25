//go:build windows

package platform

// New returns the Windows Platform implementation. The Windows trust store,
// Windows Service install, and WinINet PAC registry integration are a
// follow-up branch — this stub keeps the build green for cross-builds.
func New() Platform { return stubPlatform{name: "windows"} }
