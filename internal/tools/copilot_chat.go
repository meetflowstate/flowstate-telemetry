package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// CopilotChat configures telemetry for GitHub Copilot Chat in VS Code.
type CopilotChat struct{}

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
	settings["github.copilot.chat.otel.exporterType"] = "otlp-grpc"
	settings["github.copilot.chat.otel.otlpEndpoint"] = cfg.Endpoint

	if cfg.Prompts {
		settings["github.copilot.chat.otel.captureContent"] = true
	}

	if cfg.DryRun {
		data, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", c.settingsPath(), string(data))
		return nil
	}

	return c.writeSettings(settings)
}

func (c *CopilotChat) Remove() error {
	settings, err := c.readSettings()
	if err != nil {
		return err
	}

	keysToRemove := []string{
		"github.copilot.chat.otel.enabled",
		"github.copilot.chat.otel.exporterType",
		"github.copilot.chat.otel.otlpEndpoint",
		"github.copilot.chat.otel.captureContent",
	}

	for _, k := range keysToRemove {
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

	result := VerifyResult{OK: true, Message: "settings.json present · OTel enabled"}
	if captureContent, ok := settings["github.copilot.chat.otel.captureContent"].(bool); !ok || !captureContent {
		result.Warning = "captureContent not enabled (re-run with --prompts)"
	}

	return result
}
