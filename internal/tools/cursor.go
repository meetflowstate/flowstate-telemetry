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

// CursorTool configures telemetry hooks for the Cursor editor.
type CursorTool struct{}

func (c *CursorTool) Name() string { return "cursor" }

func (c *CursorTool) cursorDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cursor")
}

func (c *CursorTool) hooksDir() string {
	return filepath.Join(c.cursorDir(), "hooks")
}

func (c *CursorTool) hookScriptPath() string {
	return filepath.Join(c.hooksDir(), "flowstate.sh")
}

func (c *CursorTool) hooksConfigPath() string {
	return filepath.Join(c.cursorDir(), "hooks.json")
}

func (c *CursorTool) Detect() bool {
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/Applications/Cursor.app"); err == nil {
			return true
		}
	}
	if _, err := os.Stat(c.cursorDir()); err == nil {
		return true
	}
	return false
}

func (c *CursorTool) readHooksConfig() (map[string]interface{}, error) {
	data, err := os.ReadFile(c.hooksConfigPath())
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

func (c *CursorTool) writeHooksConfig(cfg map[string]interface{}) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling hooks.json: %w", err)
	}
	return os.WriteFile(c.hooksConfigPath(), data, 0644)
}

func (c *CursorTool) Install(cfg config.Config) error {
	// Write hook script
	if err := os.MkdirAll(c.hooksDir(), 0755); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}

	if cfg.DryRun {
		fmt.Printf("[dry-run] Would write hook script to %s\n", c.hookScriptPath())
		fmt.Printf("[dry-run] Would write hooks config to %s\n", c.hooksConfigPath())
		return nil
	}

	if err := os.WriteFile(c.hookScriptPath(), []byte(hooks.CursorHookScript), 0755); err != nil {
		return fmt.Errorf("writing hook script: %w", err)
	}

	// Write hooks.json
	hooksCfg, err := c.readHooksConfig()
	if err != nil {
		return err
	}

	hookCmd := map[string]interface{}{"command": c.hookScriptPath()}
	hookEntry := []interface{}{hookCmd}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		hooksBlock = make(map[string]interface{})
	}

	// Add flowstate hook to each event type, preserving existing hooks
	for _, event := range []string{"stop", "beforeMCPExecution", "beforeShellExecution", "afterFileEdit"} {
		existing := c.getExistingHooks(hooksBlock, event)
		merged := c.mergeHookEntry(existing, hookCmd)
		hooksBlock[event] = merged
	}

	hooksCfg["version"] = float64(1)
	hooksCfg["hooks"] = hooksBlock
	_ = hookEntry // used indirectly above

	return c.writeHooksConfig(hooksCfg)
}

func (c *CursorTool) getExistingHooks(hooksBlock map[string]interface{}, event string) []interface{} {
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

func (c *CursorTool) mergeHookEntry(existing []interface{}, newEntry map[string]interface{}) []interface{} {
	newCmd, _ := newEntry["command"].(string)
	for _, e := range existing {
		m, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if cmd, ok := m["command"].(string); ok && cmd == newCmd {
			return existing // already present
		}
	}
	return append(existing, newEntry)
}

func (c *CursorTool) Remove() error {
	// Remove hook script
	_ = os.Remove(c.hookScriptPath())

	// Clean up hooks.json
	hooksCfg, err := c.readHooksConfig()
	if err != nil {
		return err
	}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		return nil
	}

	for _, event := range []string{"stop", "beforeMCPExecution", "beforeShellExecution", "afterFileEdit"} {
		existing := c.getExistingHooks(hooksBlock, event)
		filtered := c.removeFlowstateHook(existing)
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

	return c.writeHooksConfig(hooksCfg)
}

func (c *CursorTool) removeFlowstateHook(hooks []interface{}) []interface{} {
	var result []interface{}
	for _, h := range hooks {
		m, ok := h.(map[string]interface{})
		if !ok {
			result = append(result, h)
			continue
		}
		cmd, ok := m["command"].(string)
		if !ok || cmd != c.hookScriptPath() {
			result = append(result, h)
		}
	}
	return result
}

func (c *CursorTool) Status() ToolStatus {
	st := ToolStatus{Detected: c.Detect()}
	if !st.Detected {
		return st
	}

	if _, err := os.Stat(c.hookScriptPath()); err == nil {
		st.Configured = true
	}

	if _, err := os.Stat(c.hooksConfigPath()); err == nil {
		hooksCfg, err := c.readHooksConfig()
		if err == nil {
			if hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{}); ok {
				if _, ok := hooksBlock["stop"]; ok {
					st.Configured = true
				}
			}
		}
	}

	return st
}

func (c *CursorTool) Verify(cfg config.Config) VerifyResult {
	if !c.Detect() {
		return VerifyResult{OK: false, Message: "Cursor not detected"}
	}

	if _, err := os.Stat(c.hookScriptPath()); err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("%s not found", c.hookScriptPath())}
	}

	if _, err := os.Stat(c.hooksConfigPath()); err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("%s not found", c.hooksConfigPath())}
	}

	return VerifyResult{OK: true, Message: "hook script and hooks.json present"}
}
