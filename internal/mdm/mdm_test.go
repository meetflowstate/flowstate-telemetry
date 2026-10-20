package mdm

import (
	"bytes"
	"io"
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
			if strings.Contains(out, "otlpProtocol") && !strings.Contains(out, `"otlpProtocol": "http"`) {
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
		`"OTEL_EXPORTER_OTLP_HEADERS": "x-flowstate-key=`,            // Claude Code settings.json env block
		`'OTEL_EXPORTER_OTLP_HEADERS': 'x-flowstate-key=`,            // the same, merged by Python (Jamf)
		`"OTEL_EXPORTER_OTLP_HEADERS" = "x-flowstate-key=`,           // the same, PowerShell hashtable (Intune)
		`command gemini "$@"`,                                        // wrapper function scoped to one command
		`SetEnvironmentVariable("OTEL_EXPORTER_OTLP_HEADERS", $null`, // Intune clean-up
		`GetEnvironmentVariable("OTEL_EXPORTER_OTLP_HEADERS"`,
		`for VAR in OTEL_EXPORTER_OTLP_HEADERS`, // Jamf clean-up (unsetenv)
		`- OTEL_EXPORTER_OTLP_HEADERS`,          // Ansible clean-up loop (state: absent)
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

// Jamf writes the hook env file per user, readable by that user only.
func TestJamf_PerUserEnvFile(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateJamf(&buf, config.Config{Endpoint: "https://otel.flowstate.host", Key: "fs_key"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`(umask 077; printf`, `"$CONF_DIR/env"`, `chmod 700 "$CONF_DIR"`, `rm -f /etc/flowstate-telemetry.env`, `KEY="${4:-fs_key}"`} {
		if !strings.Contains(out, want) {
			t.Errorf("jamf output missing %q", want)
		}
	}
}
