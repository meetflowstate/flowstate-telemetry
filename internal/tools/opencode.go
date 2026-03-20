package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// OpenCodeTool configures telemetry for the OpenCode editor.
type OpenCodeTool struct{}

func (o *OpenCodeTool) Name() string { return "opencode" }

func (o *OpenCodeTool) configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "opencode")
}

func (o *OpenCodeTool) configPath() string {
	return filepath.Join(o.configDir(), "opencode.json")
}

func (o *OpenCodeTool) Detect() bool {
	_, err := exec.LookPath("opencode")
	return err == nil
}

func (o *OpenCodeTool) readConfig() (map[string]interface{}, error) {
	data, err := os.ReadFile(o.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

func (o *OpenCodeTool) writeConfig(cfg map[string]interface{}) error {
	if err := os.MkdirAll(o.configDir(), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(o.configPath(), data, 0644)
}

func (o *OpenCodeTool) Install(cfg config.Config) error {
	opencodeCfg, err := o.readConfig()
	if err != nil {
		return err
	}

	// Set plugins
	existingPlugins, ok := opencodeCfg["plugins"].([]interface{})
	if !ok {
		existingPlugins = []interface{}{}
	}

	hasPlugin := false
	for _, p := range existingPlugins {
		if ps, ok := p.(string); ok && ps == "opencode-plugin-otel" {
			hasPlugin = true
			break
		}
	}
	if !hasPlugin {
		existingPlugins = append(existingPlugins, "opencode-plugin-otel")
	}
	opencodeCfg["plugins"] = existingPlugins

	// Set env
	env := map[string]interface{}{
		"OTEL_EXPORTER_OTLP_ENDPOINT": cfg.Endpoint,
		"OTEL_EXPORTER_OTLP_HEADERS":  fmt.Sprintf("x-flowstate-key=%s", cfg.Key),
	}

	existingEnv, ok := opencodeCfg["env"].(map[string]interface{})
	if !ok {
		existingEnv = make(map[string]interface{})
	}
	for k, v := range env {
		existingEnv[k] = v
	}
	opencodeCfg["env"] = existingEnv

	if cfg.DryRun {
		data, _ := json.MarshalIndent(opencodeCfg, "", "  ")
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", o.configPath(), string(data))
		return nil
	}

	return o.writeConfig(opencodeCfg)
}

func (o *OpenCodeTool) Remove() error {
	opencodeCfg, err := o.readConfig()
	if err != nil {
		return err
	}

	// Remove plugin
	if plugins, ok := opencodeCfg["plugins"].([]interface{}); ok {
		var filtered []interface{}
		for _, p := range plugins {
			if ps, ok := p.(string); ok && ps == "opencode-plugin-otel" {
				continue
			}
			filtered = append(filtered, p)
		}
		if len(filtered) == 0 {
			delete(opencodeCfg, "plugins")
		} else {
			opencodeCfg["plugins"] = filtered
		}
	}

	// Remove env keys
	if envBlock, ok := opencodeCfg["env"].(map[string]interface{}); ok {
		delete(envBlock, "OTEL_EXPORTER_OTLP_ENDPOINT")
		delete(envBlock, "OTEL_EXPORTER_OTLP_HEADERS")
		if len(envBlock) == 0 {
			delete(opencodeCfg, "env")
		} else {
			opencodeCfg["env"] = envBlock
		}
	}

	return o.writeConfig(opencodeCfg)
}

func (o *OpenCodeTool) Status() ToolStatus {
	st := ToolStatus{Detected: o.Detect()}
	if !st.Detected {
		return st
	}

	opencodeCfg, err := o.readConfig()
	if err != nil {
		st.Details = fmt.Sprintf("error reading config: %v", err)
		return st
	}

	if envBlock, ok := opencodeCfg["env"].(map[string]interface{}); ok {
		if _, ok := envBlock["OTEL_EXPORTER_OTLP_ENDPOINT"]; ok {
			st.Configured = true
		}
	}

	return st
}

func (o *OpenCodeTool) Verify(cfg config.Config) VerifyResult {
	if !o.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	opencodeCfg, err := o.readConfig()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read config: %v", err)}
	}

	envBlock, ok := opencodeCfg["env"].(map[string]interface{})
	if !ok {
		return VerifyResult{OK: false, Message: "no env block in opencode.json"}
	}

	if _, ok := envBlock["OTEL_EXPORTER_OTLP_ENDPOINT"]; !ok {
		return VerifyResult{OK: false, Message: "OTEL_EXPORTER_OTLP_ENDPOINT not set"}
	}

	return VerifyResult{OK: true, Message: "opencode.json configured · OTel plugin enabled"}
}
