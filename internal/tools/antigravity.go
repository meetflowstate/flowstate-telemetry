package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
)

// AntigravityTool configures telemetry hooks for Google Antigravity (IDE,
// app and agy CLI share ~/.gemini/config/hooks.json).
//
// Antigravity's hooks.json is keyed by hook-set name; Flowstate owns the
// "flowstate" entry and leaves every other entry alone. The payload does not
// name the event, so each command passes it as an argument. Antigravity has
// no prompt-submission hook, so prompt length is not reported.
type AntigravityTool struct{}

// antigravityHookSet is the top-level hooks.json key Flowstate owns.
const antigravityHookSet = "flowstate"

// antigravityEvents are the events the Flowstate script is registered for.
// PreToolUse is deliberately not used: its answer decides whether the tool
// may run.
var antigravityEvents = []string{"PostToolUse", "Stop"}

func (a *AntigravityTool) Name() string { return "antigravity" }

func (a *AntigravityTool) geminiDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini")
}

func (a *AntigravityTool) hooksConfigPath() string {
	return filepath.Join(a.geminiDir(), "config", "hooks.json")
}

func (a *AntigravityTool) hookScriptPath() string {
	return filepath.Join(a.geminiDir(), "config", "flowstate-antigravity.sh")
}

func (a *AntigravityTool) Detect() bool {
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/Applications/Antigravity.app"); err == nil {
			return true
		}
	}
	for _, bin := range []string{"agy", "antigravity"} {
		if _, err := exec.LookPath(bin); err == nil {
			return true
		}
	}
	for _, dir := range []string{"antigravity", "antigravity-cli", "antigravity-ide"} {
		if _, err := os.Stat(filepath.Join(a.geminiDir(), dir)); err == nil {
			return true
		}
	}
	return false
}

// hookSet renders the "flowstate" hooks.json entry.
func (a *AntigravityTool) hookSet() map[string]interface{} {
	handler := func(event string) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": hookCommand(a.hookScriptPath(), event),
			"timeout": float64(10),
		}
	}
	return map[string]interface{}{
		"enabled": true,
		// Tool events take matcher groups; the others take a flat handler list.
		"PostToolUse": []interface{}{map[string]interface{}{
			"matcher": "*",
			"hooks":   []interface{}{handler("PostToolUse")},
		}},
		"Stop": []interface{}{handler("Stop")},
	}
}

func (a *AntigravityTool) Install(cfg config.Config) error {
	hooksCfg, err := readJSONFile(a.hooksConfigPath())
	if err != nil {
		return err
	}
	hooksCfg[antigravityHookSet] = a.hookSet()

	if cfg.DryRun {
		fmt.Printf("[dry-run] Would write hook script to %s\n", a.hookScriptPath())
		fmt.Printf("[dry-run] Would write %s\n", hooks.EnvFilePath())
		fmt.Printf("[dry-run] Would set %q in %s: %v\n", antigravityHookSet, a.hooksConfigPath(), antigravityEvents)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(a.hookScriptPath()), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	if err := os.WriteFile(a.hookScriptPath(), []byte(hooks.AntigravityHookScript), 0755); err != nil {
		return fmt.Errorf("writing hook script: %w", err)
	}
	if err := hooks.WriteEnvFile(cfg); err != nil {
		return err
	}

	return writeJSONFile(a.hooksConfigPath(), hooksCfg, 0644)
}

func (a *AntigravityTool) Remove() error {
	_ = os.Remove(a.hookScriptPath())

	hooksCfg, err := readJSONFile(a.hooksConfigPath())
	if err != nil {
		return err
	}
	if _, ok := hooksCfg[antigravityHookSet]; !ok {
		return nil
	}
	delete(hooksCfg, antigravityHookSet)

	return writeJSONFile(a.hooksConfigPath(), hooksCfg, 0644)
}

func (a *AntigravityTool) registered() bool {
	hooksCfg, err := readJSONFile(a.hooksConfigPath())
	if err != nil {
		return false
	}
	set, ok := hooksCfg[antigravityHookSet].(map[string]interface{})
	if !ok {
		return false
	}
	enabled, _ := set["enabled"].(bool)
	return enabled && set["PostToolUse"] != nil && set["Stop"] != nil
}

func (a *AntigravityTool) Status() ToolStatus {
	st := ToolStatus{Detected: a.Detect()}
	if !st.Detected {
		return st
	}
	st.Configured = a.registered()
	return st
}

func (a *AntigravityTool) Verify(cfg config.Config) VerifyResult {
	return verifyHookTool(a.Detect(), "Antigravity", a.hookScriptPath(), a.hooksConfigPath(), a.registered(),
		"hooks.json is read once at startup: restart Antigravity after install",
		"Antigravity's hook payloads are only partly documented; edit line counts are best effort and prompts are not reported (no prompt hook)")
}
