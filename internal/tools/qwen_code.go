package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
)

// QwenCode configures telemetry for Alibaba's Qwen Code CLI.
//
// Same telemetry block as Gemini CLI (it is a fork), except Qwen has no
// header setting: the key reaches it only through OTEL_EXPORTER_OTLP_HEADERS,
// set by a `qwen` wrapper function in the shell profile (never exported
// globally). With otlpProtocol "http" it appends /v1/{logs,metrics,traces}
// and sends OTLP/HTTP JSON.
type QwenCode struct{}

func (q *QwenCode) Name() string { return "qwen-code" }

func (q *QwenCode) configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".qwen")
}

func (q *QwenCode) settingsPath() string {
	return filepath.Join(q.configDir(), "settings.json")
}

func (q *QwenCode) Detect() bool {
	if _, err := exec.LookPath("qwen"); err == nil {
		return true
	}
	if _, err := os.Stat(q.configDir()); err == nil {
		return true
	}
	return false
}

func (q *QwenCode) readSettings() (map[string]interface{}, error) {
	data, err := os.ReadFile(q.settingsPath())
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

func (q *QwenCode) writeSettings(settings map[string]interface{}) error {
	if err := os.MkdirAll(q.configDir(), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}
	return os.WriteFile(q.settingsPath(), data, 0644)
}

func (q *QwenCode) Install(cfg config.Config) error {
	settings, err := q.readSettings()
	if err != nil {
		return err
	}

	// logPrompts defaults to true in Qwen, so it is always written.
	telemetry := map[string]interface{}{
		"enabled":      true,
		"target":       "local",
		"otlpEndpoint": cfg.BaseEndpoint(),
		"otlpProtocol": "http",
		"logPrompts":   cfg.Prompts,
	}

	settings["telemetry"] = telemetry
	block := geminiStyleBlock("qwen", "Qwen Code", true, cfg)

	if cfg.DryRun {
		data, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", q.settingsPath(), redactKey(string(data), cfg.Key))
		return shell.WriteToolBlock(q.Name(), block, true)
	}

	if err := q.writeSettings(settings); err != nil {
		return err
	}
	return shell.WriteToolBlock(q.Name(), block, false)
}

func (q *QwenCode) Remove() error {
	settings, err := q.readSettings()
	if err != nil {
		return err
	}

	delete(settings, "telemetry")

	if err := q.writeSettings(settings); err != nil {
		return err
	}
	return shell.RemoveToolBlock(q.Name())
}

func (q *QwenCode) Status() ToolStatus {
	st := ToolStatus{Detected: q.Detect()}
	if !st.Detected {
		return st
	}

	settings, err := q.readSettings()
	if err != nil {
		st.Details = fmt.Sprintf("error reading settings: %v", err)
		return st
	}

	telemetry, ok := settings["telemetry"].(map[string]interface{})
	if !ok {
		return st
	}

	if enabled, ok := telemetry["enabled"].(bool); ok && enabled {
		st.Configured = true
	}

	if logPrompts, ok := telemetry["logPrompts"].(bool); ok && logPrompts {
		st.Prompts = true
	}

	return st
}

func (q *QwenCode) Verify(cfg config.Config) VerifyResult {
	if !q.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	settings, err := q.readSettings()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read settings: %v", err)}
	}

	telemetry, ok := settings["telemetry"].(map[string]interface{})
	if !ok {
		return VerifyResult{OK: false, Message: "no telemetry block in settings.json"}
	}

	enabled, ok := telemetry["enabled"].(bool)
	if !ok || !enabled {
		return VerifyResult{OK: false, Message: "telemetry not enabled"}
	}

	return verifyGeminiStyle(telemetry, cfg, "QWEN_TELEMETRY_OTLP_ENDPOINT", q.Name(), "qwen", false)
}
