package shell

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// GUI apps on macOS (VS Code, Cursor, Windsurf launched from the Dock or
// Spotlight) do not read the shell profile. `--gui-env` publishes the
// FLOWSTATE_* variables to the user's launchd session with `launchctl setenv`
// and drops a LaunchAgent that repeats it at every login. It is opt-in:
// launchd variables are visible to every GUI app the user runs.
//
// OTEL_EXPORTER_OTLP_HEADERS is never published (earlier releases did): every
// OTel SDK in every GUI app would send the Flowstate key to its own collector.
// Installing and removing unset it when it carries x-flowstate-key (a value
// the user set for their own collector is left alone), and a refreshed
// LaunchAgent no longer sets it.

const guiEnvLabel = "inc.flowstate.telemetry.env"

// goos, runCommand and launchctlGetenv are swapped out by tests.
var (
	goos       = runtime.GOOS
	runCommand = func(name string, args ...string) error {
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	launchctlGetenv = func(name string) string {
		out, err := exec.Command("launchctl", "getenv", name).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
)

// unsetLegacyHeaders unsets the launchd OTEL_EXPORTER_OTLP_HEADERS that
// earlier releases published, only when it carries the Flowstate key.
func unsetLegacyHeaders() {
	if strings.Contains(launchctlGetenv("OTEL_EXPORTER_OTLP_HEADERS"), config.AuthHeader) {
		_ = runCommand("launchctl", "unsetenv", "OTEL_EXPORTER_OTLP_HEADERS")
	}
}

// GUIEnvAgentPath returns the LaunchAgent plist written by InstallGUIEnv.
func GUIEnvAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", guiEnvLabel+".plist")
}

// guiEnvVars lists the variables GUI apps need, in a stable order.
func guiEnvVars(cfg config.Config) [][2]string {
	vars := [][2]string{{"FLOWSTATE_OTLP_ENDPOINT", cfg.BaseEndpoint()}}
	if cfg.Key != "" {
		vars = append(vars, [2]string{"FLOWSTATE_OTLP_KEY", cfg.Key})
	}
	return vars
}

// GUIEnvSupported reports whether --gui-env can be used on this OS.
func GUIEnvSupported() bool { return goos == "darwin" }

// InstallGUIEnv sets the Flowstate variables for the current launchd user
// session and installs a LaunchAgent that sets them again at login. Apps
// already running must be restarted to see them.
func InstallGUIEnv(cfg config.Config, dryRun bool) error {
	if !GUIEnvSupported() {
		return fmt.Errorf("--gui-env is only supported on macOS")
	}
	vars := guiEnvVars(cfg)
	plist := guiEnvPlist(vars)
	if dryRun {
		shown := plist
		if cfg.Key != "" {
			shown = strings.ReplaceAll(plist, cfg.Key, "<redacted>")
		}
		fmt.Printf("[dry-run] Would write %s:\n%s\n", GUIEnvAgentPath(), shown)
		fmt.Println("[dry-run] Would run: launchctl unsetenv OTEL_EXPORTER_OTLP_HEADERS (only if it carries " + config.AuthHeader + ")")
		for _, kv := range vars {
			fmt.Printf("[dry-run] Would run: launchctl setenv %s <value>\n", kv[0])
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(GUIEnvAgentPath()), 0755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(GUIEnvAgentPath(), []byte(plist), 0600); err != nil {
		return fmt.Errorf("writing LaunchAgent: %w", err)
	}
	// Set by earlier releases.
	unsetLegacyHeaders()
	for _, kv := range vars {
		if err := runCommand("launchctl", "setenv", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// RemoveGUIEnv unsets the launchd variables and deletes the LaunchAgent.
// It is a no-op when --gui-env was never used.
func RemoveGUIEnv() error {
	if !GUIEnvSupported() || !GUIEnvInstalled() {
		return nil
	}
	for _, name := range []string{"FLOWSTATE_OTLP_ENDPOINT", "FLOWSTATE_OTLP_KEY"} {
		_ = runCommand("launchctl", "unsetenv", name)
	}
	unsetLegacyHeaders()
	if err := os.Remove(GUIEnvAgentPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing LaunchAgent: %w", err)
	}
	return nil
}

// GUIEnvInstalled reports whether the --gui-env LaunchAgent is present.
func GUIEnvInstalled() bool {
	_, err := os.Stat(GUIEnvAgentPath())
	return err == nil
}

// GUIEnvAgentText returns the LaunchAgent plist ("" when absent), for verify.
func GUIEnvAgentText() string {
	data, _ := os.ReadFile(GUIEnvAgentPath())
	return string(data)
}

func guiEnvPlist(vars [][2]string) string {
	var script []string
	for _, kv := range vars {
		script = append(script, fmt.Sprintf("/bin/launchctl setenv %s %s", kv[0], shQuote(kv[1])))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/sh</string>
        <string>-c</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`, guiEnvLabel, xmlEscape(strings.Join(script, "; ")))
}

// shQuote single-quotes s for /bin/sh.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
