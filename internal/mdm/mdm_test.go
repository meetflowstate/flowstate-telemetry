package mdm

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
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
				"OTEL_EXPORTER_OTLP_HEADERS",
				"x-flowstate-key",
				"http/protobuf",
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
		})
	}
}
