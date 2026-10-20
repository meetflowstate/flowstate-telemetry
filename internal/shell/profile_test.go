package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// sandbox points HOME and SHELL at a temp dir so nothing touches the real
// profile, and returns the profile path.
func sandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	return filepath.Join(home, ".zshrc")
}

func TestBuildEnvBlock_Golden(t *testing.T) {
	cfg := config.Config{
		Endpoint: "https://otel.flowstate.host",
		Key:      "fs_key",
		TeamID:   "team-1",
		Email:    "dev@example.com",
	}
	want := `# --- Flowstate Telemetry ---
export FLOWSTATE_OTLP_ENDPOINT="https://otel.flowstate.host"
export FLOWSTATE_OTLP_KEY="fs_key"
export OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=fs_key"
export OTEL_RESOURCE_ATTRIBUTES="flowstate.team_id=team-1,flowstate.email=dev@example.com"
# --- End Flowstate Telemetry ---`
	if got := buildEnvBlock(cfg); got != want {
		t.Fatalf("block mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildEnvBlock_NoServiceNameAndOmitsEmptyAttributes(t *testing.T) {
	got := buildEnvBlock(config.Config{Endpoint: "https://otel.flowstate.host", Key: "k"})
	if strings.Contains(got, "service.name") {
		t.Fatalf("block must not set service.name:\n%s", got)
	}
	if strings.Contains(got, "OTEL_RESOURCE_ATTRIBUTES") {
		t.Fatalf("empty OTEL_RESOURCE_ATTRIBUTES must be omitted:\n%s", got)
	}
}

func TestWriteEnvVars_ReplacesBlockAndPreservesProfile(t *testing.T) {
	profile := sandbox(t)
	if err := os.WriteFile(profile, []byte("export PATH=/x:$PATH\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Endpoint: "https://a.example", Key: "one"}
	if err := WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	cfg.Key = "two"
	if err := WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(profile)
	s := string(data)
	if !strings.HasPrefix(s, "export PATH=/x:$PATH\n") {
		t.Fatalf("existing content lost:\n%s", s)
	}
	if strings.Count(s, markerStart) != 1 || strings.Contains(s, `"one"`) || !strings.Contains(s, `"two"`) {
		t.Fatalf("block not replaced:\n%s", s)
	}

	if err := RemoveEnvVars(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(profile)
	if strings.Contains(string(data), "Flowstate") {
		t.Fatalf("block not removed:\n%s", data)
	}
}

func TestToolBlock_IndependentOfMainBlock(t *testing.T) {
	profile := sandbox(t)
	cfg := config.Config{Endpoint: "https://a.example", Key: "k"}
	if err := WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteToolBlock("copilot-cli", []string{"copilot() { command copilot \"$@\"; }"}, false); err != nil {
		t.Fatal(err)
	}
	if !HasToolBlock("copilot-cli") {
		t.Fatal("tool block not detected")
	}
	// Rewriting the main block must leave the tool block alone.
	if err := WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	if !HasToolBlock("copilot-cli") {
		t.Fatal("tool block lost when main block was rewritten")
	}
	if err := RemoveToolBlock("copilot-cli"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(profile)
	if strings.Contains(string(data), "copilot") || !strings.Contains(string(data), markerStart) {
		t.Fatalf("wrong block removed:\n%s", data)
	}

	// RemoveEnvVars (remove --all) clears tool blocks too, even for tools
	// that are no longer installed.
	_ = WriteToolBlock("opencode", []string{"x"}, false)
	_ = WriteToolBlock("copilot-cli", []string{"y"}, false)
	if err := RemoveEnvVars(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(profile)
	if strings.Contains(string(data), "Flowstate") {
		t.Fatalf("blocks left behind:\n%s", data)
	}
}

func TestInstallGUIEnv(t *testing.T) {
	sandbox(t)
	var calls []string
	oldRun, oldOS := runCommand, goos
	t.Cleanup(func() { runCommand, goos = oldRun, oldOS })
	goos = "darwin"
	runCommand = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}

	cfg := config.Config{Endpoint: "https://otel.flowstate.host", Key: "k'ey"}
	if err := InstallGUIEnv(cfg, false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"launchctl setenv FLOWSTATE_OTLP_ENDPOINT https://otel.flowstate.host",
		"launchctl setenv FLOWSTATE_OTLP_KEY k'ey",
		"launchctl setenv OTEL_EXPORTER_OTLP_HEADERS x-flowstate-key=k'ey",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(calls, "\n"))
	}
	plist, err := os.ReadFile(GUIEnvAgentPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plist), `/bin/launchctl setenv FLOWSTATE_OTLP_KEY &#39;k&#39;\&#39;&#39;ey&#39;`) {
		t.Fatalf("plist not quoted/escaped:\n%s", plist)
	}
	if st, _ := os.Stat(GUIEnvAgentPath()); st.Mode().Perm() != 0600 {
		t.Fatalf("plist mode = %v, want 0600", st.Mode().Perm())
	}

	calls = nil
	if err := RemoveGUIEnv(); err != nil {
		t.Fatal(err)
	}
	if GUIEnvInstalled() || len(calls) != 3 {
		t.Fatalf("remove incomplete: installed=%v calls=%v", GUIEnvInstalled(), calls)
	}
}

func TestInstallGUIEnv_RejectsNonDarwin(t *testing.T) {
	sandbox(t)
	old := goos
	t.Cleanup(func() { goos = old })
	goos = "linux"
	if err := InstallGUIEnv(config.Config{}, false); err == nil {
		t.Fatal("expected an error off macOS")
	}
}
