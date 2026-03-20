package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// ClaudeCode configures telemetry for Anthropic's Claude Code CLI.
type ClaudeCode struct{}

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) Detect() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

func (c *ClaudeCode) settingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func (c *ClaudeCode) readSettings() (map[string]interface{}, error) {
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

func (c *ClaudeCode) writeSettings(settings map[string]interface{}) error {
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

func (c *ClaudeCode) Install(cfg config.Config) error {
	settings, err := c.readSettings()
	if err != nil {
		return err
	}

	env := map[string]interface{}{
		"CLAUDE_CODE_ENABLE_TELEMETRY":     "1",
		"OTEL_METRICS_EXPORTER":            "otlp",
		"OTEL_LOGS_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":      "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT":      cfg.Endpoint,
		"OTEL_EXPORTER_OTLP_HEADERS":       fmt.Sprintf("x-flowstate-key=%s", cfg.Key),
		"OTEL_METRICS_TEMPORALITY_PREFERENCE": "cumulative",
		"OTEL_METRICS_INCLUDE_SESSION_ID":     "true",
		"OTEL_METRICS_INCLUDE_ACCOUNT_UUID":   "true",
	}

	if cfg.Prompts {
		env["OTEL_LOG_USER_PROMPTS"] = "1"
	}

	// Merge with existing env block
	existingEnv, ok := settings["env"].(map[string]interface{})
	if !ok {
		existingEnv = make(map[string]interface{})
	}
	for k, v := range env {
		existingEnv[k] = v
	}
	settings["env"] = existingEnv

	if cfg.DryRun {
		data, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", c.settingsPath(), string(data))
		return nil
	}

	return c.writeSettings(settings)
}

func (c *ClaudeCode) Remove() error {
	settings, err := c.readSettings()
	if err != nil {
		return err
	}

	envBlock, ok := settings["env"].(map[string]interface{})
	if !ok {
		return nil
	}

	keysToRemove := []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY",
		"OTEL_METRICS_EXPORTER",
		"OTEL_LOGS_EXPORTER",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_HEADERS",
		"OTEL_METRICS_TEMPORALITY_PREFERENCE",
		"OTEL_METRICS_INCLUDE_SESSION_ID",
		"OTEL_METRICS_INCLUDE_ACCOUNT_UUID",
		"OTEL_LOG_USER_PROMPTS",
	}

	for _, k := range keysToRemove {
		delete(envBlock, k)
	}

	if len(envBlock) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = envBlock
	}

	return c.writeSettings(settings)
}

func (c *ClaudeCode) Status() ToolStatus {
	st := ToolStatus{Detected: c.Detect()}
	if !st.Detected {
		return st
	}

	settings, err := c.readSettings()
	if err != nil {
		st.Details = fmt.Sprintf("error reading settings: %v", err)
		return st
	}

	envBlock, ok := settings["env"].(map[string]interface{})
	if !ok {
		return st
	}

	if val, ok := envBlock["CLAUDE_CODE_ENABLE_TELEMETRY"].(string); ok && val == "1" {
		st.Configured = true
	}

	if val, ok := envBlock["OTEL_LOG_USER_PROMPTS"].(string); ok && val == "1" {
		st.Prompts = true
	}

	return st
}

func (c *ClaudeCode) Verify(cfg config.Config) VerifyResult {
	if !c.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	settings, err := c.readSettings()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read settings: %v", err)}
	}

	envBlock, ok := settings["env"].(map[string]interface{})
	if !ok {
		return VerifyResult{OK: false, Message: "no env block in settings.json"}
	}

	val, ok := envBlock["CLAUDE_CODE_ENABLE_TELEMETRY"].(string)
	if !ok || val != "1" {
		return VerifyResult{OK: false, Message: "CLAUDE_CODE_ENABLE_TELEMETRY not set"}
	}

	result := VerifyResult{OK: true, Message: "OTel endpoint configured"}
	if _, ok := envBlock["OTEL_LOG_USER_PROMPTS"]; !ok {
		result.Warning = "prompt capture not enabled (re-run with --prompts)"
	}

	return result
}
