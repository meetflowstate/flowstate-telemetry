package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
)

// GeminiCLI configures telemetry for Google's Gemini CLI.
//
// With otlpProtocol "http" Gemini appends /v1/{logs,metrics,traces} to
// otlpEndpoint and sends OTLP/HTTP JSON. The `telemetry.otlpHeaders` object
// exists from v0.65.0; older releases warn about it on every start, so it is
// only written when the installed version supports it. Otherwise the key goes
// into a `gemini` wrapper function in the shell profile, never into a global
// OTEL_EXPORTER_OTLP_HEADERS. The wrapper always sets delta temporality,
// which the OTel JS metrics exporter reads from the environment only.
type GeminiCLI struct{}

// geminiStyleBlock renders the wrapper for Gemini CLI or Qwen Code. withKey
// adds OTEL_EXPORTER_OTLP_HEADERS for builds that cannot take the key from
// settings.json; the key itself comes from FLOWSTATE_OTLP_KEY at run time.
// Team and e-mail resource attributes are scoped here too (settings.json has
// no option for them).
func geminiStyleBlock(command, product string, withKey bool, cfg config.Config) []string {
	assignments := []string{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta"}
	if withKey {
		assignments = append(assignments, fmt.Sprintf(`OTEL_EXPORTER_OTLP_HEADERS="%s=${FLOWSTATE_OTLP_KEY:-}"`, config.AuthHeader))
	}
	assignments = append(assignments, resourceAttrsAssignment(cfg)...)
	return []string{
		"# Scopes OpenTelemetry settings to " + product + " only.",
		shell.WrapperFunction(command, assignments),
	}
}

// geminiVersion reports the installed Gemini CLI version ("" if unknown).
// Swapped out by tests.
var geminiVersion = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gemini", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// geminiSupportsHeaders reports whether version (e.g. "0.65.0-preview.0")
// accepts telemetry.otlpHeaders.
func geminiSupportsHeaders(version string) bool {
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimPrefix(version, "v"), "%d.%d", &major, &minor); err != nil {
		return false
	}
	return major > 0 || minor >= 65
}

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
		return nil, fmt.Errorf("parsing settings: %w", unparseable(err))
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
	// The file may hold the key (telemetry.otlpHeaders): a new file is
	// private (0600); Install tightens an existing one that holds it.
	return os.WriteFile(g.settingsPath(), data, 0600)
}

func (g *GeminiCLI) Install(cfg config.Config) error {
	settings, err := g.readSettings()
	if err != nil {
		return err
	}

	// target "local" is required for a custom endpoint ("gcp" ignores it).
	// logPrompts defaults to true in Gemini, so it is always written.
	telemetry := map[string]interface{}{
		"enabled":      true,
		"target":       "local",
		"otlpEndpoint": cfg.BaseEndpoint(),
		"otlpProtocol": "http",
		"logPrompts":   cfg.Prompts,
	}
	inSettings := cfg.Key != "" && geminiSupportsHeaders(geminiVersion())
	if inSettings {
		telemetry["otlpHeaders"] = map[string]interface{}{config.AuthHeader: cfg.Key}
	}

	settings["telemetry"] = telemetry
	block := geminiStyleBlock("gemini", "Gemini CLI", !inSettings, cfg)

	if cfg.DryRun {
		data, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", g.settingsPath(), redactKey(string(data), cfg.Key))
		return shell.WriteToolBlock(g.Name(), block, true)
	}

	if err := g.writeSettings(settings); err != nil {
		return err
	}
	if inSettings {
		if err := tightenMode(g.settingsPath()); err != nil {
			return err
		}
	}
	return shell.WriteToolBlock(g.Name(), block, false)
}

func (g *GeminiCLI) Remove() error {
	// The shell block is removed even when the settings file cannot be.
	var err error
	if fileExists(g.settingsPath()) {
		var settings map[string]interface{}
		if settings, err = g.readSettings(); err == nil {
			delete(settings, "telemetry")
			err = g.writeSettings(settings)
		}
	}
	return errors.Join(err, shell.RemoveToolBlock(g.Name()))
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

	return verifyGeminiStyle(telemetry, cfg, "GEMINI_TELEMETRY_OTLP_ENDPOINT", g.Name(), "gemini", true)
}

// verifyGeminiStyle checks a Gemini/Qwen `telemetry` block and the tool's
// wrapper function. headersSetting says whether the tool can take
// otlpHeaders from settings.json.
func verifyGeminiStyle(telemetry map[string]interface{}, cfg config.Config, endpointEnv, tool, command string, headersSetting bool) VerifyResult {
	if p, _ := telemetry["otlpProtocol"].(string); p != "http" {
		return VerifyResult{OK: false, Message: fmt.Sprintf("otlpProtocol=%q, want \"http\" (gRPC is not accepted)", p)}
	}
	endpoint, _ := telemetry["otlpEndpoint"].(string)
	if endpoint == "" {
		return VerifyResult{OK: false, Message: "otlpEndpoint not set"}
	}
	if r := legacyEndpoint("otlpEndpoint", endpoint); r != nil {
		return *r
	}
	if _, ok := telemetry["outfile"]; ok {
		return VerifyResult{OK: false, Message: "telemetry.outfile is set and overrides OTLP export"}
	}

	headers, _ := telemetry["otlpHeaders"].(map[string]interface{})
	_, inSettings := headers[config.AuthHeader]
	block := shell.ToolBlock(tool)
	inWrapper := strings.Contains(block, "OTEL_EXPORTER_OTLP_HEADERS=")
	if !inSettings && !inWrapper {
		return VerifyResult{OK: false, Message: fmt.Sprintf("no x-flowstate-key: neither telemetry.otlpHeaders nor a `%s` wrapper in %s (re-run install)", command, shellProfileName())}
	}

	var warnings []string
	if endpoint != cfg.BaseEndpoint() {
		warnings = append(warnings, fmt.Sprintf("otlpEndpoint %s differs from %s", endpoint, cfg.BaseEndpoint()))
	}
	for _, env := range []string{endpointEnv, "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if v := os.Getenv(env); v != "" && strings.TrimRight(v, "/") != endpoint {
			warnings = append(warnings, fmt.Sprintf("%s=%s overrides otlpEndpoint", env, v))
		}
	}
	if !strings.Contains(block, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta") {
		warnings = append(warnings, fmt.Sprintf("metrics temporality is not delta: the `%s` wrapper is missing (re-run install)", command))
	}
	if p, _ := telemetry["logPrompts"].(bool); p {
		warnings = append(warnings, promptsOnWarning("logPrompts"))
	}

	result := VerifyResult{OK: true, Message: fmt.Sprintf("settings.json present · OTLP/HTTP endpoint=%s", endpoint), Warning: joinWarnings(warnings)}
	if !inSettings {
		wrapperNote := fmt.Sprintf("the key comes from the `%s` wrapper in %s: only `%s` started from a shell that reads it is covered", command, shellProfileName(), command)
		if headersSetting {
			wrapperNote += " (telemetry.otlpHeaders needs Gemini CLI 0.65+; re-run install after upgrading)"
		} else {
			wrapperNote += " (settings.json has no header option)"
		}
		result.Notes = append(result.Notes, wrapperNote)
	}
	return result
}
