package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// GeminiCLI configures telemetry for Google's Gemini CLI.
type GeminiCLI struct{}

func (g *GeminiCLI) Name() string { return "gemini-cli" }

func (g *GeminiCLI) configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini")
}

func (g *GeminiCLI) settingsPath() string {
	return filepath.Join(g.configDir(), "settings.json")
}

func (g *GeminiCLI) Detect() bool {
	if _, err := exec.LookPath("gemini"); err == nil {
		return true
	}
	if _, err := os.Stat(g.configDir()); err == nil {
		return true
	}
	return false
}

func (g *GeminiCLI) readSettings() (map[string]interface{}, error) {
	data, err := os.ReadFile(g.settingsPath())
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

func (g *GeminiCLI) writeSettings(settings map[string]interface{}) error {
	if err := os.MkdirAll(g.configDir(), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}
	return os.WriteFile(g.settingsPath(), data, 0644)
}

func (g *GeminiCLI) Install(cfg config.Config) error {
	settings, err := g.readSettings()
	if err != nil {
		return err
	}

	telemetry := map[string]interface{}{
		"enabled":      true,
		"target":       "local",
		"otlpEndpoint": cfg.Endpoint,
		"otlpProtocol": "grpc",
		"logPrompts":   false,
	}

	if cfg.Prompts {
		telemetry["logPrompts"] = true
	}

	settings["telemetry"] = telemetry

	if cfg.DryRun {
		data, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", g.settingsPath(), string(data))
		return nil
	}

	return g.writeSettings(settings)
}

func (g *GeminiCLI) Remove() error {
	settings, err := g.readSettings()
	if err != nil {
		return err
	}

	delete(settings, "telemetry")

	return g.writeSettings(settings)
}

func (g *GeminiCLI) Status() ToolStatus {
	st := ToolStatus{Detected: g.Detect()}
	if !st.Detected {
		return st
	}

	settings, err := g.readSettings()
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

func (g *GeminiCLI) Verify(cfg config.Config) VerifyResult {
	if !g.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	settings, err := g.readSettings()
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

	result := VerifyResult{OK: true, Message: "settings.json present · telemetry enabled"}
	if logPrompts, ok := telemetry["logPrompts"].(bool); !ok || !logPrompts {
		result.Warning = "logPrompts not enabled (re-run with --prompts)"
	} else {
		result.Message = "settings.json present · logPrompts=true"
	}

	return result
}
