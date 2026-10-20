package mdm

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"gopkg.in/yaml.v3"
)

// Every MDM artefact must follow the OTLP/HTTP receiver contract: no gRPC,
// no forced service.name, delta temporality for Claude Code, and the key
// sent as x-flowstate-key.
func TestTemplates_FollowHTTPContract(t *testing.T) {
	cfg := config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}
	generators := map[string]func(io.Writer, config.Config) error{
		"jamf": GenerateJamf, "intune": GenerateIntune, "ansible": GenerateAnsible, "puppet": GeneratePuppet,
	}
	for name, gen := range generators {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := gen(&buf, cfg); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, banned := range []string{"grpc", "OTEL_METRICS_TEMPORALITY_PREFERENCE\": \"cumulative", "=\"service.name=", "\"service.name=flowstate-telemetry\" }"} {
				if strings.Contains(out, banned) {
					t.Errorf("%s output contains %q", name, banned)
				}
			}
			for _, want := range []string{
				"https://otel.flowstate.host",
				"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
				"x-flowstate-key",
				"http/protobuf",
				"ONE Flowstate key", // shared-key attribution warning
				"per-user",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s output missing %q", name, want)
				}
			}
			if strings.Contains(out, "github.copilot.chat.otel.exporterType") && !strings.Contains(out, "otlp-http") {
				t.Errorf("%s configures Copilot Chat without otlp-http", name)
			}
			if strings.Contains(out, "otlpProtocol") && !strings.Contains(out, `"otlpProtocol": "http"`) && !strings.Contains(out, `'otlpProtocol': 'http'`) {
				t.Errorf("%s configures Gemini without otlpProtocol http", name)
			}
			if strings.Contains(out, "github.copilot.chat.otel.headers") && !strings.Contains(out, "settingsSync.ignoredSettings") {
				t.Errorf("%s lets Settings Sync upload the Copilot Chat headers setting", name)
			}
		})
	}
}

// The key may reach OpenTelemetry only through a tool's own settings or a
// wrapper scoped to one command: never a machine-wide, session-wide or
// shell-wide OTEL_EXPORTER_OTLP_HEADERS that every OTel SDK would send to
// its own collector.
func TestTemplates_NoGlobalOTelHeaders(t *testing.T) {
	cfg := config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}
	generators := map[string]func(io.Writer, config.Config) error{
		"jamf": GenerateJamf, "intune": GenerateIntune, "ansible": GenerateAnsible, "puppet": GeneratePuppet,
	}
	allowed := []string{
		`"OTEL_EXPORTER_OTLP_HEADERS": "x-flowstate-key=`,                             // Claude Code settings.json env block
		`'OTEL_EXPORTER_OTLP_HEADERS': 'x-flowstate-key=`,                             // the same, merged by Python (Jamf, Puppet)
		`"OTEL_EXPORTER_OTLP_HEADERS": {{ ('x-flowstate-key=' ~ fs_key) | to_json }}`, // the same, Ansible
		`"OTEL_EXPORTER_OTLP_HEADERS" = "x-flowstate-key=`,                            // the same, PowerShell hashtable (Intune)
		`command gemini "$@"`, // wrapper function scoped to one command
		`SetEnvironmentVariable("OTEL_EXPORTER_OTLP_HEADERS", $null`, // Intune clean-up
		`GetEnvironmentVariable("OTEL_EXPORTER_OTLP_HEADERS"`,
		`Remove-MachineVariable "OTEL_EXPORTER_OTLP_HEADERS"`,
		`launchctl getenv OTEL_EXPORTER_OTLP_HEADERS`,   // Jamf clean-up: read,
		`launchctl unsetenv OTEL_EXPORTER_OTLP_HEADERS`, // then unset only when it carries the key
		`- OTEL_EXPORTER_OTLP_HEADERS`,                  // Ansible clean-up loop (state: absent)
	}
	for name, gen := range generators {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := gen(&buf, cfg); err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(buf.String(), "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.Contains(line, "OTEL_EXPORTER_OTLP_HEADERS") || strings.HasPrefix(trimmed, "#") {
					continue
				}
				ok := false
				for _, a := range allowed {
					if strings.Contains(line, a) {
						ok = true
						break
					}
				}
				if !ok {
					t.Errorf("%s sets OTEL_EXPORTER_OTLP_HEADERS outside a tool's own settings: %s", name, trimmed)
				}
			}
			for _, banned := range []string{
				"launchctl setenv", "> /etc/flowstate-telemetry.env", "chmod 644 /etc/flowstate-telemetry.env",
				"/etc/flowstate-telemetry.env;", `"FLOWSTATE_OTLP_KEY", $Key, "Machine"`,
			} {
				if strings.Contains(buf.String(), banned) {
					t.Errorf("%s output contains %q", name, banned)
				}
			}
		})
	}
}

// Flowstate team/e-mail must never be exported as OTEL_RESOURCE_ATTRIBUTES
// machine- or shell-wide; the templates may only clean up old values.
func TestTemplates_NoGlobalResourceAttributes(t *testing.T) {
	cfg := config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key", TeamID: "team-1", Email: "dev@example.com"}
	generators := map[string]func(io.Writer, config.Config) error{
		"jamf": GenerateJamf, "intune": GenerateIntune, "ansible": GenerateAnsible, "puppet": GeneratePuppet,
	}
	for name, gen := range generators {
		var buf bytes.Buffer
		if err := gen(&buf, cfg); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(buf.String(), "\n") {
			if !strings.Contains(line, "OTEL_RESOURCE_ATTRIBUTES") || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if !strings.Contains(line, "$null") && !strings.Contains(line, "GetEnvironmentVariable") && !strings.Contains(line, "regexp:") {
				t.Errorf("%s sets OTEL_RESOURCE_ATTRIBUTES: %s", name, strings.TrimSpace(line))
			}
		}
		if strings.Contains(buf.String(), "dev@example.com") || strings.Contains(buf.String(), "flowstate.team_id") {
			t.Errorf("%s embeds the team/e-mail attributes", name)
		}
	}
}

func TestAnsible_IsValidYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateAnsible(&buf, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}); err != nil {
		t.Fatal(err)
	}
	var doc []map[string]interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("ansible output is not YAML: %v", err)
	}
	if len(doc) != 1 || doc[0]["tasks"] == nil {
		t.Fatalf("unexpected playbook shape: %v", doc)
	}
}

// Jamf writes every file under a home directory as that user (never as
// root, so a planted symlink cannot redirect a root write), the hook env
// file readable by that user only, and never puts the key on a command line.
func TestJamf_PerUserWritesAsUser(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateJamf(&buf, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`sudo -u "$USERNAME" -H /bin/bash -c "$(declare -f configure_user)`,
		`printf '%s\n%s\n' "$ENDPOINT" "$KEY" |`, // key on stdin
		`FS_ENDPOINT="$endpoint" FS_KEY="$key" "$python" -c "$merge" "$settings"`,
		`umask 077`, `chmod 700 "$dir"`, `tmp=$(mktemp "$dir/.env.XXXXXX")`, `mv -f "$tmp" "$dir/env"`,
		`[ ! -L "$HOMEDIR" ]`, `rm -f /etc/flowstate-telemetry.env`,
		`DEFAULT_KEY='fs_key'`, `KEY="${4:-$DEFAULT_KEY}"`,
		`os.chmod(tmp, mode)`, `os.replace(tmp, real)`, `real = os.path.realpath(path)`,
		`*x-flowstate-key*)`, `*[[:cntrl:]]*)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("jamf output missing %q", want)
		}
	}
	for _, banned := range []string{`chown "`, "chown -R", "'$KEY'", "'$ENDPOINT'", "'$SETTINGS'", "x-flowstate-key=$KEY"} {
		if strings.Contains(out, banned) {
			t.Errorf("jamf output contains %q", banned)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "python") && strings.Contains(line, " -c ") && strings.Contains(strings.ToLower(line), "key\"") && !strings.Contains(line, "FS_KEY=") {
			t.Errorf("key passed to python on the command line: %s", line)
		}
	}
	checkShell(t, "jamf", out)
}

// A key or endpoint with shell metacharacters is embedded literally.
func TestJamf_QuotesKey(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateJamf(&buf, config.Config{Endpoint: "https://otel.flowstate.host", Key: `k'e"y$x`}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `DEFAULT_KEY='k'\''e"y$x'`) {
		t.Fatalf("key not single-quoted:\n%s", buf.String())
	}
	checkShell(t, "jamf", buf.String())
}

// Intune runs as the logged-on user: Machine-scope clean-up must not abort
// the script (it would never configure the user's tools), and a separate
// SYSTEM script removes the machine-wide values.
func TestIntune_MachineCleanupIsBestEffort(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateIntune(&buf, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(buf.String(), "\n")
	for i, line := range lines {
		if strings.Contains(line, "SetEnvironmentVariable(") && strings.Contains(line, `"Machine")`) {
			inTry := strings.Contains(line, "try {") || (i > 0 && strings.TrimSpace(lines[i-1]) == "try {")
			if !inTry {
				t.Errorf("Machine-scope write outside try/catch: %s", strings.TrimSpace(line))
			}
		}
	}
	for _, want := range []string{"--machine-cleanup", "Write-Warning", "$script:MachineLeft"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("intune output missing %q", want)
		}
	}

	buf.Reset()
	if err := GenerateIntuneMachineCleanup(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"FLOWSTATE_OTLP_ENDPOINT", "FLOWSTATE_OTLP_KEY"`,
		`$headers.Contains("x-flowstate-key")`,
		`-eq "service.name=flowstate-telemetry"`,
		"WindowsBuiltInRole]::Administrator",
		"exit 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("machine clean-up missing %q", want)
		}
	}
	if strings.Contains(out, "fs_key") || strings.Contains(out, "$Key") {
		t.Error("machine clean-up embeds a key")
	}
}

// Ansible checks for Gemini on the target (not the controller), writes the
// profile block to ~/.zshrc on macOS too, keeps the key out of every profile
// and creates key-holding files 0600.
func TestAnsible_ProfileAndKeyFiles(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateAnsible(&buf, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}); err != nil {
		t.Fatal(err)
	}
	var doc []map[string]interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	tasks, _ := doc[0]["tasks"].([]interface{})
	var block, loop string
	modes := map[string]string{}
	for _, raw := range tasks {
		task, _ := raw.(map[string]interface{})
		if b, ok := task["blockinfile"].(map[string]interface{}); ok {
			block, _ = b["block"].(string)
			loop, _ = task["loop"].(string)
		}
		if c, ok := task["copy"].(map[string]interface{}); ok {
			dest, _ := c["dest"].(string)
			mode, _ := c["mode"].(string)
			modes[dest[strings.LastIndex(dest, "}}")+2:]] = mode
		}
	}
	if strings.Contains(block, "flowstate_key") || !strings.Contains(block, "FLOWSTATE_OTLP_KEY=*)") || !strings.Contains(block, `command gemini "$@"`) {
		t.Errorf("profile block wrong:\n%s", block)
	}
	if !strings.Contains(loop, ".zshrc") || !strings.Contains(loop, "Darwin") {
		t.Errorf("profile block not written to ~/.zshrc on macOS: %s", loop)
	}
	for _, f := range []string{"/.config/flowstate-telemetry/env", "/.claude/settings.json", "/settings.json"} {
		if modes[f] != "0600" {
			t.Errorf("%s mode = %q, want 0600", f, modes[f])
		}
	}
	if strings.Contains(buf.String(), "lookup('pipe'") || !strings.Contains(buf.String(), "command -v gemini") {
		t.Error("gemini check must run on the target")
	}
	if !strings.Contains(buf.String(), "/Library/Application Support/Code/User' if ansible_system == 'Darwin'") {
		t.Error("VS Code settings path is not chosen per OS")
	}
	checkShell(t, "ansible profile block", strings.ReplaceAll(block, "{{ fs_endpoint | quote }}", "https://otel.flowstate.host"))
}

// Puppet merges Claude Code's settings (no whole-file rewrite), writes the
// user's files as the user with the key in the environment, keeps the key
// out of the profile, and covers ~/.zshrc on macOS.
func TestPuppet_MergesAndWritesAsUser(t *testing.T) {
	var buf bytes.Buffer
	if err := GeneratePuppet(&buf, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`user        => $username`,
		`"FS_KEY=${key}"`,
		`'Darwin' => '.bashrc .zshrc'`,
		`env.update({`,
		`merge_json(`,
		`--check`,
		`onlyif      => '/opt/flowstate-telemetry/configure-user --has-python'`,
		`/usr/bin/xcode-select -p`,
		`String $key      = 'fs_key'`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("puppet output missing %q", want)
		}
	}
	for _, banned := range []string{
		`"${homedir}/.claude/settings.json"`, "file_line", `FLOWSTATE_OTLP_KEY=\"${key}\"`,
		// No root-run file resources under a home directory.
		`file { "${vscode_dir}`, `file { "${homedir}`, "replace => false", "/usr/bin/env python3",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("puppet output contains %q", banned)
		}
	}
}

// checkShell runs bash -n (and shellcheck, when installed) on a script.
func checkShell(t *testing.T, name, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: bash -n: %v\n%s", name, err, out)
	}
	if sc, err := exec.LookPath("shellcheck"); err == nil {
		cmd := exec.Command(sc, "-s", "bash", "-")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: shellcheck: %v\n%s", name, err, out)
		}
	}
}
