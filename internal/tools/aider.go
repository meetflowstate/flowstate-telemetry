package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"gopkg.in/yaml.v3"
)

// AiderTool configures telemetry for the Aider AI coding assistant.
type AiderTool struct{}

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
		return nil, fmt.Errorf("parsing config: %w", err)
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

func (a *AiderTool) Install(cfg config.Config) error {
	aiderCfg, err := a.readConfig()
	if err != nil {
		return err
	}

	aiderCfg["llm-history-file"] = "/var/log/aider/llm-history.log"

	if cfg.DryRun {
		data, _ := yaml.Marshal(aiderCfg)
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", a.configPath(), string(data))
		return nil
	}

	return a.writeConfig(aiderCfg)
}

func (a *AiderTool) Remove() error {
	aiderCfg, err := a.readConfig()
	if err != nil {
		return err
	}

	delete(aiderCfg, "llm-history-file")

	return a.writeConfig(aiderCfg)
}

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

	if _, ok := aiderCfg["llm-history-file"]; ok {
		st.Configured = true
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

	histFile, ok := aiderCfg["llm-history-file"].(string)
	if !ok || histFile == "" {
		return VerifyResult{OK: false, Message: "llm-history-file not configured"}
	}

	result := VerifyResult{OK: true, Message: fmt.Sprintf("llm-history-file=%s", histFile)}
	result.Warning = "file watcher sidecar not yet available (V2 feature)"

	return result
}
