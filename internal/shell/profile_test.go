package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
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
	sandbox(t)
	cfg := config.Config{
		Endpoint: "https://otel.flowstate.host",
		Key:      "fs_key",
		TeamID:   "team-1",
		Email:    "dev@example.com",
	}
	want := `# --- Flowstate Telemetry ---
export FLOWSTATE_OTLP_ENDPOINT='https://otel.flowstate.host'
# FLOWSTATE_OTLP_KEY is read from the env file (mode 0600), not stored here.
` + KeyLoader(hooks.EnvFilePath()) + `
# --- End Flowstate Telemetry ---`
	if got := buildEnvBlock(cfg); got != want {
		t.Fatalf("block mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(want, "fs_key") {
		t.Fatal("block embeds the key")
	}
}

// The key must never be exported as OTEL_EXPORTER_OTLP_HEADERS: every OTel
// SDK started from the shell would send it to its own collector.
func TestBuildEnvBlock_NoGlobalOTelHeaders(t *testing.T) {
	got := buildEnvBlock(config.Config{Endpoint: "https://otel.flowstate.host", Key: "k"})
	got += buildEnvBlock(config.Config{Endpoint: "https://otel.flowstate.host", Key: "k", TeamID: "t", Email: "e@x"})
	if strings.Contains(got, "OTEL_EXPORTER_OTLP_HEADERS") || strings.Contains(got, "OTEL_EXPORTER_OTLP_ENDPOINT") ||
		strings.Contains(got, "OTEL_RESOURCE_ATTRIBUTES") || strings.Contains(got, "e@x") {
		t.Fatalf("block exports generic OTel variables:\n%s", got)
	}
}

func TestWriteEnvVars_DryRunRedactsKey(t *testing.T) {
	profile := sandbox(t)
	out := captureStdout(t, func() {
		if err := WriteEnvVars(config.Config{Endpoint: "https://otel.flowstate.host", Key: "sekrit-key"}, true); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "sekrit-key") || !strings.Contains(out, markerStart) {
		t.Fatalf("dry-run output not redacted:\n%s", out)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote the profile")
	}
}

// Upgrading rewrites a block written by an older release (legacy endpoint,
// global OTEL_EXPORTER_OTLP_HEADERS).
func TestWriteEnvVars_ReplacesLegacyBlock(t *testing.T) {
	profile := sandbox(t)
	old := "export PATH=/x\n\n" + markerStart + "\nexport FLOWSTATE_OTLP_ENDPOINT=\"https://otel.flowstate.inc\"\nexport FLOWSTATE_OTLP_KEY=\"k\"\nexport OTEL_EXPORTER_OTLP_HEADERS=\"x-flowstate-key=k\"\nexport OTEL_RESOURCE_ATTRIBUTES=\"flowstate.team_id=t,flowstate.email=e@x\"\n" + markerEnd + "\n"
	if err := os.WriteFile(profile, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEnvVars(config.Config{Endpoint: config.DefaultEndpoint, Key: "k"}, false); err != nil {
		t.Fatal(err)
	}
	got := ProfileText()
	if config.ContainsLegacyEndpoint(got) || strings.Contains(got, "OTEL_EXPORTER_OTLP_HEADERS") || strings.Contains(got, "OTEL_RESOURCE_ATTRIBUTES") || !strings.Contains(MainBlock(), config.DefaultEndpoint) {
		t.Fatalf("legacy block survived:\n%s", got)
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
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	return <-done
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
	cfg := config.Config{Endpoint: "https://a.example", Key: "key-one-1234"}
	if err := WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	cfg.Key = "key-two-5678"
	if err := WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(profile)
	s := string(data)
	if !strings.HasPrefix(s, "export PATH=/x:$PATH\n") {
		t.Fatalf("existing content lost:\n%s", s)
	}
	if strings.Count(s, markerStart) != 1 || strings.Contains(s, "key-one") || strings.Contains(s, "key-two") {
		t.Fatalf("block not replaced, or the key is in the profile:\n%s", s)
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

	launchd := map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "x-flowstate-key=old"}
	oldGetenv := launchctlGetenv
	t.Cleanup(func() { launchctlGetenv = oldGetenv })
	launchctlGetenv = func(name string) string { return launchd[name] }

	cfg := config.Config{Endpoint: "https://otel.flowstate.host", Key: "k'ey"}
	if err := InstallGUIEnv(cfg, false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"launchctl unsetenv OTEL_EXPORTER_OTLP_HEADERS",
		"launchctl setenv FLOWSTATE_OTLP_ENDPOINT https://otel.flowstate.host",
		"launchctl setenv FLOWSTATE_OTLP_KEY k'ey",
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
	if strings.Contains(string(plist), "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Fatalf("plist publishes OTEL_EXPORTER_OTLP_HEADERS:\n%s", plist)
	}
	if st, _ := os.Stat(GUIEnvAgentPath()); st.Mode().Perm() != 0600 {
		t.Fatalf("plist mode = %v, want 0600", st.Mode().Perm())
	}
	// A plist an earlier release left group/world-readable is tightened.
	if err := os.Chmod(GUIEnvAgentPath(), 0644); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := InstallGUIEnv(cfg, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(GUIEnvAgentPath()); st.Mode().Perm() != 0600 {
		t.Fatalf("existing plist mode = %v, want 0600", st.Mode().Perm())
	}

	calls = nil
	if err := RemoveGUIEnv(); err != nil {
		t.Fatal(err)
	}
	if GUIEnvInstalled() || len(calls) != 3 {
		t.Fatalf("remove incomplete: installed=%v calls=%v", GUIEnvInstalled(), calls)
	}

	// A user's own OTEL_EXPORTER_OTLP_HEADERS (no Flowstate key) is never
	// unset, by install or by remove.
	launchd["OTEL_EXPORTER_OTLP_HEADERS"] = "authorization=Bearer theirs"
	calls = nil
	if err := InstallGUIEnv(cfg, false); err != nil {
		t.Fatal(err)
	}
	if err := RemoveGUIEnv(); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.Contains(c, "OTEL_EXPORTER_OTLP_HEADERS") {
			t.Fatalf("unset the user's own OTEL_EXPORTER_OTLP_HEADERS: %v", calls)
		}
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

// The profile block loads the key from the 0600 env file at shell start, in
// every shell the profile may be written for, whatever the key contains.
func TestKeyLoader_LoadsKeyFromEnvFile(t *testing.T) {
	sandbox(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	key := `fs_k'ey="with" $dollar=and=equals=`
	if err := hooks.WriteEnvFile(config.Config{Endpoint: "https://otel.flowstate.host", Key: key}); err != nil {
		t.Fatal(err)
	}
	if err := WriteEnvVars(config.Config{Endpoint: "https://otel.flowstate.host", Key: key}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ProfileText(), key) || strings.Contains(ProfileText(), "fs_k") {
		t.Fatalf("profile embeds the key:\n%s", ProfileText())
	}
	if st, _ := os.Stat(hooks.EnvFilePath()); st.Mode().Perm() != 0600 {
		t.Fatalf("env file mode = %v, want 0600", st.Mode().Perm())
	}
	for _, sh := range []string{"sh", "bash", "zsh"} {
		bin, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		for name, loader := range map[string]string{"profile": ". " + ProfilePath(), "default path": KeyLoader("")} {
			cmd := exec.Command(bin, "-c", "unset FLOWSTATE_OTLP_KEY; "+loader+`; printf '%s|%s' "$FLOWSTATE_OTLP_KEY" "${_fs_env-unset}"`)
			cmd.Env = append(os.Environ(), "FLOWSTATE_OTLP_KEY=")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s (%s): %v\n%s", sh, name, err, out)
			}
			if string(out) != key+"|unset" {
				t.Errorf("%s (%s) loaded %q, want %q (and helper variables unset)", sh, name, out, key+"|unset")
			}
		}
	}

	// A missing env file leaves the variable unset and the shell working.
	if err := hooks.RemoveEnvFile(); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-c", ". "+ProfilePath()+`; printf '%s' "${FLOWSTATE_OTLP_KEY-unset}"`).CombinedOutput()
	if err != nil || string(out) != "unset" {
		t.Fatalf("missing env file: %v %q", err, out)
	}
}

// Re-installing without a key keeps the key already in the env file.
func TestWriteEnvFile_KeepsKeyWhenNoneGiven(t *testing.T) {
	sandbox(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := hooks.WriteEnvFile(config.Config{Endpoint: "https://a.example", Key: "kept"}); err != nil {
		t.Fatal(err)
	}
	if err := hooks.WriteEnvFile(config.Config{Endpoint: "https://b.example"}); err != nil {
		t.Fatal(err)
	}
	if got := hooks.EnvFileKey(); got != "kept" {
		t.Fatalf("key = %q, want kept", got)
	}
}

// Removing a block never joins the user's neighbouring lines, wherever the
// block sits and whatever blank lines surround it.
func TestRemoveExistingBlock(t *testing.T) {
	b := markerStart + "\nexport FLOWSTATE_OTLP_ENDPOINT=x\n" + markerEnd
	cases := []struct{ name, in, want string }{
		{"only block", b + "\n", ""},
		{"only block, no final newline", b, ""},
		{"only block after blank line", "\n" + b + "\n", ""},
		{"start, no blank", b + "\nalias ll=ls\n", "alias ll=ls\n"},
		{"start, blank after", b + "\n\nalias ll=ls\n", "\nalias ll=ls\n"},
		{"middle, no blank lines", "export FOO=1\n" + b + "\nalias ll=ls\n", "export FOO=1\nalias ll=ls\n"},
		{"middle, blank before", "export FOO=1\n\n" + b + "\nalias ll=ls\n", "export FOO=1\nalias ll=ls\n"},
		{"middle, blank after", "export FOO=1\n" + b + "\n\nalias ll=ls\n", "export FOO=1\n\nalias ll=ls\n"},
		{"middle, blank both sides", "export FOO=1\n\n" + b + "\n\nalias ll=ls\n", "export FOO=1\n\nalias ll=ls\n"},
		{"middle, two blanks before", "export FOO=1\n\n\n" + b + "\nalias ll=ls\n", "export FOO=1\n\nalias ll=ls\n"},
		{"end, no blank", "export FOO=1\n" + b + "\n", "export FOO=1\n"},
		{"end, no final newline", "export FOO=1\n" + b, "export FOO=1\n"},
		{"end, blank before", "export FOO=1\n\n" + b + "\n", "export FOO=1\n"},
		{"absent", "export FOO=1\n", "export FOO=1\n"},
		{"unterminated", "export FOO=1\n" + markerStart + "\nx\n", "export FOO=1\n" + markerStart + "\nx\n"},
	}
	for _, c := range cases {
		if got := removeExistingBlock(c.in, markerStart, markerEnd); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// Installing again and again leaves the profile unchanged (from the second
// install on: the first may reorder blocks), and removing restores the
// user's lines exactly.
func TestWriteEnvVars_RepeatedInstallsAreStable(t *testing.T) {
	for name, user := range map[string]string{
		"empty":            "",
		"no final newline": "export FOO=1\nalias ll=ls",
		"plain":            "export FOO=1\nalias ll=ls\n",
		"trailing blank":   "export FOO=1\nalias ll=ls\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			profile := sandbox(t)
			if err := os.WriteFile(profile, []byte(user), 0644); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Endpoint: "https://otel.flowstate.host", Key: "k"}
			var first string
			for i := 0; i < 4; i++ {
				if err := WriteEnvVars(cfg, false); err != nil {
					t.Fatal(err)
				}
				if err := WriteToolBlock("gemini-cli", []string{"function gemini { command gemini \"$@\"; }"}, false); err != nil {
					t.Fatal(err)
				}
				if i <= 1 {
					first = ProfileText()
				} else if got := ProfileText(); got != first {
					t.Fatalf("install %d changed the profile:\n%q\nwant\n%q", i+1, got, first)
				}
			}
			assertUserLines(t, ProfileText(), user)
			if err := RemoveEnvVars(); err != nil {
				t.Fatal(err)
			}
			want := user
			if want != "" && !strings.HasSuffix(want, "\n") {
				want += "\n"
			}
			if got := ProfileText(); got != want {
				t.Fatalf("after remove:\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// Upgrading a profile written by an older release, with several blocks
// packed between the user's lines (no blank lines), rewrites every block
// and keeps every user line whole.
func TestWriteEnvVars_UpgradeRewritesSeveralBlocks(t *testing.T) {
	profile := sandbox(t)
	gStart, gEnd := toolMarkers("gemini-cli")
	oStart, oEnd := toolMarkers("opencode")
	user := "export FOO=1\nalias ll=ls\nexport BAR=2\nsource ~/.secrets\n"
	old := "export FOO=1\n" +
		markerStart + "\nexport FLOWSTATE_OTLP_ENDPOINT=\"https://otel.flowstate.inc\"\nexport FLOWSTATE_OTLP_KEY=\"oldkey123\"\nexport OTEL_EXPORTER_OTLP_HEADERS=\"x-flowstate-key=oldkey123\"\n" + markerEnd + "\n" +
		"alias ll=ls\n" +
		gStart + "\ngemini() { command gemini \"$@\"; }\n" + gEnd + "\n" +
		"export BAR=2\n" +
		oStart + "\nopencode() { command opencode \"$@\"; }\n" + oEnd + "\n" +
		"source ~/.secrets"
	if err := os.WriteFile(profile, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Endpoint: config.DefaultEndpoint, Key: "new"}
	if err := WriteEnvVars(cfg, false); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"gemini-cli", "opencode"} {
		if err := WriteToolBlock(tool, []string{"function " + tool + " { command " + tool + " \"$@\"; }"}, false); err != nil {
			t.Fatal(err)
		}
	}
	got := ProfileText()
	assertUserLines(t, got, user)
	for _, m := range []string{markerStart, gStart, oStart} {
		if strings.Count(got, m) != 1 {
			t.Errorf("%q appears %d times:\n%s", m, strings.Count(got, m), got)
		}
	}
	if strings.Contains(got, "otel.flowstate.inc") || strings.Contains(got, "oldkey123") || strings.Contains(got, "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Fatalf("legacy content survived:\n%s", got)
	}
	if err := RemoveEnvVars(); err != nil {
		t.Fatal(err)
	}
	if got := ProfileText(); got != user {
		t.Fatalf("after remove:\n%q\nwant\n%q", got, user)
	}
}

// assertUserLines checks that every non-empty line of user appears, whole
// and in order, as its own line of got.
func assertUserLines(t *testing.T, got, user string) {
	t.Helper()
	lines := strings.Split(got, "\n")
	i := 0
	for _, want := range strings.Split(user, "\n") {
		if want == "" {
			continue
		}
		for i < len(lines) && lines[i] != want {
			i++
		}
		if i == len(lines) {
			t.Fatalf("user line %q missing or joined to another line:\n%s", want, got)
		}
		i++
	}
}

// The endpoint is single-quoted in the profile: should a hostile value ever
// get past validation, $(...) and backticks in it are never run at shell
// start.
func TestBuildEnvBlock_EndpointIsLiteral(t *testing.T) {
	home := filepath.Dir(sandbox(t))
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	hostile := "https://x.example/$(touch pwned)`touch pwned2`'q\"z"
	block := buildEnvBlock(config.Config{Endpoint: hostile})
	cmd := exec.Command(bash, "-c", block+"\nprintf %s \"$FLOWSTATE_OTLP_ENDPOINT\"")
	cmd.Dir = home
	got, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != hostile {
		t.Errorf("endpoint = %q, want %q", got, hostile)
	}
	for _, f := range []string{"pwned", "pwned2"} {
		if _, err := os.Stat(filepath.Join(home, f)); err == nil {
			t.Errorf("%s created: the endpoint was executed", f)
		}
	}
}
