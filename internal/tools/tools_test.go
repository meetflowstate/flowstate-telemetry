package tools

import (
	"encoding/json"
	"flag"
	"io"
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

	golden(t, home, "gemini-0.65.zshrc", readFile(t, shell.ProfilePath()))
	if res := g.Verify(testCfg); !res.OK || res.Warning != "" || len(res.Notes) != 0 {
		t.Fatalf("verify (0.65): %+v", res)
	}

	// Older releases get the key from the wrapper only.
	withGeminiVersion(t, "0.64.0")
	install(t, g, testCfg)
	if strings.Contains(readFile(t, g.settingsPath()), "otlpHeaders") {
		t.Fatal("otlpHeaders written for a Gemini CLI that does not support it")
	}
	golden(t, home, "gemini-0.64.zshrc", readFile(t, shell.ProfilePath()))
	if res := g.Verify(testCfg); !res.OK || res.Warning != "" || len(res.Notes) != 1 {
		t.Fatalf("verify (0.64): %+v", res)
	}

	// Without the wrapper an old Gemini has no key at all.
	if err := shell.RemoveToolBlock(g.Name()); err != nil {
		t.Fatal(err)
	}
	if res := g.Verify(testCfg); res.OK {
		t.Fatalf("verify passed without a key: %+v", res)
	}

	if err := g.Remove(); err != nil {
		t.Fatal(err)
	}
	if shell.HasToolBlock(g.Name()) || strings.Contains(readFile(t, g.settingsPath()), "telemetry") {
		t.Fatal("remove incomplete")
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
	golden(t, home, "qwen.zshrc", readFile(t, shell.ProfilePath()))
	if res := q.Verify(testCfg); !res.OK || res.Warning != "" {
		t.Fatalf("verify: %+v", res)
	}
	if err := q.Remove(); err != nil {
		t.Fatal(err)
	}
	if shell.HasToolBlock(q.Name()) {
		t.Fatal("qwen wrapper not removed")
	}
}

func TestCopilotChat_Install(t *testing.T) {
	home := sandbox(t)
	c := &CopilotChat{}
	writeFile(t, c.settingsPath(), `{"editor.fontSize": 13, "github.copilot.chat.otel.exporterType": "otlp-grpc",
  "settingsSync.ignoredSettings": ["editor.fontSize"]}`)
	install(t, c, testCfg)
	golden(t, home, "copilot-chat.settings.json", readFile(t, c.settingsPath()))
	// Idempotent: the ignore entry is not duplicated.
	install(t, c, testCfg)
	if n := strings.Count(readFile(t, c.settingsPath()), `"github.copilot.chat.otel.headers"`); n != 2 {
		t.Fatalf("headers key appears %d times, want 2 (setting + ignore entry)", n)
	}
	if res := c.Verify(testCfg); !res.OK || res.Warning != "" || len(res.Notes) == 0 {
		t.Fatalf("verify: %+v", res)
	}
	// Even with the key in the environment, the setting is required.
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-flowstate-key=k")
	writeFile(t, c.settingsPath(), `{"github.copilot.chat.otel.enabled": true, "github.copilot.chat.otel.exporterType": "otlp-http",
  "github.copilot.chat.otel.otlpEndpoint": "https://otel.flowstate.host"}`)
	if res := c.Verify(testCfg); res.OK {
		t.Fatalf("verify passed without the headers setting: %+v", res)
	}

	install(t, c, testCfg)
	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c.settingsPath()); strings.Contains(got, "copilot") || strings.Contains(got, "settingsSync") {
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

func TestDryRun_WritesNothingAndRedactsKey(t *testing.T) {
	home := sandbox(t)
	withGeminiVersion(t, "0.65.0")
	cfg := testCfg
	cfg.DryRun = true
	out := captureStdout(t, func() {
		for _, tool := range All() {
			if err := tool.Install(cfg); err != nil {
				t.Fatalf("%s dry-run: %v", tool.Name(), err)
			}
		}
		if err := shell.WriteEnvVars(cfg, true); err != nil {
			t.Fatal(err)
		}
	})
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatalf("dry-run wrote files: %v", entries)
	}
	if strings.Contains(out, testCfg.Key) {
		t.Fatalf("dry-run output contains the key:\n%s", out)
	}
	if !strings.Contains(out, "<redacted>") {
		t.Fatalf("dry-run output shows no redacted key:\n%s", out)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	_ = w.Close()
	return <-done
}

// No install may export the key to every OpenTelemetry SDK: the shell
// profile, wrapper blocks included, never exports OTEL_EXPORTER_OTLP_*.
func TestInstall_NoGlobalOTelHeaders(t *testing.T) {
	sandbox(t)
	withGeminiVersion(t, "0.64.0")
	for _, tool := range All() {
		install(t, tool, testCfg)
	}
	if err := shell.WriteEnvVars(testCfg, false); err != nil {
		t.Fatal(err)
	}
	profile := readFile(t, shell.ProfilePath())
	for _, line := range strings.Split(profile, "\n") {
		if strings.Contains(line, "OTEL_EXPORTER_OTLP") && !strings.Contains(line, " command ") {
			t.Errorf("profile sets OTEL_EXPORTER_OTLP_* outside a wrapper: %s", line)
		}
		if strings.Contains(line, testCfg.Key) && !strings.HasPrefix(line, "export FLOWSTATE_OTLP_KEY=") {
			t.Errorf("key outside FLOWSTATE_OTLP_KEY: %s", line)
		}
	}
	for _, cmd := range []string{"copilot", "opencode", "gemini", "qwen"} {
		if !strings.Contains(profile, " command "+cmd+" ") {
			t.Errorf("no %s wrapper:\n%s", cmd, profile)
		}
	}
	if res := VerifyEnvironment(testCfg); !res.OK || res.Warning != "" {
		t.Fatalf("VerifyEnvironment after install: %+v", res)
	}
}

func TestCodexCLI_StripKeepsCommentsOfNextTable(t *testing.T) {
	raw := `model = "x"

[otel]
environment = "dev"
# about otel exporters
exporter = "none"

# Fast profile: keep this comment
[profiles.fast]
model = "y"

[otel.exporter.otlp-http]
# inside otel
endpoint = "https://otel.flowstate.inc/v1/logs"
`
	got := stripCodexOTel(raw)
	want := `model = "x"

# Fast profile: keep this comment
[profiles.fast]
model = "y"`
	if got != want {
		t.Fatalf("stripCodexOTel:\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCodexCLI_VerifyWarnsWhenAnalyticsOff(t *testing.T) {
	sandbox(t)
	c := &CodexCLI{}
	writeFile(t, c.configPath(), codexExisting) // [analytics] enabled = false
	install(t, c, testCfg)
	res := c.Verify(testCfg)
	if !res.OK || !strings.Contains(res.Warning, "[analytics] enabled = false") {
		t.Fatalf("verify: %+v", res)
	}
	if strings.Contains(res.Warning, "--prompts") {
		t.Fatalf("verify recommends prompt logging: %+v", res)
	}

	writeFile(t, c.configPath(), "[analytics]\nenabled = true\n")
	install(t, c, testCfg)
	if res := c.Verify(testCfg); !res.OK || res.Warning != "" {
		t.Fatalf("verify with analytics on: %+v", res)
	}
}

// verify never recommends --prompts; it warns when prompt logging is on.
func TestVerify_NeverRecommendsPrompts(t *testing.T) {
	sandbox(t)
	withGeminiVersion(t, "0.65.0")
	for _, prompts := range []bool{false, true} {
		cfg := testCfg
		cfg.Prompts = prompts
		for _, tool := range []Tool{&ClaudeCode{}, &CodexCLI{}, &GeminiCLI{}, &QwenCode{}, &CopilotChat{}} {
			install(t, tool, cfg)
			res := tool.Verify(cfg)
			if strings.Contains(res.Warning+res.Message, "--prompts") && !strings.Contains(res.Warning, "without --prompts") {
				t.Errorf("%s recommends --prompts: %+v", tool.Name(), res)
			}
			// Claude Code is not on PATH in tests, so it is skipped there.
			if res.OK && prompts != strings.Contains(res.Warning, "prompt content leaves the machine") {
				t.Errorf("%s prompts=%v warning = %q", tool.Name(), prompts, res.Warning)
			}
		}
	}
}

// An earlier release left the retired endpoint in tool configs and the hook
// env file; verify fails on each, and install rewrites them.
func TestLegacyEndpoint_VerifyFlagsAndInstallRewrites(t *testing.T) {
	home := sandbox(t)
	withGeminiVersion(t, "0.65.0")
	legacy := config.Config{Endpoint: "https://otel.flowstate.inc", Key: testCfg.Key}

	g, cx, cc, cur := &GeminiCLI{}, &CodexCLI{}, &CopilotChat{}, &CursorTool{}
	writeFile(t, cx.configPath(), "")
	writeFile(t, filepath.Join(home, ".cursor", "x"), "")
	writeFile(t, cc.settingsPath(), "{}")
	for _, tool := range []Tool{g, cx, cc, cur} {
		install(t, tool, legacy)
		if res := tool.Verify(testCfg); res.OK || !strings.Contains(res.Message, "otel.flowstate.inc") {
			t.Errorf("%s verify did not flag the legacy endpoint: %+v", tool.Name(), res)
		}
	}
	if res := VerifyEnvironment(testCfg); res.OK || !strings.Contains(res.Message, hooks.EnvFilePath()) {
		t.Fatalf("VerifyEnvironment did not flag the env file: %+v", res)
	}

	for _, tool := range []Tool{g, cx, cc, cur} {
		install(t, tool, testCfg)
		if res := tool.Verify(testCfg); !res.OK {
			t.Errorf("%s still failing after re-install: %+v", tool.Name(), res)
		}
	}
	if config.ContainsLegacyEndpoint(readFile(t, hooks.EnvFilePath())) {
		t.Fatal("env file not rewritten")
	}
}

func TestVerifyEnvironment(t *testing.T) {
	home := sandbox(t)
	old := systemEnvFiles
	t.Cleanup(func() { systemEnvFiles = old })
	etc := filepath.Join(home, "etc-environment")
	systemEnvFiles = []string{etc}

	if res := VerifyEnvironment(testCfg); !res.OK || res.Warning != "" {
		t.Fatalf("clean environment: %+v", res)
	}

	t.Setenv("FLOWSTATE_OTLP_ENDPOINT", "https://otel.flowstate.inc")
	if res := VerifyEnvironment(testCfg); res.OK {
		t.Fatalf("legacy FLOWSTATE_OTLP_ENDPOINT not flagged: %+v", res)
	}
	t.Setenv("FLOWSTATE_OTLP_ENDPOINT", "")

	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-flowstate-key=k")
	if res := VerifyEnvironment(testCfg); res.OK || !strings.Contains(res.Message, "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Fatalf("global headers not flagged: %+v", res)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")

	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "flowstate.team_id=t,flowstate.email=e@x")
	if res := VerifyEnvironment(testCfg); res.OK || !strings.Contains(res.Message, "OTEL_RESOURCE_ATTRIBUTES") {
		t.Fatalf("global resource attributes not flagged: %+v", res)
	}
	// A user's own attributes are none of our business.
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=dev")
	if res := VerifyEnvironment(testCfg); !res.OK {
		t.Fatalf("unrelated OTEL_RESOURCE_ATTRIBUTES flagged: %+v", res)
	}
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	writeFile(t, shell.ProfilePath(), "# --- Flowstate Telemetry ---\nexport FLOWSTATE_OTLP_ENDPOINT=\"https://otel.flowstate.inc\"\nexport OTEL_EXPORTER_OTLP_HEADERS=\"x-flowstate-key=k\"\nexport OTEL_RESOURCE_ATTRIBUTES=\"flowstate.team_id=t\"\n# --- End Flowstate Telemetry ---\n")
	res := VerifyEnvironment(testCfg)
	if res.OK || !strings.Contains(res.Message, "exports OTEL_EXPORTER_OTLP_HEADERS globally") || !strings.Contains(res.Message, "exports OTEL_RESOURCE_ATTRIBUTES globally") {
		t.Fatalf("legacy profile block not flagged: %+v", res)
	}
	// install rewrites the block, which clears all of it.
	if err := shell.WriteEnvVars(config.Config{Endpoint: testCfg.Endpoint, Key: testCfg.Key, TeamID: "t", Email: "e@x"}, false); err != nil {
		t.Fatal(err)
	}
	if res := VerifyEnvironment(testCfg); !res.OK {
		t.Fatalf("still failing after rewrite: %+v", res)
	}

	writeFile(t, etc, "PATH=/usr/bin\nOTEL_EXPORTER_OTLP_HEADERS=x-flowstate-key=k\nOTEL_RESOURCE_ATTRIBUTES=flowstate.email=e@x\nFLOWSTATE_OTLP_ENDPOINT=https://otel.flowstate.inc\n")
	if res := VerifyEnvironment(testCfg); res.OK || strings.Count(res.Message, etc) != 3 {
		t.Fatalf("MDM leftovers not flagged: %+v", res)
	}
}

// Team and e-mail reach only the tools, scoped: Claude Code's env block and
// the wrapper functions. Nothing exports them.
func TestResourceAttributes_Scoped(t *testing.T) {
	home := sandbox(t)
	withGeminiVersion(t, "0.65.0")
	cfg := testCfg
	cfg.TeamID, cfg.Email = "team-1", "dev@example.com"
	const attrs = "flowstate.team_id=team-1,flowstate.email=dev@example.com"

	c := &ClaudeCode{}
	writeFile(t, c.settingsPath(), `{"env": {"MY_VAR": "keep"}}`)
	for _, tool := range []Tool{c, &CopilotCLI{}, &OpenCodeTool{}, &GeminiCLI{}, &QwenCode{}} {
		install(t, tool, cfg)
	}
	if err := shell.WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, c.settingsPath()), `"OTEL_RESOURCE_ATTRIBUTES": "`+attrs+`"`) {
		t.Fatalf("Claude Code env block lacks the attributes:\n%s", readFile(t, c.settingsPath()))
	}
	golden(t, home, "resource-attrs.zshrc", readFile(t, shell.ProfilePath()))
	for _, tool := range []string{"copilot-cli", "opencode", "gemini-cli", "qwen-code"} {
		if !strings.Contains(shell.ToolBlock(tool), "OTEL_RESOURCE_ATTRIBUTES='"+attrs+"' ") {
			t.Errorf("%s wrapper lacks the attributes:\n%s", tool, shell.ToolBlock(tool))
		}
	}
	for _, line := range strings.Split(readFile(t, shell.ProfilePath()), "\n") {
		if strings.Contains(line, "OTEL_RESOURCE_ATTRIBUTES") && !strings.Contains(line, " command ") {
			t.Errorf("OTEL_RESOURCE_ATTRIBUTES outside a wrapper: %s", line)
		}
	}
	if res := VerifyEnvironment(cfg); !res.OK {
		t.Fatalf("VerifyEnvironment: %+v", res)
	}

	// Re-installing without team/e-mail drops our attributes again.
	install(t, c, testCfg)
	if strings.Contains(readFile(t, c.settingsPath()), "OTEL_RESOURCE_ATTRIBUTES") {
		t.Fatal("stale attributes left in the Claude Code env block")
	}
	install(t, c, cfg)
	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c.settingsPath()); strings.Contains(got, "OTEL_RESOURCE_ATTRIBUTES") || !strings.Contains(got, "MY_VAR") {
		t.Fatalf("remove result wrong:\n%s", got)
	}

	// A user's own attributes in Claude Code's env block are left alone.
	writeFile(t, c.settingsPath(), `{"env": {"OTEL_RESOURCE_ATTRIBUTES": "deployment.environment=dev"}}`)
	install(t, c, testCfg)
	if !strings.Contains(readFile(t, c.settingsPath()), "deployment.environment=dev") {
		t.Fatal("user's own OTEL_RESOURCE_ATTRIBUTES removed")
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
