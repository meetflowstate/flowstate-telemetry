package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"gopkg.in/yaml.v3"
)

// AiderTool handles the Aider AI coding assistant.
//
// Aider has no OpenTelemetry export, so nothing is sent to Flowstate and
// there is nothing to configure. Earlier releases set `llm-history-file` in
// ~/.aider.conf.yml, which makes Aider write every prompt and response to a
// local file; Install and Remove take that setting out again (only the value
// this CLI wrote, never a history file the user chose).
type AiderTool struct{}

// legacyAiderHistoryFile is the llm-history-file earlier releases wrote.
const legacyAiderHistoryFile = "/var/log/aider/llm-history.log"

func (a *AiderTool) Name() string { return "aider" }

func (a *AiderTool) configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aider.conf.yml")
}

func (a *AiderTool) Detect() bool {
	_, err := exec.LookPath("aider")
	return err == nil
}

func (a *AiderTool) readConfig() (map[string]interface{}, error) {
	data, err := os.ReadFile(a.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", unparseable(err))
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}
	return cfg, nil
}

func (a *AiderTool) writeConfig(cfg map[string]interface{}) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(a.configPath(), data, 0644)
}

// hasLegacyHistory reports whether the config has the llm-history-file
// setting an earlier release wrote.
func hasLegacyHistory(cfg map[string]interface{}) bool {
	v, _ := cfg["llm-history-file"].(string)
	return v == legacyAiderHistoryFile
}

// removeLegacy deletes the earlier release's llm-history-file setting,
// rewriting the config only when it was there.
func (a *AiderTool) removeLegacy(dryRun bool) error {
	aiderCfg, err := a.readConfig()
	if err != nil {
		return err
	}
	if !hasLegacyHistory(aiderCfg) {
		return nil
	}
	delete(aiderCfg, "llm-history-file")
	if dryRun {
		fmt.Printf("[dry-run] Would remove llm-history-file (%s) from %s\n", legacyAiderHistoryFile, a.configPath())
		return nil
	}
	return a.writeConfig(aiderCfg)
}

// Install configures nothing (Aider cannot export telemetry); it removes the
// prompt/response history setting earlier releases wrote.
func (a *AiderTool) Install(cfg config.Config) error {
	return a.removeLegacy(cfg.DryRun)
}

// InstallNotes implements Noter.
func (a *AiderTool) InstallNotes(cfg config.Config) []string {
	return []string{"Aider has no telemetry export: nothing is sent to Flowstate. The llm-history-file setting earlier releases added (it stores full prompts and responses locally) is removed"}
}

func (a *AiderTool) Remove() error {
	return a.removeLegacy(false)
}

// Status reports Configured only while the earlier release's setting is
// still there, so `remove --all` cleans it up.
func (a *AiderTool) Status() ToolStatus {
	st := ToolStatus{Detected: a.Detect()}
	if !st.Detected {
		return st
	}

	aiderCfg, err := a.readConfig()
	if err != nil {
		st.Details = fmt.Sprintf("error reading config: %v", err)
		return st
	}

	if hasLegacyHistory(aiderCfg) {
		st.Configured = true
		st.Prompts = true
		st.Details = "llm-history-file set by an earlier release (re-run install to remove it)"
	}

	return st
}

func (a *AiderTool) Verify(cfg config.Config) VerifyResult {
	if !a.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	aiderCfg, err := a.readConfig()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read config: %v", err)}
	}

	if hasLegacyHistory(aiderCfg) {
		return VerifyResult{OK: false, Message: fmt.Sprintf("llm-history-file=%s (set by an earlier release) stores full prompts and responses locally: re-run install to remove it", legacyAiderHistoryFile)}
	}

	result := VerifyResult{OK: true, Message: "nothing to configure: Aider has no telemetry export (nothing is sent to Flowstate)"}
	if hist, _ := aiderCfg["llm-history-file"].(string); hist != "" {
		result.Warning = fmt.Sprintf("llm-history-file=%s stores full prompts and responses locally", hist)
	}
	return result
}
