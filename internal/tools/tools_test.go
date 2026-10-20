package tools

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
	toml "github.com/pelletier/go-toml/v2"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// testCfg is the configuration every golden file is rendered with.
var testCfg = config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_test_key"}

// sandbox points HOME, SHELL and XDG_CONFIG_HOME at a temp dir so no test
// can touch the real ~/.claude, ~/.codex, ~/.gemini or shell profile.
func sandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	if h, _ := os.UserHomeDir(); h != home {
		t.Fatalf("HOME override not honoured: %s", h)
	}
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// golden compares got (with the sandbox HOME replaced by $HOME) against
// testdata/<name>, rewriting it under -update.
func golden(t *testing.T, home, name, got string) {
	t.Helper()
	got = strings.ReplaceAll(got, home, "$HOME")
	path := filepath.Join("testdata", name)
	if *update {
		writeFile(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run go test ./internal/tools -update): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func install(t *testing.T, tool Tool, cfg config.Config) {
	t.Helper()
	if err := tool.Install(cfg); err != nil {
		t.Fatalf("%s install: %v", tool.Name(), err)
	}
}

func TestClaudeCode_Install(t *testing.T) {
	home := sandbox(t)
	c := &ClaudeCode{}
	writeFile(t, c.settingsPath(), `{
  "model": "opus",
  "env": {
    "MY_VAR": "keep",
    "OTEL_METRICS_TEMPORALITY_PREFERENCE": "cumulative",
    "OTEL_LOG_USER_PROMPTS": "1"
  }
}`)
	install(t, c, testCfg)
	got := readFile(t, c.settingsPath())
	golden(t, home, "claude-code.settings.json", got)

	install(t, c, config.Config{Endpoint: testCfg.Endpoint, Key: testCfg.Key, Prompts: true})
	if !strings.Contains(readFile(t, c.settingsPath()), `"OTEL_LOG_USER_PROMPTS": "1"`) {
		t.Fatal("OTEL_LOG_USER_PROMPTS not written with --prompts")
	}

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	var after map[string]interface{}
	_ = json.Unmarshal([]byte(readFile(t, c.settingsPath())), &after)
	env, _ := after["env"].(map[string]interface{})
	if len(env) != 1 || env["MY_VAR"] != "keep" || after["model"] != "opus" {
		t.Fatalf("remove did not restore unrelated settings: %v", after)
	}
}

const codexExisting = `# my codex config
model = "gpt-5-codex" # inline comment

[analytics]
enabled = false

[otel]
environment = "production"
log_user_prompt = true

[otel.exporter.otlp-grpc]
endpoint = "https://old.example:4317"

[profiles.fast]
model = "gpt-5-mini"
`

func TestCodexCLI_Install(t *testing.T) {
	home := sandbox(t)
	c := &CodexCLI{}
	writeFile(t, c.configPath(), codexExisting)
	install(t, c, testCfg)
	got := readFile(t, c.configPath())
	golden(t, home, "codex.config.toml", got)

	var parsed map[string]interface{}
	if err := toml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("written config does not parse: %v", err)
	}
	if parsed["model"] != "gpt-5-codex" || parsed["profiles"] == nil || parsed["analytics"] == nil {
		t.Fatalf("unrelated config lost: %v", parsed)
	}
	otel := parsed["otel"].(map[string]interface{})
	for _, s := range codexSignals {
		exp := codexHTTPExporter(otel, s.key)
		if exp == nil || exp["endpoint"] != "https://otel.flowstate.host/v1/"+s.signal || exp["protocol"] != "binary" {
			t.Fatalf("%s wrong: %v", s.key, otel[s.key])
		}
	}
	if strings.Contains(got, "otlp-grpc") || otel["log_user_prompt"] != false {
		t.Fatalf("stale settings survived:\n%s", got)
	}

	// Re-installing is idempotent.
	install(t, c, testCfg)
	if again := readFile(t, c.configPath()); again != got {
		t.Fatalf("second install changed the file:\n%s", again)
	}

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, c.configPath())
	if strings.Contains(after, "otel") || !strings.Contains(after, "# my codex config") || !strings.Contains(after, "[profiles.fast]") {
		t.Fatalf("remove result wrong:\n%s", after)
	}
}

func TestCodexCLI_RejectsDottedOTelKeys(t *testing.T) {
	sandbox(t)
	c := &CodexCLI{}
	writeFile(t, c.configPath(), "otel.environment = \"dev\"\n")
	if err := c.Install(testCfg); err == nil {
		t.Fatal("expected an error for otel keys outside an [otel] table")
	}
}

func withGeminiVersion(t *testing.T, v string) {
	t.Helper()
	old := geminiVersion
	geminiVersion = func() string { return v }
	t.Cleanup(func() { geminiVersion = old })
}

func TestGeminiCLI_Install(t *testing.T) {
	home := sandbox(t)
	g := &GeminiCLI{}
	writeFile(t, g.settingsPath(), `{"theme": "dark", "telemetry": {"otlpProtocol": "grpc", "outfile": "/tmp/x"}}`)

	withGeminiVersion(t, "0.65.0-preview.0")
	install(t, g, testCfg)
	golden(t, home, "gemini.settings.json", readFile(t, g.settingsPath()))

	withGeminiVersion(t, "0.64.0")
	install(t, g, testCfg)
	if strings.Contains(readFile(t, g.settingsPath()), "otlpHeaders") {
		t.Fatal("otlpHeaders written for a Gemini CLI that does not support it")
	}
}

func TestGeminiSupportsHeaders(t *testing.T) {
	for v, want := range map[string]bool{
		"0.64.0": false, "0.65.0": true, "0.65.0-preview.0": true, "v1.0.0": true, "": false, "nightly": false,
	} {
		if got := geminiSupportsHeaders(v); got != want {
			t.Errorf("geminiSupportsHeaders(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestQwenCode_Install(t *testing.T) {
	home := sandbox(t)
	q := &QwenCode{}
	writeFile(t, q.settingsPath(), `{"telemetry": {"otlpProtocol": "grpc"}}`)
	install(t, q, testCfg)
	got := readFile(t, q.settingsPath())
	golden(t, home, "qwen.settings.json", got)
	if strings.Contains(got, "otlpHeaders") {
		t.Fatal("Qwen has no otlpHeaders setting")
	}
}

func TestCopilotChat_Install(t *testing.T) {
	home := sandbox(t)
	c := &CopilotChat{}
	writeFile(t, c.settingsPath(), `{"editor.fontSize": 13, "github.copilot.chat.otel.exporterType": "otlp-grpc"}`)
	install(t, c, testCfg)
	golden(t, home, "copilot-chat.settings.json", readFile(t, c.settingsPath()))

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c.settingsPath()); strings.Contains(got, "copilot") || !strings.Contains(got, "fontSize") {
		t.Fatalf("remove result wrong:\n%s", got)
	}
}

func TestCopilotCLI_Install(t *testing.T) {
	home := sandbox(t)
	c := &CopilotCLI{}
	install(t, c, testCfg)
	got := readFile(t, shell.ProfilePath())
	golden(t, home, "copilot-cli.zshrc", got)
	if strings.Contains(got, "export OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Fatal("OTEL_EXPORTER_OTLP_ENDPOINT must be scoped to the wrapper, not exported")
	}
	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, shell.ProfilePath()), "copilot") {
		t.Fatal("wrapper not removed")
	}
}

func TestOpenCode_Install(t *testing.T) {
	home := sandbox(t)
	o := &OpenCodeTool{}
	writeFile(t, o.configPath(), `{
  "$schema": "https://opencode.ai/config.json",
  "plugin": ["opencode-plugin-otel", "my-plugin"],
  "plugins": ["opencode-plugin-otel"],
  "env": {"OTEL_EXPORTER_OTLP_ENDPOINT": "https://old", "OTEL_EXPORTER_OTLP_HEADERS": "x=y"}
}`)
	install(t, o, testCfg)
	golden(t, home, "opencode.json", readFile(t, o.configPath()))
	golden(t, home, "opencode.zshrc", readFile(t, shell.ProfilePath()))

	if err := o.Remove(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.configPath())
	if strings.Contains(got, "openTelemetry") || !strings.Contains(got, "my-plugin") {
		t.Fatalf("remove result wrong:\n%s", got)
	}
	if strings.Contains(readFile(t, shell.ProfilePath()), "opencode") {
		t.Fatal("wrapper not removed")
	}
}

func assertExecutable(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0100 == 0 {
		t.Fatalf("%s is not executable", path)
	}
}

func assertEnvFile(t *testing.T) {
	t.Helper()
	st, err := os.Stat(hooks.EnvFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("env file mode = %v, want 0600", st.Mode().Perm())
	}
	if got := readFile(t, hooks.EnvFilePath()); got != "FLOWSTATE_OTLP_ENDPOINT=https://otel.flowstate.host\nFLOWSTATE_OTLP_KEY=fs_test_key\n" {
		t.Fatalf("env file = %q", got)
	}
}

func TestCursor_Install(t *testing.T) {
	home := sandbox(t)
	c := &CursorTool{}
	writeFile(t, c.hooksConfigPath(), `{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [{"command": "`+c.hookScriptPath()+`"}],
    "stop": [{"command": "./my-stop.sh"}, {"command": "`+c.hookScriptPath()+`"}]
  }
}`)
	install(t, c, testCfg)
	golden(t, home, "cursor.hooks.json", readFile(t, c.hooksConfigPath()))
	assertExecutable(t, c.hookScriptPath())
	assertEnvFile(t)
	if !c.registered() {
		t.Fatal("hooks not registered")
	}

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, c.hooksConfigPath())
	if strings.Contains(got, "flowstate") || !strings.Contains(got, "my-stop.sh") {
		t.Fatalf("remove result wrong:\n%s", got)
	}
}

func TestWindsurf_Install(t *testing.T) {
	home := sandbox(t)
	w := &WindsurfTool{}
	writeFile(t, w.hooksConfigPath(), `{
  "hooks": {
    "post_cascade_response_with_transcript": [{"command": "`+w.hookScriptPath()+`"}],
    "pre_run_command": [{"command": "/usr/local/bin/guard"}]
  }
}`)
	install(t, w, testCfg)
	golden(t, home, "windsurf.hooks.json", readFile(t, w.hooksConfigPath()))
	assertExecutable(t, w.hookScriptPath())
	assertEnvFile(t)

	if err := w.Remove(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, w.hooksConfigPath()); strings.Contains(got, "flowstate") || !strings.Contains(got, "guard") {
		t.Fatalf("remove result wrong:\n%s", got)
	}
}

func TestAntigravity_Install(t *testing.T) {
	home := sandbox(t)
	a := &AntigravityTool{}
	writeFile(t, a.hooksConfigPath(), `{"mine": {"enabled": true, "Stop": [{"type": "command", "command": "echo hi"}]}}`)
	install(t, a, testCfg)
	golden(t, home, "antigravity.hooks.json", readFile(t, a.hooksConfigPath()))
	assertExecutable(t, a.hookScriptPath())
	assertEnvFile(t)
	if !a.registered() {
		t.Fatal("hooks not registered")
	}

	if err := a.Remove(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, a.hooksConfigPath()); strings.Contains(got, "flowstate") || !strings.Contains(got, "echo hi") {
		t.Fatalf("remove result wrong:\n%s", got)
	}
}

func TestDryRun_WritesNothing(t *testing.T) {
	home := sandbox(t)
	withGeminiVersion(t, "0.65.0")
	cfg := testCfg
	cfg.DryRun = true
	for _, tool := range All() {
		if err := tool.Install(cfg); err != nil {
			t.Fatalf("%s dry-run: %v", tool.Name(), err)
		}
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatalf("dry-run wrote files: %v", entries)
	}
}

func TestHookCommand_QuotesPaths(t *testing.T) {
	if got := hookCommand("/a b/flowstate.sh", "stop"); got != `'/a b/flowstate.sh' stop` {
		t.Fatalf("hookCommand = %q", got)
	}
	if !isFlowstateHook(map[string]interface{}{"command": `'/a b/flowstate.sh' stop`}, "/a b/flowstate.sh") {
		t.Fatal("quoted command not recognised")
	}
}
