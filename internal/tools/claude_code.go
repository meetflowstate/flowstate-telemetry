package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
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
		return nil, fmt.Errorf("parsing settings: %w", unparseable(err))
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
	// The file holds the key: a new file is private (0600). Install
	// tightens an existing one (tightenMode). Written in place, so a
	// symlinked settings.json stays a symlink.
	return os.WriteFile(c.settingsPath(), data, 0600)
}

func (c *ClaudeCode) Install(cfg config.Config) error {
	settings, err := c.readSettings()
	if err != nil {
		return err
	}

	// The SDK appends /v1/metrics and /v1/logs to the base endpoint for
	// http/protobuf. Delta temporality lets the receiver sum points without
	// keeping per-series state.
	env := map[string]interface{}{
		"CLAUDE_CODE_ENABLE_TELEMETRY":                      "1",
		"OTEL_METRICS_EXPORTER":                             "otlp",
		"OTEL_LOGS_EXPORTER":                                "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":                       "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT":                       cfg.BaseEndpoint(),
		"OTEL_EXPORTER_OTLP_HEADERS":                        cfg.HeadersEnv(),
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "delta",
		"OTEL_METRICS_INCLUDE_SESSION_ID":                   "true",
		"OTEL_METRICS_INCLUDE_ACCOUNT_UUID":                 "true",
	}

	if cfg.Prompts {
		env["OTEL_LOG_USER_PROMPTS"] = "1"
	}

	// Merge with existing env block
	existingEnv, ok := settings["env"].(map[string]interface{})
	if !ok {
		existingEnv = make(map[string]interface{})
	}
	// Earlier releases wrote a key the SDK never read.
	delete(existingEnv, "OTEL_METRICS_TEMPORALITY_PREFERENCE")
	if !cfg.Prompts {
		delete(existingEnv, "OTEL_LOG_USER_PROMPTS")
	}
	// Team and e-mail are scoped to Claude Code here, never exported. The
	// user's own resource attributes are kept; only ours are replaced.
	setResourceAttrs(existingEnv, shell.ResourceAttributes(cfg))
	for k, v := range env {
		existingEnv[k] = v
	}
	settings["env"] = existingEnv

	if cfg.DryRun {
		data, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", c.settingsPath(), redactKey(string(data), cfg.Key))
		return nil
	}

	if err := c.writeSettings(settings); err != nil {
		return err
	}
	// It now holds the key.
	return tightenMode(c.settingsPath())
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
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
		"OTEL_METRICS_INCLUDE_SESSION_ID",
		"OTEL_METRICS_INCLUDE_ACCOUNT_UUID",
		"OTEL_LOG_USER_PROMPTS",
	}

	for _, k := range keysToRemove {
		delete(envBlock, k)
	}
	setResourceAttrs(envBlock, "")

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

	if p, _ := envBlock["OTEL_EXPORTER_OTLP_PROTOCOL"].(string); p != "http/protobuf" {
		return VerifyResult{OK: false, Message: fmt.Sprintf("OTEL_EXPORTER_OTLP_PROTOCOL=%q, want http/protobuf", p)}
	}
	endpoint, _ := envBlock["OTEL_EXPORTER_OTLP_ENDPOINT"].(string)
	if endpoint == "" {
		return VerifyResult{OK: false, Message: "OTEL_EXPORTER_OTLP_ENDPOINT not set"}
	}
	if r := legacyEndpoint("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint); r != nil {
		return *r
	}
	if h, _ := envBlock["OTEL_EXPORTER_OTLP_HEADERS"].(string); !strings.Contains(h, config.AuthHeader+"=") || strings.HasSuffix(h, "=") {
		return VerifyResult{OK: false, Message: "OTEL_EXPORTER_OTLP_HEADERS has no x-flowstate-key"}
	}

	result := VerifyResult{OK: true, Message: fmt.Sprintf("OTLP/HTTP endpoint=%s", endpoint)}
	var warnings []string
	if endpoint != cfg.BaseEndpoint() {
		warnings = append(warnings, fmt.Sprintf("endpoint %s differs from %s", endpoint, cfg.BaseEndpoint()))
	}
	if t, _ := envBlock["OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE"].(string); t != "delta" {
		warnings = append(warnings, "metrics temporality is not delta (re-run install)")
	}
	if p, _ := envBlock["OTEL_LOG_USER_PROMPTS"].(string); p != "" && p != "0" && p != "false" {
		warnings = append(warnings, promptsOnWarning("OTEL_LOG_USER_PROMPTS"))
	}
	result.Warning = strings.Join(warnings, "; ")

	return result
}
