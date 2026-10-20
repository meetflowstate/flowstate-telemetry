package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// remove --all cleans a tool's files even when the tool is not detected
// (claude not on PATH): the key must not stay in ~/.claude/settings.json.
func TestRemoveAll_CleansUndetectedTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PATH", t.TempDir()) // nothing detected
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"model": "opus", "env": {"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_EXPORTER_OTLP_HEADERS": "x-flowstate-key=secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	old := flagRemoveAll
	flagRemoveAll = true
	t.Cleanup(func() { flagRemoveAll = old })
	if err := runRemove(nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), "opus") {
		t.Fatalf("settings after remove --all:\n%s", data)
	}
}

// removeEnv points HOME at a fresh directory with nothing on PATH (no tool
// detected by its command) and returns it.
func removeEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PATH", t.TempDir())
	return home
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

// runRemoveWith runs remove with --all or --tool name.
func runRemoveWith(t *testing.T, all bool, tool string) error {
	t.Helper()
	oldAll, oldTool := flagRemoveAll, flagRemoveTool
	flagRemoveAll, flagRemoveTool = all, tool
	t.Cleanup(func() { flagRemoveAll, flagRemoveTool = oldAll, oldTool })
	return runRemove(nil, nil)
}

// remove --all does not fail on a file it cannot parse for a tool that is
// not configured (nothing of ours could be in it); it leaves the file alone.
// Named with --tool, the same file still fails.
func TestRemoveAll_UnparseableFileOfUnconfiguredToolIsAWarning(t *testing.T) {
	home := removeEnv(t)
	settings := filepath.Join(home, ".gemini", "settings.json")
	writeFile(t, settings, "{bad")
	if err := runRemoveWith(t, true, ""); err != nil {
		t.Fatalf("remove --all failed on an unconfigured tool's broken file: %v", err)
	}
	if data, err := os.ReadFile(settings); err != nil || string(data) != "{bad" {
		t.Fatalf("broken settings.json changed: %q, %v", data, err)
	}
	if err := runRemoveWith(t, false, "gemini-cli"); err == nil {
		t.Fatal("remove --tool gemini-cli succeeded on a broken settings.json")
	}
}

// A configured tool whose file cannot be parsed still fails remove --all.
func TestRemoveAll_UnparseableFileOfConfiguredToolFails(t *testing.T) {
	home := removeEnv(t)
	writeFile(t, filepath.Join(home, ".cursor", "hooks", "flowstate.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(home, ".cursor", "hooks.json"), "{bad")
	if err := runRemoveWith(t, true, ""); err == nil {
		t.Fatal("remove --all succeeded on a configured tool's broken hooks.json")
	}
}

func TestProxyInstallIsDisabled(t *testing.T) {
	err := runProxyInstall(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "install --all") {
		t.Fatalf("proxy install not disabled: %v", err)
	}
	if proxyCmd.Deprecated == "" {
		t.Fatal("proxy command not marked deprecated")
	}
}

func TestGenerateMDM_RejectsBadValues(t *testing.T) {
	for _, c := range []struct{ endpoint, key string }{
		{"https://otel.flowstate.host", "bad\nkey"},
		{`https://x.example/"$(id)"`, "k"},
		{"http://otel.flowstate.host", "k"},
		{"http://localhost:8787", "k"}, // artefacts always need https
	} {
		oldE, oldK, oldF := flagEndpoint, flagKey, flagMDMFormat
		flagEndpoint, flagKey, flagMDMFormat = c.endpoint, c.key, "jamf"
		err := runGenerateMDM(nil, nil)
		flagEndpoint, flagKey, flagMDMFormat = oldE, oldK, oldF
		if err == nil {
			t.Errorf("endpoint %q key %q accepted", c.endpoint, c.key)
		}
	}
	if generateMDMCmd.Flags().Lookup("tool") != nil {
		t.Error("generate-mdm still has the unused --tool flag")
	}
}
