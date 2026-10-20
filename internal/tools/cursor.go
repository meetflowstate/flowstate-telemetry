package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
)

// CursorTool configures telemetry hooks for the Cursor editor.
type CursorTool struct{}

// cursorEvents are the Cursor hooks the Flowstate script is registered for.
// All are observe-only; beforeSubmitPrompt is answered with
// {"continue": true}. Permission hooks (beforeShellExecution,
// beforeMCPExecution) are deliberately not used: a malformed answer there
// blocks the action.
var cursorEvents = []string{
	"sessionStart",
	"beforeSubmitPrompt",
	"afterFileEdit",
	"afterTabFileEdit",
	"stop",
	"sessionEnd",
}

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

func (c *CursorTool) Install(cfg config.Config) error {
	hooksCfg, err := readJSONFile(c.hooksConfigPath())
	if err != nil {
		return err
	}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		hooksBlock = make(map[string]interface{})
	}
	addFlowstateHooks(hooksBlock, c.hookScriptPath(), cursorEvents, nil)
	hooksCfg["version"] = float64(1)
	hooksCfg["hooks"] = hooksBlock

	if cfg.DryRun {
		fmt.Printf("[dry-run] Would write hook script to %s\n", c.hookScriptPath())
		fmt.Printf("[dry-run] Would write %s\n", hooks.EnvFilePath())
		fmt.Printf("[dry-run] Would register hooks in %s: %v\n", c.hooksConfigPath(), cursorEvents)
		return nil
	}

	if err := os.MkdirAll(c.hooksDir(), 0755); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}
	if err := os.WriteFile(c.hookScriptPath(), []byte(hooks.CursorHookScript), 0755); err != nil {
		return fmt.Errorf("writing hook script: %w", err)
	}
	if err := hooks.WriteEnvFile(cfg); err != nil {
		return err
	}

	return writeJSONFile(c.hooksConfigPath(), hooksCfg, 0644)
}

func (c *CursorTool) Remove() error {
	_ = os.Remove(c.hookScriptPath())

	hooksCfg, err := readJSONFile(c.hooksConfigPath())
	if err != nil {
		return err
	}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		return nil
	}
	stripFlowstateHooks(hooksBlock, c.hookScriptPath())

	if len(hooksBlock) == 0 {
		delete(hooksCfg, "hooks")
	} else {
		hooksCfg["hooks"] = hooksBlock
	}

	return writeJSONFile(c.hooksConfigPath(), hooksCfg, 0644)
}

func (c *CursorTool) registered() bool {
	hooksCfg, err := readJSONFile(c.hooksConfigPath())
	if err != nil {
		return false
	}
	hooksBlock, _ := hooksCfg["hooks"].(map[string]interface{})
	for _, event := range cursorEvents {
		if !hasFlowstateHook(hooksBlock, c.hookScriptPath(), event) {
			return false
		}
	}
	return true
}

func (c *CursorTool) Status() ToolStatus {
	st := ToolStatus{Detected: c.Detect()}
	if !st.Detected {
		return st
	}

	if _, err := os.Stat(c.hookScriptPath()); err == nil {
		st.Configured = true
	}

	return st
}

func (c *CursorTool) Verify(cfg config.Config) VerifyResult {
	return verifyHookTool(c.Detect(), "Cursor", c.hookScriptPath(), c.hooksConfigPath(), c.registered(),
		"hooks.json is read when Cursor starts: restart Cursor after install")
}

// verifyHookTool is the shared Verify for hook-script tools.
func verifyHookTool(detected bool, product, script, hooksPath string, registered bool, notes ...string) VerifyResult {
	if !detected {
		return VerifyResult{OK: false, Message: product + " not detected"}
	}
	if _, err := os.Stat(script); err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("%s not found", script)}
	}
	if !registered {
		return VerifyResult{OK: false, Message: fmt.Sprintf("Flowstate hooks missing from %s", hooksPath)}
	}
	var warnings []string
	envFile, err := os.ReadFile(hooks.EnvFilePath())
	if err != nil && os.Getenv("FLOWSTATE_OTLP_KEY") == "" {
		warnings = append(warnings, fmt.Sprintf("%s missing: hooks will post without a key", hooks.EnvFilePath()))
	}
	if config.ContainsLegacyEndpoint(string(envFile)) {
		return VerifyResult{OK: false, Message: fmt.Sprintf("%s points at the retired otel.flowstate.inc receiver: re-run `flowstate-telemetry install`", hooks.EnvFilePath())}
	}
	if !commandAvailable("jq") || !commandAvailable("curl") {
		warnings = append(warnings, "jq and curl are required by the hook script")
	}
	return VerifyResult{OK: true, Message: "hook script and hooks.json present · OTLP/HTTP JSON to /v1/logs",
		Warning: joinWarnings(warnings), Notes: notes}
}
