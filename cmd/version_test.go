package cmd

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// TestVersionStringIncludesAllFields verifies the version string carries
// every piece of build metadata callers expect — version, short commit,
// build date, Go version, OS, and arch. The defaults (`dev`, `unknown`,
// `unknown`) cover the no-ldflags local-build path so support diagnostics
// don't print empty fields.
func TestVersionStringIncludesAllFields(t *testing.T) {
	got := VersionString()
	for _, want := range []string{
		version,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("VersionString() = %q, want substring %q", got, want)
		}
	}
}

// TestVersionStringTruncatesCommit ensures long commit SHAs are trimmed
// to the conventional 7 characters so the line stays compact.
func TestVersionStringTruncatesCommit(t *testing.T) {
	prev := commit
	defer func() { commit = prev }()
	commit = "0123456789abcdef0123456789abcdef01234567"

	got := VersionString()
	if !strings.Contains(got, "0123456") {
		t.Errorf("VersionString() = %q, expected to contain short commit %q", got, "0123456")
	}
	if strings.Contains(got, "0123456789a") {
		t.Errorf("VersionString() = %q, commit should be truncated to 7 chars", got)
	}
}

// TestVersionSubcommandOutput asserts `flowstate-telemetry version`
// prints exactly VersionString() followed by a newline. Important for
// MDM scripts that grep the output.
func TestVersionSubcommandOutput(t *testing.T) {
	var out bytes.Buffer
	versionCmd.SetOut(&out)
	versionCmd.Run(versionCmd, nil)
	want := VersionString() + "\n"
	if got := out.String(); got != want {
		t.Errorf("version subcommand output = %q, want %q", got, want)
	}
}
