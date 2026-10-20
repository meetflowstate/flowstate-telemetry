package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// CopilotChat configures telemetry for GitHub Copilot Chat in VS Code.
//
// exporterType "otlp-http" makes the extension append /v1/{traces,logs,
// metrics} to otlpEndpoint. The key goes only in the
// `github.copilot.chat.otel.headers` setting (VS Code 1.138+): it is never
// exported as OTEL_EXPORTER_OTLP_HEADERS, which every OTel SDK would send to
// its own collector, so older builds without the setting cannot authenticate.
//
// VS Code Settings Sync would upload the headers setting (and so the key) to
// the user's Microsoft/GitHub account and every synced machine, so Install
// also lists it in `settingsSync.ignoredSettings`.
type CopilotChat struct{}

const (
	copilotChatHeadersKey = "github.copilot.chat.otel.headers"
	settingsSyncIgnored   = "settingsSync.ignoredSettings"
)

// copilotChatKeys are the settings Flowstate owns.
var copilotChatKeys = []string{
	"github.copilot.chat.otel.enabled",
	"github.copilot.chat.otel.exporterType",
	"github.copilot.chat.otel.otlpEndpoint",
	copilotChatHeadersKey,
	"github.copilot.chat.otel.captureContent",
}

// setSyncIgnored adds (or removes) the headers setting in
// settingsSync.ignoredSettings, keeping the user's other entries.
func setSyncIgnored(settings map[string]interface{}, ignore bool) {
	list, _ := settings[settingsSyncIgnored].([]interface{})
	var kept []interface{}
	for _, v := range list {
		if v != copilotChatHeadersKey {
			kept = append(kept, v)
		}
	}
	if ignore {
		kept = append(kept, copilotChatHeadersKey)
	}
	if len(kept) == 0 {
		delete(settings, settingsSyncIgnored)
	} else {
		settings[settingsSyncIgnored] = kept
	}
}

// syncIgnored reports whether the headers setting is excluded from Settings Sync.
func syncIgnored(settings map[string]interface{}) bool {
	list, _ := settings[settingsSyncIgnored].([]interface{})
	for _, v := range list {
		if v == copilotChatHeadersKey {
			return true
		}
	}
	return false
}

// copilotChatKeyNotes is printed after install and by verify.
var copilotChatKeyNotes = []string{
	"the key is stored in VS Code settings.json (" + copilotChatHeadersKey + "); install lists it in " + settingsSyncIgnored +
		" so Settings Sync does not upload it. If Settings Sync was on before, the key may already be in your sync data: rotate it",
	"VS Code builds without the " + copilotChatHeadersKey + " setting (before 1.138) cannot authenticate: update VS Code",
}

// InstallNotes implements Noter.
func (c *CopilotChat) InstallNotes(cfg config.Config) []string {
	if cfg.Key == "" {
		return nil
	}
	return copilotChatKeyNotes
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
	// The file holds the key (github.copilot.chat.otel.headers): a new file
	// is private (0600); an existing file keeps its mode.
	return os.WriteFile(c.settingsPath(), data, 0600)
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
		settings[copilotChatHeadersKey] = map[string]interface{}{config.AuthHeader: cfg.Key}
	} else {
		delete(settings, copilotChatHeadersKey)
	}
	setSyncIgnored(settings, true)

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
	setSyncIgnored(settings, false)

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

	if r := legacyEndpoint("otlpEndpoint", endpoint); r != nil {
		return *r
	}
	headers, _ := settings[copilotChatHeadersKey].(map[string]interface{})
	if key, _ := headers[config.AuthHeader].(string); key == "" {
		return VerifyResult{OK: false, Message: "no x-flowstate-key in " + copilotChatHeadersKey + " (the key is never read from the environment)",
			Notes: copilotChatKeyNotes[1:]}
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
	if !syncIgnored(settings) {
		warnings = append(warnings, "VS Code Settings Sync uploads "+copilotChatHeadersKey+" (your key) unless it is in "+settingsSyncIgnored+": re-run install")
	}
	if captureContent, _ := settings["github.copilot.chat.otel.captureContent"].(bool); captureContent {
		warnings = append(warnings, promptsOnWarning("captureContent"))
	}

	return VerifyResult{OK: true, Message: fmt.Sprintf("settings.json present · OTLP/HTTP endpoint=%s", endpoint),
		Warning: joinWarnings(warnings), Notes: copilotChatKeyNotes}
}
