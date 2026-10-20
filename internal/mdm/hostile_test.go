package mdm

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"gopkg.in/yaml.v3"
)

// hostileKey has every character that could break out of a quoted string in
// PowerShell, YAML/Jinja, JSON or a shell: quotes, $, backticks, $(...),
// backslashes and braces. Control characters are rejected at input
// (config.Validate) and by the artefacts' own checks.
const hostileKey = "k'e\"y$HOME`id`$(touch pwned)\\z{{ x }}’"

func generate(t *testing.T, gen func(*bytes.Buffer) error) string {
	t.Helper()
	var buf bytes.Buffer
	if err := gen(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestIntune_QuotesHostileKey(t *testing.T) {
	out := generate(t, func(b *bytes.Buffer) error {
		return GenerateIntune(b, config.Config{Endpoint: "https://otel.flowstate.host", Key: hostileKey})
	})
	want := "$Key = 'k''e\"y$HOME`id`$(touch pwned)\\z{{ x }}’’'"
	if !strings.Contains(out, want) {
		t.Fatalf("key not a single-quoted PowerShell literal; want %s in:\n%s", want, out)
	}
	if !strings.Contains(out, "$Endpoint = 'https://otel.flowstate.host'") {
		t.Error("endpoint not single-quoted")
	}
	if pwsh, err := exec.LookPath("pwsh"); err == nil {
		line := want + "\n$Key"
		cmd := exec.Command(pwsh, "-NoProfile", "-Command", line)
		cmd.Dir = t.TempDir()
		got, err := cmd.Output()
		if err != nil || strings.TrimRight(string(got), "\r\n") != hostileKey {
			t.Errorf("pwsh read %q (%v), want %q", got, err, hostileKey)
		}
	}
}

// Ansible: the embedded defaults are !unsafe single-quoted YAML (never
// templated), the JSON files get to_json values, the profile a quoted value,
// and the play checks overridden values itself.
func TestAnsible_HostileKey(t *testing.T) {
	out := generate(t, func(b *bytes.Buffer) error {
		return GenerateAnsible(b, config.Config{Endpoint: "https://otel.flowstate.host", Key: hostileKey})
	})
	var doc []map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not YAML: %v", err)
	}
	vars, _ := doc[0]["vars"].(map[string]interface{})
	if vars["flowstate_default_key"] != hostileKey {
		t.Fatalf("default key = %q, want %q", vars["flowstate_default_key"], hostileKey)
	}
	for _, want := range []string{
		"flowstate_default_key: !unsafe '", "flowstate_default_endpoint: !unsafe 'https://otel.flowstate.host'",
		"{{ fs_key | to_json }}", "{{ fs_endpoint | to_json }}", "{{ fs_endpoint | quote }}",
		"fs_key is not search(", "fs_endpoint is match('^https://",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ansible output missing %q", want)
		}
	}
	for _, banned := range []string{`"{{ fs_key }}"`, `"{{ fs_endpoint }}"`, `default('`} {
		if strings.Contains(out, banned) {
			t.Errorf("ansible output contains %q", banned)
		}
	}
}

// Puppet: the key is a single-quoted Puppet string, and the manifest refuses
// a key with control characters or an endpoint that is not a plain https URL.
func TestPuppet_HostileKey(t *testing.T) {
	out := generate(t, func(b *bytes.Buffer) error {
		return GeneratePuppet(b, config.Config{Endpoint: "https://otel.flowstate.host", Key: hostileKey})
	})
	if !strings.Contains(out, `String $key      = 'k\'e"y$HOME`+"`id`"+`$(touch pwned)\\z{{ x }}`+"’'") {
		t.Errorf("key not single-quoted for Puppet:\n%s", out)
	}
	for _, want := range []string{`if $key =~ /[[:cntrl:]]/ {`, `unless $endpoint =~ /\Ahttps:\/\/`} {
		if !strings.Contains(out, want) {
			t.Errorf("puppet output missing %q", want)
		}
	}
}

// extractBetween returns the text after start up to end.
func extractBetween(t *testing.T, s, start, end string) string {
	t.Helper()
	i := strings.Index(s, start)
	if i < 0 {
		t.Fatalf("%q not found", start)
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		t.Fatalf("%q not found", end)
	}
	return s[:j]
}

func needPython(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	return py
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func mustWrite(t *testing.T, path, text string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()&os.ModeSymlink != 0
}

// Jamf's merge follows a symlinked settings.json (dotfiles) and keeps the
// link, tightens a group/world-readable file, and skips (warns, exit 0) a
// link that leads to a file the user does not own.
func TestJamf_MergeClaudeFollowsOwnSymlink(t *testing.T) {
	py := needPython(t)
	out := generate(t, func(b *bytes.Buffer) error {
		return GenerateJamf(b, config.Config{Endpoint: "https://otel.flowstate.host", Key: hostileKey})
	})
	merge := extractBetween(t, out, "read -r -d '' MERGE_CLAUDE <<'PYEOF' || true\n", "\nPYEOF\n")

	home := t.TempDir()
	dotfiles := filepath.Join(home, "dotfiles", "claude.json")
	mustWrite(t, dotfiles, `{"model": "opus", "env": {"MINE": "1"}}`, 0644)
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfiles, settings); err != nil {
		t.Fatal(err)
	}
	run := func(path string) ([]byte, error) {
		cmd := exec.Command(py, "-c", merge, path)
		cmd.Env = append(os.Environ(), "FS_ENDPOINT=https://otel.flowstate.host", "FS_KEY="+hostileKey)
		return cmd.CombinedOutput()
	}
	if b, err := run(settings); err != nil {
		t.Fatalf("merge: %v\n%s", err, b)
	}
	if !isSymlink(t, settings) {
		t.Fatal("settings.json symlink was replaced by a regular file")
	}
	var got struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(mustRead(t, dotfiles)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "opus" || got.Env["MINE"] != "1" || got.Env["OTEL_EXPORTER_OTLP_HEADERS"] != "x-flowstate-key="+hostileKey {
		t.Fatalf("merged settings wrong: %+v", got)
	}
	if m := mode(t, dotfiles); m != 0600 {
		t.Errorf("mode = %o, want 0600 (tightened from 0644)", m)
	}

	// A link to a file this user does not own (root's) is skipped with a
	// warning: not an error, so the policy run goes on.
	if os.Getuid() != 0 {
		other := filepath.Join(home, ".claude", "other.json")
		if err := os.Symlink("/etc/hosts", other); err != nil {
			t.Fatal(err)
		}
		before := mustRead(t, "/etc/hosts")
		b, err := run(other)
		if err != nil || !strings.Contains(string(b), "warning:") || !strings.Contains(string(b), "not a file this user owns") {
			t.Fatalf("link to /etc/hosts: want a warning and exit 0, got %v\n%s", err, b)
		}
		if mustRead(t, "/etc/hosts") != before || !isSymlink(t, other) {
			t.Fatal("link to /etc/hosts changed")
		}
	}
}

// A settings.json linked to a file the user does not own is only a warning
// in configure_user (with python3): the user is still configured and the
// Cursor hooks step still runs.
func TestJamf_ConfigureUserSkipsForeignSymlink(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root")
	}
	py := needPython(t)
	out := generate(t, func(b *bytes.Buffer) error {
		return GenerateJamf(b, config.Config{Endpoint: "https://otel.flowstate.host", Key: hostileKey})
	})
	merge := extractBetween(t, out, "read -r -d '' MERGE_CLAUDE <<'PYEOF' || true\n", "\nPYEOF\n")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.Symlink("/etc/hosts", settings); err != nil {
		t.Fatal(err)
	}
	b, err := runConfigureUser(t, out, home, py, merge, hostileKey)
	if err != nil {
		t.Fatalf("configure_user failed on a foreign symlink: %v\n%s", err, b)
	}
	if !strings.Contains(string(b), "warning:") {
		t.Errorf("no warning printed:\n%s", b)
	}
	if !isSymlink(t, settings) {
		t.Error("settings.json symlink replaced")
	}
	if fi, err := os.Stat(filepath.Join(home, ".cursor", "hooks")); err != nil || !fi.IsDir() {
		t.Errorf("Cursor hooks step skipped: %v", err)
	}
}

// runConfigureUser runs Jamf's configure_user for home with the given
// python3 and merge script ("" for none), feeding the endpoint and key on
// stdin.
func runConfigureUser(t *testing.T, script, home, python, merge, key string) ([]byte, error) {
	t.Helper()
	fn := extractBetween(t, script, "configure_user() {", "\n}\n\n# Configure each user")
	cmd := exec.Command("/bin/bash", "-c", "configure_user() {"+fn+"\n}\nconfigure_user \"$@\"", "x", home, python, merge)
	cmd.Stdin = strings.NewReader("https://otel.flowstate.host\n" + key + "\n")
	cmd.Dir = home
	return cmd.CombinedOutput()
}

// configureUser runs Jamf's configure_user for home with no python3 (the
// fallback heredoc).
func configureUser(t *testing.T, script, home, key string) {
	t.Helper()
	if b, err := runConfigureUser(t, script, home, "", "", key); err != nil {
		t.Fatalf("configure_user: %v\n%s", err, b)
	}
}

// Without python3, Jamf writes a new settings.json itself: the key must be
// JSON-escaped, and $(...) or backticks in it are never run.
func TestJamf_NoPythonFallbackEscapesKey(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("no /bin/bash")
	}
	out := generate(t, func(b *bytes.Buffer) error {
		return GenerateJamf(b, config.Config{Endpoint: "https://otel.flowstate.host", Key: "k"})
	})
	home := t.TempDir()
	configureUser(t, out, home, hostileKey)
	var got struct {
		Env map[string]string `json:"env"`
	}
	raw := mustRead(t, filepath.Join(home, ".claude", "settings.json"))
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	if got.Env["OTEL_EXPORTER_OTLP_HEADERS"] != "x-flowstate-key="+hostileKey {
		t.Fatalf("headers = %q", got.Env["OTEL_EXPORTER_OTLP_HEADERS"])
	}
	if _, err := os.Stat(filepath.Join(home, "pwned")); err == nil {
		t.Fatal("$(...) in the key was executed")
	}
	if env := mustRead(t, filepath.Join(home, ".config", "flowstate-telemetry", "env")); !strings.Contains(env, "FLOWSTATE_OTLP_KEY="+hostileKey+"\n") {
		t.Fatalf("env file = %q", env)
	}
	if m := mode(t, filepath.Join(home, ".claude", "settings.json")); m != 0600 {
		t.Errorf("settings mode = %o, want 0600", m)
	}
}

// A parameter-4 key with a newline stops the script before anything changes.
func TestJamf_RejectsControlCharacterKey(t *testing.T) {
	out := generate(t, func(b *bytes.Buffer) error {
		return GenerateJamf(b, config.Config{Endpoint: "https://otel.flowstate.host", Key: "k"})
	})
	head := out[:strings.Index(out, "echo \"==> Configuring")]
	cmd := exec.Command("/bin/bash", "-c", head+"\necho reached", "jamf", "", "", "", "bad\nkey")
	b, err := cmd.CombinedOutput()
	if err == nil || strings.Contains(string(b), "reached") {
		t.Fatalf("control character accepted: %v\n%s", err, b)
	}
}

// puppetScripts returns the generated configure-user.py and its sh wrapper.
func puppetScripts(t *testing.T, cfg config.Config) (py, sh string) {
	t.Helper()
	out := generate(t, func(b *bytes.Buffer) error { return GeneratePuppet(b, cfg) })
	dedent := func(s string) string {
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimPrefix(l, "      ")
		}
		return strings.Join(lines, "\n")
	}
	py = dedent(extractBetween(t, out, "content => @(PYEOF),\n", "      | PYEOF"))
	sh = dedent(extractBetween(t, out, "content => @(SHEOF),\n", "      | SHEOF"))
	return py, sh
}

func TestPuppet_ScriptsCompile(t *testing.T) {
	py, sh := puppetScripts(t, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"})
	pyBin := needPython(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "configure-user.py")
	mustWrite(t, path, py, 0644)
	if b, err := exec.Command(pyBin, "-m", "py_compile", path).CombinedOutput(); err != nil {
		t.Fatalf("py_compile: %v\n%s", err, b)
	}
	cmd := exec.Command("/bin/sh", "-n")
	cmd.Stdin = strings.NewReader(sh)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, b)
	}
	if sc, err := exec.LookPath("shellcheck"); err == nil {
		cmd := exec.Command(sc, "-s", "sh", "-")
		cmd.Stdin = strings.NewReader(sh)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("shellcheck: %v\n%s", err, b)
		}
	}
}

type puppetRun struct {
	t      *testing.T
	py     string
	script string
	home   string
	env    []string
}

func newPuppetRun(t *testing.T, endpoint, key string) *puppetRun {
	t.Helper()
	pyBin := needPython(t)
	py, _ := puppetScripts(t, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"})
	dir := t.TempDir()
	script := filepath.Join(dir, "configure-user.py")
	mustWrite(t, script, py, 0644)
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	return &puppetRun{t: t, py: pyBin, script: script, home: home, env: []string{
		"HOME=" + home, "PATH=/usr/bin:/bin", "FS_ENDPOINT=" + endpoint, "FS_KEY=" + key, "FS_RC_FILES=.bashrc .zshrc",
	}}
}

// run runs the script (args, e.g. --check) and returns its exit code.
func (r *puppetRun) run(args ...string) int {
	r.t.Helper()
	cmd := exec.Command(r.py, append([]string{r.script}, args...)...)
	cmd.Env = r.env
	cmd.Dir = r.home
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	} else if err != nil {
		r.t.Fatalf("run: %v\n%s", err, b)
	}
	return 0
}

func (r *puppetRun) path(rel string) string { return filepath.Join(r.home, rel) }

// Puppet's user script follows symlinked dotfiles (the link stays a link),
// merges every settings file, tightens key files, single-quotes the endpoint
// in the profile, and is idempotent by content (--check stays true after the
// user appends lines below the block).
func TestPuppet_UserScript(t *testing.T) {
	r := newPuppetRun(t, "https://otel.flowstate.host", hostileKey)

	dotfiles := r.path("dotfiles")
	mustWrite(t, filepath.Join(dotfiles, "zshrc"), "export PATH=/x\n", 0644)
	mustWrite(t, filepath.Join(dotfiles, "claude.json"), `{"model": "opus"}`, 0644)
	for link, target := range map[string]string{".zshrc": "zshrc", ".claude/settings.json": "claude.json"} {
		if err := os.MkdirAll(filepath.Dir(r.path(link)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dotfiles, target), r.path(link)); err != nil {
			t.Fatal(err)
		}
	}
	vscode := ".config/Code/User"
	if runtime.GOOS == "darwin" {
		vscode = "Library/Application Support/Code/User"
	}
	mustWrite(t, r.path(vscode+"/settings.json"), `{"editor.fontSize": 14, "settingsSync.ignoredSettings": ["x"]}`, 0644)
	if err := os.MkdirAll(r.path(".gemini"), 0755); err != nil {
		t.Fatal(err)
	}

	if code := r.run("--check"); code != 1 {
		t.Fatalf("--check before = %d, want 1", code)
	}
	if code := r.run(); code != 0 {
		t.Fatalf("run = %d", code)
	}

	for _, link := range []string{".zshrc", ".claude/settings.json"} {
		if !isSymlink(t, r.path(link)) {
			t.Errorf("%s is no longer a symlink", link)
		}
	}
	zshrc := mustRead(t, filepath.Join(dotfiles, "zshrc"))
	if !strings.HasPrefix(zshrc, "export PATH=/x\n# BEGIN Flowstate Telemetry\nexport FLOWSTATE_OTLP_ENDPOINT='https://otel.flowstate.host'\n") {
		t.Errorf("profile block wrong:\n%s", zshrc)
	}
	if strings.Contains(zshrc, "pwned") {
		t.Error("the profile holds the key")
	}

	var claude struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(dotfiles, "claude.json"))), &claude); err != nil {
		t.Fatal(err)
	}
	if claude.Model != "opus" || claude.Env["OTEL_EXPORTER_OTLP_HEADERS"] != "x-flowstate-key="+hostileKey {
		t.Errorf("claude settings wrong: %+v", claude)
	}
	if m := mode(t, filepath.Join(dotfiles, "claude.json")); m != 0600 {
		t.Errorf("claude settings mode = %o, want 0600", m)
	}

	var vs map[string]interface{}
	if err := json.Unmarshal([]byte(mustRead(t, r.path(vscode+"/settings.json"))), &vs); err != nil {
		t.Fatal(err)
	}
	headers, _ := vs["github.copilot.chat.otel.headers"].(map[string]interface{})
	ignored, _ := vs["settingsSync.ignoredSettings"].([]interface{})
	if vs["editor.fontSize"] != float64(14) || headers["x-flowstate-key"] != hostileKey || len(ignored) != 2 {
		t.Errorf("VS Code settings wrong: %v", vs)
	}
	if m := mode(t, r.path(vscode+"/settings.json")); m != 0600 {
		t.Errorf("VS Code settings mode = %o, want 0600", m)
	}

	var gem struct {
		Telemetry map[string]interface{} `json:"telemetry"`
	}
	if err := json.Unmarshal([]byte(mustRead(t, r.path(".gemini/settings.json"))), &gem); err != nil {
		t.Fatal(err)
	}
	if gem.Telemetry["otlpProtocol"] != "http" || gem.Telemetry["logPrompts"] != false {
		t.Errorf("gemini settings wrong: %v", gem)
	}

	env := r.path(".config/flowstate-telemetry/env")
	if got := mustRead(t, env); got != "FLOWSTATE_OTLP_ENDPOINT=https://otel.flowstate.host\nFLOWSTATE_OTLP_KEY="+hostileKey+"\n" {
		t.Errorf("env file = %q", got)
	}
	if m := mode(t, env); m != 0600 {
		t.Errorf("env mode = %o", m)
	}

	if code := r.run("--check"); code != 0 {
		t.Fatalf("--check after run = %d, want 0", code)
	}
	// Lines the user adds after the block do not make it "changed" again.
	f, err := os.OpenFile(filepath.Join(dotfiles, "zshrc"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("alias ll='ls -l'\n")
	_ = f.Close()
	if code := r.run("--check"); code != 0 {
		t.Fatalf("--check after the user appended a line = %d, want 0", code)
	}
	// A new endpoint replaces the block where it stands.
	r.env[2] = "FS_ENDPOINT=https://other.example"
	if code := r.run(); code != 0 {
		t.Fatalf("rerun = %d", code)
	}
	if got := mustRead(t, filepath.Join(dotfiles, "zshrc")); !strings.HasSuffix(got, "# END Flowstate Telemetry\nalias ll='ls -l'\n") || !strings.Contains(got, "'https://other.example'") {
		t.Errorf("block not replaced in place:\n%s", got)
	}
}

// A symlink to a file the user does not own is left alone (warned), and the
// run neither fails nor reports it as pending forever.
func TestPuppet_UserScriptRefusesForeignSymlink(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root")
	}
	r := newPuppetRun(t, "https://otel.flowstate.host", "fs_key")
	if err := os.Symlink("/etc/hosts", r.path(".bashrc")); err != nil {
		t.Fatal(err)
	}
	if code := r.run(); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if code := r.run("--check"); code != 0 {
		t.Fatalf("--check = %d, want 0 (the foreign file is skipped)", code)
	}
	if !isSymlink(t, r.path(".bashrc")) {
		t.Fatal(".bashrc replaced")
	}
}

// A hostile endpoint (should validation ever be bypassed) is never executed
// when the profile is read: it is single-quoted.
func TestPuppet_ProfileEndpointIsLiteral(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	hostile := "https://x.example/$(touch pwned)`touch pwned2`'q"
	r := newPuppetRun(t, hostile, "fs_key")
	if code := r.run(); code != 0 {
		t.Fatalf("run = %d", code)
	}
	cmd := exec.Command(bash, "-c", `source ./.bashrc; printf %s "$FLOWSTATE_OTLP_ENDPOINT"`)
	cmd.Dir = r.home
	cmd.Env = r.env
	got, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != hostile {
		t.Errorf("endpoint = %q, want %q", got, hostile)
	}
	for _, f := range []string{"pwned", "pwned2"} {
		if _, err := os.Stat(r.path(f)); err == nil {
			t.Errorf("%s created: the endpoint was executed", f)
		}
	}
}
