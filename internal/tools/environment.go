package tools

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
)

// systemEnvFiles are machine-wide files earlier releases' MDM templates wrote
// the key (and a global OTEL_EXPORTER_OTLP_HEADERS) to. Swapped out by tests.
var systemEnvFiles = []string{
	"/etc/environment",
	"/etc/profile.d/flowstate-telemetry.sh",
	"/etc/flowstate-telemetry.env",
}

// VerifyEnvironment checks what is shared by every tool: the process
// environment, the shell profile block, the hook env file, the --gui-env
// LaunchAgent and machine-wide files. It fails on the retired
// otel.flowstate.inc endpoint, and when the key (OTEL_EXPORTER_OTLP_HEADERS)
// or the Flowstate team/e-mail (OTEL_RESOURCE_ATTRIBUTES) is exported
// globally, where every OpenTelemetry SDK would send it to its own collector.
func VerifyEnvironment(cfg config.Config) VerifyResult {
	var errs []string
	reinstall := "re-run `flowstate-telemetry install` and open a new shell"

	for _, name := range []string{"FLOWSTATE_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if v := os.Getenv(name); config.IsLegacyEndpoint(v) {
			errs = append(errs, fmt.Sprintf("%s=%s is the retired receiver (%s)", name, v, reinstall))
		}
	}
	if strings.Contains(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"), config.AuthHeader) {
		errs = append(errs, "OTEL_EXPORTER_OTLP_HEADERS carrying x-flowstate-key is exported in this shell: every OpenTelemetry SDK sends it to its own collector ("+reinstall+"; check machine-wide files too)")
	}
	if isFlowstateResourceAttrs(os.Getenv("OTEL_RESOURCE_ATTRIBUTES")) {
		errs = append(errs, "OTEL_RESOURCE_ATTRIBUTES carrying flowstate.team_id/flowstate.email is exported in this shell: every OpenTelemetry SDK sends it to its own collector ("+reinstall+"; check machine-wide files too)")
	}

	var warnings []string
	if block := shell.MainBlock(); block != "" {
		if regexp.MustCompile(`(?m)^\s*export FLOWSTATE_OTLP_KEY=`).MatchString(block) {
			warnings = append(warnings, fmt.Sprintf("%s stores the key in plain text (re-run install: it moves to %s, mode 0600)", shell.ProfilePath(), hooks.EnvFilePath()))
		} else if _, err := os.Stat(hooks.EnvFilePath()); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s missing: shells get no FLOWSTATE_OTLP_KEY, so the wrappers send no key (re-run install with --key)", hooks.EnvFilePath()))
		}
		if config.ContainsLegacyEndpoint(block) {
			errs = append(errs, fmt.Sprintf("%s exports the retired otel.flowstate.inc endpoint (%s)", shell.ProfilePath(), reinstall))
		}
		for _, name := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_RESOURCE_ATTRIBUTES"} {
			if strings.Contains(block, name) {
				errs = append(errs, fmt.Sprintf("%s exports %s globally (%s)", shell.ProfilePath(), name, reinstall))
			}
		}
	}

	if data, err := os.ReadFile(hooks.EnvFilePath()); err == nil && config.ContainsLegacyEndpoint(string(data)) {
		errs = append(errs, fmt.Sprintf("%s points at the retired otel.flowstate.inc receiver (%s)", hooks.EnvFilePath(), reinstall))
	}

	if plist := shell.GUIEnvAgentText(); plist != "" {
		if config.ContainsLegacyEndpoint(plist) {
			errs = append(errs, fmt.Sprintf("%s sets the retired otel.flowstate.inc endpoint (%s)", shell.GUIEnvAgentPath(), reinstall))
		}
		for _, name := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_RESOURCE_ATTRIBUTES"} {
			if strings.Contains(plist, name) {
				errs = append(errs, fmt.Sprintf("%s publishes %s to every GUI app (%s)", shell.GUIEnvAgentPath(), name, reinstall))
			}
		}
	}

	for _, path := range systemEnvFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(data)
		if config.ContainsLegacyEndpoint(text) {
			errs = append(errs, fmt.Sprintf("%s sets the retired otel.flowstate.inc endpoint (left by an older MDM deployment; redeploy with the current generate-mdm output)", path))
		}
		if strings.Contains(text, "OTEL_EXPORTER_OTLP_HEADERS") && strings.Contains(text, config.AuthHeader) {
			errs = append(errs, fmt.Sprintf("%s sets OTEL_EXPORTER_OTLP_HEADERS with the Flowstate key for every process (left by an older MDM deployment; redeploy with the current generate-mdm output)", path))
		}
		if strings.Contains(text, "OTEL_RESOURCE_ATTRIBUTES") && isFlowstateResourceAttrs(text) {
			errs = append(errs, fmt.Sprintf("%s sets OTEL_RESOURCE_ATTRIBUTES with Flowstate team/e-mail for every process (remove it; tools get them scoped)", path))
		}
	}

	if len(errs) > 0 {
		return VerifyResult{OK: false, Message: strings.Join(errs, "; "), Warning: joinWarnings(warnings)}
	}
	return VerifyResult{OK: true, Message: fmt.Sprintf("endpoint %s · key and resource attributes not exported to other OpenTelemetry SDKs", cfg.BaseEndpoint()),
		Warning: joinWarnings(warnings)}
}
