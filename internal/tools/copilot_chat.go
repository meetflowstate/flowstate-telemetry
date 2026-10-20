package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
)

// CopilotChat configures telemetry for GitHub Copilot Chat in VS Code.
//
// exporterType "otlp-http" makes the extension append /v1/{traces,logs,
// metrics} to otlpEndpoint. The key is written to the
// `github.copilot.chat.otel.headers` setting (VS Code 1.138+) and is also
// exported as OTEL_EXPORTER_OTLP_HEADERS by the shell profile block, which
// older builds need. VS Code started from the Dock never reads the shell
// profile; `install --gui-env` publishes the variables to launchd for that.
type CopilotChat struct{}

// copilotChatKeys are the settings Flowstate owns.
var copilotChatKeys = []string{
	"github.copilot.chat.otel.enabled",
	"github.copilot.chat.otel.exporterType",
	"github.copilot.chat.otel.otlpEndpoint",
	"github.copilot.chat.otel.headers",
	"github.copilot.chat.otel.captureContent",
}

func (c *CopilotChat) Name() string { return "copilot-chat" }

func (c *CopilotChat) vsCodeSettingsDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User")
	case "linux":
		return filepath.Join(home, ".config", "Code", "User")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Code", "User")
	default:
		return filepath.Join(home, ".config", "Code", "User")
	}
}

func (c *CopilotChat) settingsPath() string {
	return filepath.Join(c.vsCodeSettingsDir(), "settings.json")
}

func (c *CopilotChat) Detect() bool {
	_, err := os.Stat(c.vsCodeSettingsDir())
	return err == nil
}

func (c *CopilotChat) readSettings() (map[string]interface{}, error) {
	data, err := os.ReadFile(c.settingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("reading settings: %w", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parsing settings: %w", err)
	}
	return settings, nil
}

func (c *CopilotChat) writeSettings(settings map[string]interface{}) error {
	dir := filepath.Dir(c.settingsPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}
	return os.WriteFile(c.settingsPath(), data, 0644)
}

func (c *CopilotChat) Install(cfg config.Config) error {
	settings, err := c.readSettings()
	if err != nil {
		return err
	}

	settings["github.copilot.chat.otel.enabled"] = true
	settings["github.copilot.chat.otel.exporterType"] = "otlp-http"
	settings["github.copilot.chat.otel.otlpEndpoint"] = cfg.BaseEndpoint()
	settings["github.copilot.chat.otel.captureContent"] = cfg.Prompts
	if cfg.Key != "" {
		settings["github.copilot.chat.otel.headers"] = map[string]interface{}{config.AuthHeader: cfg.Key}
	} else {
		delete(settings, "github.copilot.chat.otel.headers")
	}

	if cfg.DryRun {
		data, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", c.settingsPath(), redactKey(string(data), cfg.Key))
		return nil
	}

	return c.writeSettings(settings)
}

func (c *CopilotChat) Remove() error {
	settings, err := c.readSettings()
	if err != nil {
		return err
	}

	for _, k := range copilotChatKeys {
		delete(settings, k)
	}

	return c.writeSettings(settings)
}

func (c *CopilotChat) Status() ToolStatus {
	st := ToolStatus{Detected: c.Detect()}
	if !st.Detected {
		return st
	}

	settings, err := c.readSettings()
	if err != nil {
		st.Details = fmt.Sprintf("error reading settings: %v", err)
		return st
	}

	if val, ok := settings["github.copilot.chat.otel.enabled"].(bool); ok && val {
		st.Configured = true
	}

	if val, ok := settings["github.copilot.chat.otel.captureContent"].(bool); ok && val {
		st.Prompts = true
	}

	return st
}

func (c *CopilotChat) Verify(cfg config.Config) VerifyResult {
	if !c.Detect() {
		return VerifyResult{OK: false, Message: "VS Code settings directory not found"}
	}

	settings, err := c.readSettings()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read settings: %v", err)}
	}

	val, ok := settings["github.copilot.chat.otel.enabled"].(bool)
	if !ok || !val {
		return VerifyResult{OK: false, Message: "github.copilot.chat.otel.enabled not set"}
	}

	if t, _ := settings["github.copilot.chat.otel.exporterType"].(string); t != "otlp-http" {
		return VerifyResult{OK: false, Message: fmt.Sprintf("exporterType=%q, want \"otlp-http\" (gRPC is not accepted)", t)}
	}
	endpoint, _ := settings["github.copilot.chat.otel.otlpEndpoint"].(string)
	if endpoint == "" {
		return VerifyResult{OK: false, Message: "otlpEndpoint not set"}
	}

	var warnings []string
	if endpoint != cfg.BaseEndpoint() {
		warnings = append(warnings, fmt.Sprintf("otlpEndpoint %s differs from %s", endpoint, cfg.BaseEndpoint()))
	}
	for _, env := range []string{"COPILOT_OTEL_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if v := os.Getenv(env); v != "" && strings.TrimRight(v, "/") != endpoint {
			warnings = append(warnings, fmt.Sprintf("%s=%s overrides otlpEndpoint", env, v))
		}
	}
	headers, _ := settings["github.copilot.chat.otel.headers"].(map[string]interface{})
	if _, ok := headers[config.AuthHeader]; !ok && !strings.Contains(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"), config.AuthHeader+"=") {
		warnings = append(warnings, "no x-flowstate-key header in settings or OTEL_EXPORTER_OTLP_HEADERS")
	}
	if captureContent, ok := settings["github.copilot.chat.otel.captureContent"].(bool); !ok || !captureContent {
		warnings = append(warnings, "captureContent not enabled (re-run with --prompts)")
	}

	result := VerifyResult{OK: true, Message: fmt.Sprintf("settings.json present · OTLP/HTTP endpoint=%s", endpoint), Warning: joinWarnings(warnings)}
	if shell.GUIEnvSupported() && !shell.GUIEnvInstalled() {
		result.Notes = append(result.Notes,
			"VS Code opened from the Dock does not read "+shell.ProfilePath()+"; builds before 1.138 ignore the headers setting, so run `install --gui-env` (launchctl setenv) or start VS Code with `code` from a terminal")
	}
	return result
}
