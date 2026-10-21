package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
)

// WindsurfTool configures telemetry hooks for the Windsurf (Codeium) editor.
type WindsurfTool struct{}

// windsurfEvents are the Cascade hooks the Flowstate script is registered
// for. post_cascade_response_with_transcript is deliberately not used: the
// script never uploads transcripts.
var windsurfEvents = []string{
	"pre_user_prompt",
	"post_write_code",
	"post_cascade_response",
}

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

func (w *WindsurfTool) Install(cfg config.Config) error {
	hooksCfg, err := readJSONFile(w.hooksConfigPath())
	if err != nil {
		return err
	}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		hooksBlock = make(map[string]interface{})
	}
	addFlowstateHooks(hooksBlock, w.hookScriptPath(), windsurfEvents, map[string]interface{}{"show_output": false})
	hooksCfg["hooks"] = hooksBlock

	if cfg.DryRun {
		fmt.Printf("[dry-run] Would write hook script to %s\n", w.hookScriptPath())
		fmt.Printf("[dry-run] Would write %s\n", hooks.EnvFilePath())
		fmt.Printf("[dry-run] Would register hooks in %s: %v\n", w.hooksConfigPath(), windsurfEvents)
		return nil
	}

	if err := os.MkdirAll(w.hooksDir(), 0755); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}
	if err := os.WriteFile(w.hookScriptPath(), []byte(hooks.WindsurfHookScript), 0755); err != nil {
		return fmt.Errorf("writing hook script: %w", err)
	}
	if err := hooks.WriteEnvFile(cfg); err != nil {
		return err
	}

	return writeJSONFile(w.hooksConfigPath(), hooksCfg, 0644)
}

func (w *WindsurfTool) Remove() error {
	_ = os.Remove(w.hookScriptPath())

	hooksCfg, err := readJSONFile(w.hooksConfigPath())
	if err != nil {
		return err
	}

	hooksBlock, ok := hooksCfg["hooks"].(map[string]interface{})
	if !ok {
		return nil
	}
	stripFlowstateHooks(hooksBlock, w.hookScriptPath())

	if len(hooksBlock) == 0 {
		delete(hooksCfg, "hooks")
	} else {
		hooksCfg["hooks"] = hooksBlock
	}

	return writeJSONFile(w.hooksConfigPath(), hooksCfg, 0644)
}

func (w *WindsurfTool) registered() bool {
	hooksCfg, err := readJSONFile(w.hooksConfigPath())
	if err != nil {
		return false
	}
	hooksBlock, _ := hooksCfg["hooks"].(map[string]interface{})
	for _, event := range windsurfEvents {
		if !hasFlowstateHook(hooksBlock, w.hookScriptPath(), event) {
			return false
		}
	}
	return true
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
	return verifyHookTool(w.Detect(), "Windsurf", w.hookScriptPath(), w.hooksConfigPath(), w.registered(),
		"hooks do not run in Restricted Mode")
}
