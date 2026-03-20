package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
)

// WindsurfTool configures telemetry hooks for the Windsurf (Codeium) editor.
type WindsurfTool struct{}

func (w *WindsurfTool) Name() string { return "windsurf" }

func (w *WindsurfTool) windsurfDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".windsurf")
}

func (w *WindsurfTool) codeiumDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codeium")
}

func (w *WindsurfTool) hooksDir() string {
	return filepath.Join(w.windsurfDir(), "hooks")
}

func (w *WindsurfTool) hookScriptPath() string {
	return filepath.Join(w.hooksDir(), "flowstate.sh")
}

func (w *WindsurfTool) hooksConfigPath() string {
	return filepath.Join(w.codeiumDir(), "windsurf", "hooks.json")
}

func (w *WindsurfTool) Detect() bool {
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/Applications/Windsurf.app"); err == nil {
			return true
		}
	}
	if _, err := os.Stat(w.codeiumDir()); err == nil {
		return true
	}
	return false
}

func (w *WindsurfTool) readHooksConfig() (map[string]interface{}, error) {
	data, err := os.ReadFile(w.hooksConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("reading hooks.json: %w", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing hooks.json: %w", err)
	}
	return cfg, nil
}

func (w *WindsurfTool) writeHooksConfig(cfg map[string]interface{}) error {
	dir := filepath.Dir(w.hooksConfigPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling hooks.json: %w", err)
	}
	return os.WriteFile(w.hooksConfigPath(), data, 0644)
}

func (w *WindsurfTool) Install(cfg config.Config) error {
	// Write hook script
	if err := os.MkdirAll(w.hooksDir(), 0755); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}

	if cfg.DryRun {
		fmt.Printf("[dry-run] Would write hook script to %s\n", w.hookScriptPath())
		fmt.Printf("[dry-run] Would write hooks config to %s\n", w.hooksConfigPath())
		return nil
	}

	if err := os.WriteFile(w.hookScriptPath(), []byte(hooks.WindsurfHookScript), 0755); err != nil {
		return fmt.Errorf("writing hook script: %w", err)
	}

	// Write hooks.json
	hooksCfg, err := w.readHooksConfig()
	if err != nil {
		return err
	}

	hookCmd := map[string]interface{}{"command": w.hookScriptPath()}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		hooksBlock = make(map[string]interface{})
	}

	events := []string{
		"pre_user_prompt",
		"post_cascade_response_with_transcript",
		"post_write_code",
		"post_run_command",
		"post_mcp_tool_use",
	}

	for _, event := range events {
		existing := w.getExistingHooks(hooksBlock, event)
		merged := w.mergeHookEntry(existing, hookCmd)
		hooksBlock[event] = merged
	}

	hooksCfg["hooks"] = hooksBlock

	return w.writeHooksConfig(hooksCfg)
}

func (w *WindsurfTool) getExistingHooks(hooksBlock map[string]interface{}, event string) []interface{} {
	raw, ok := hooksBlock[event]
	if !ok {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	return arr
}

func (w *WindsurfTool) mergeHookEntry(existing []interface{}, newEntry map[string]interface{}) []interface{} {
	newCmd, _ := newEntry["command"].(string)
	for _, e := range existing {
		m, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if cmd, ok := m["command"].(string); ok && cmd == newCmd {
			return existing
		}
	}
	return append(existing, newEntry)
}

func (w *WindsurfTool) Remove() error {
	_ = os.Remove(w.hookScriptPath())

	hooksCfg, err := w.readHooksConfig()
	if err != nil {
		return err
	}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		return nil
	}

	events := []string{
		"pre_user_prompt",
		"post_cascade_response_with_transcript",
		"post_write_code",
		"post_run_command",
		"post_mcp_tool_use",
	}

	for _, event := range events {
		existing := w.getExistingHooks(hooksBlock, event)
		filtered := w.removeFlowstateHook(existing)
		if len(filtered) == 0 {
			delete(hooksBlock, event)
		} else {
			hooksBlock[event] = filtered
		}
	}

	if len(hooksBlock) == 0 {
		delete(hooksCfg, "hooks")
	} else {
		hooksCfg["hooks"] = hooksBlock
	}

	return w.writeHooksConfig(hooksCfg)
}

func (w *WindsurfTool) removeFlowstateHook(hooksList []interface{}) []interface{} {
	var result []interface{}
	for _, h := range hooksList {
		m, ok := h.(map[string]interface{})
		if !ok {
			result = append(result, h)
			continue
		}
		cmd, ok := m["command"].(string)
		if !ok || cmd != w.hookScriptPath() {
			result = append(result, h)
		}
	}
	return result
}

func (w *WindsurfTool) Status() ToolStatus {
	st := ToolStatus{Detected: w.Detect()}
	if !st.Detected {
		return st
	}

	if _, err := os.Stat(w.hookScriptPath()); err == nil {
		st.Configured = true
	}

	return st
}

func (w *WindsurfTool) Verify(cfg config.Config) VerifyResult {
	if !w.Detect() {
		return VerifyResult{OK: false, Message: "Windsurf not detected"}
	}

	if _, err := os.Stat(w.hookScriptPath()); err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("%s not found", w.hookScriptPath())}
	}

	if _, err := os.Stat(w.hooksConfigPath()); err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("%s not found", w.hooksConfigPath())}
	}

	return VerifyResult{OK: true, Message: "hook script and hooks.json present"}
}
