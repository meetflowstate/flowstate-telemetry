package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
)

// CopilotCLI configures telemetry for the GitHub Copilot CLI (`copilot`).
//
// The CLI is configured only through environment variables, and the generic
// OTEL_EXPORTER_OTLP_ENDPOINT it reads would also redirect (and, in VS Code
// Copilot Chat, switch on) every other OpenTelemetry SDK started from the
// shell. So the variables are scoped to a `copilot` wrapper function in the
// shell profile instead of being exported. The CLI exports traces and
// metrics (no logs) over OTLP/HTTP.
type CopilotCLI struct{}

func (c *CopilotCLI) Name() string { return "copilot-cli" }

func (c *CopilotCLI) Detect() bool {
	_, err := exec.LookPath("copilot")
	return err == nil
}

// copilotCLIBlock renders the profile lines. The endpoint and key come from
// the shared Flowstate block, so rotating the key updates the wrapper too.
func copilotCLIBlock(cfg config.Config) []string {
	assignments := []string{
		"COPILOT_OTEL_ENABLED=true",
		fmt.Sprintf(`OTEL_EXPORTER_OTLP_ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-%s}"`, cfg.BaseEndpoint()),
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		fmt.Sprintf(`OTEL_EXPORTER_OTLP_HEADERS="%s=${FLOWSTATE_OTLP_KEY:-}"`, config.AuthHeader),
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta",
	}
	if cfg.Prompts {
		assignments = append(assignments, "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true")
	}
	return []string{
		"# Scopes OpenTelemetry settings to the Copilot CLI only.",
		shell.WrapperFunction("copilot", assignments),
	}
}

func (c *CopilotCLI) Install(cfg config.Config) error {
	return shell.WriteToolBlock(c.Name(), copilotCLIBlock(cfg), cfg.DryRun)
}

func (c *CopilotCLI) Remove() error {
	return shell.RemoveToolBlock(c.Name())
}

func (c *CopilotCLI) Status() ToolStatus {
	st := ToolStatus{Detected: c.Detect()}
	if !st.Detected {
		return st
	}
	st.Configured = shell.HasToolBlock(c.Name())
	if st.Configured {
		data, _ := os.ReadFile(shell.ProfilePath())
		st.Prompts = strings.Contains(string(data), "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true")
	}
	return st
}

func (c *CopilotCLI) Verify(cfg config.Config) VerifyResult {
	if !c.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}
	if !shell.HasToolBlock(c.Name()) {
		return VerifyResult{OK: false, Message: fmt.Sprintf("copilot wrapper missing from %s", shell.ProfilePath())}
	}
	result := VerifyResult{
		OK:      true,
		Message: fmt.Sprintf("copilot wrapper in %s · OTLP/HTTP protobuf traces+metrics", shell.ProfilePath()),
		Notes: []string{
			"only `copilot` started from a shell that reads " + shell.ProfilePath() + " is covered (open a new terminal after install)",
		},
	}
	block := shell.ToolBlock(c.Name())
	var warnings []string
	if !strings.Contains(block, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta") {
		warnings = append(warnings, "metrics temporality is not delta (re-run install)")
	}
	if strings.Contains(block, "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true") {
		warnings = append(warnings, promptsOnWarning("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"))
	}
	result.Warning = joinWarnings(warnings)
	return result
}
