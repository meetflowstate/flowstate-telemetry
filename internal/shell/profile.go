package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
)

const (
	markerStart = "# --- Flowstate Telemetry ---"
	markerEnd   = "# --- End Flowstate Telemetry ---"
)

// ProfilePath returns the path to the user's shell profile file,
// detecting the current shell from the SHELL environment variable.
func ProfilePath() string {
	home, _ := os.UserHomeDir()
	shell := os.Getenv("SHELL")

	switch {
	case strings.HasSuffix(shell, "/zsh"):
		return filepath.Join(home, ".zshrc")
	case strings.HasSuffix(shell, "/bash"):
		return filepath.Join(home, ".bashrc")
	default:
		// Default to .profile for unknown shells
		return filepath.Join(home, ".profile")
	}
}

// WriteEnvVars writes Flowstate telemetry environment variables to the
// user's shell profile, surrounded by marker comments for easy identification
// and removal.
func WriteEnvVars(cfg config.Config, dryRun bool) error {
	return writeBlock(markerStart, markerEnd, buildEnvBlock(cfg), dryRun, cfg.Key)
}

// RemoveEnvVars removes the Flowstate telemetry block, and every tool block
// written by WriteToolBlock, from the shell profile.
func RemoveEnvVars() error {
	if err := removeBlock(markerStart, markerEnd); err != nil {
		return err
	}
	data, err := os.ReadFile(ProfilePath())
	if err != nil {
		return nil
	}
	for _, m := range toolMarkerPattern.FindAllStringSubmatch(string(data), -1) {
		if err := RemoveToolBlock(m[1]); err != nil {
			return err
		}
	}
	return nil
}

var toolMarkerPattern = regexp.MustCompile(`(?m)^# --- Flowstate Telemetry: ([A-Za-z0-9_.-]+) ---$`)

// WriteToolBlock writes a block owned by one tool (for example a wrapper
// function that scopes OTEL_* variables to a single command) to the shell
// profile, replacing any previous block for that tool.
func WriteToolBlock(tool string, lines []string, dryRun bool) error {
	start, end := toolMarkers(tool)
	block := strings.Join(append(append([]string{start}, lines...), end), "\n")
	return writeBlock(start, end, block, dryRun, "")
}

// RemoveToolBlock removes the block written by WriteToolBlock for tool.
func RemoveToolBlock(tool string) error {
	start, end := toolMarkers(tool)
	return removeBlock(start, end)
}

// HasToolBlock reports whether the shell profile contains tool's block.
func HasToolBlock(tool string) bool {
	start, _ := toolMarkers(tool)
	data, err := os.ReadFile(ProfilePath())
	return err == nil && strings.Contains(string(data), start)
}

// ToolBlock returns the text of tool's block in the shell profile, or "".
func ToolBlock(tool string) string {
	start, end := toolMarkers(tool)
	return blockText(ProfileText(), start, end)
}

// MainBlock returns the text of the shared Flowstate block, or "".
func MainBlock() string {
	return blockText(ProfileText(), markerStart, markerEnd)
}

// ProfileText returns the shell profile's contents ("" when missing).
func ProfileText() string {
	data, _ := os.ReadFile(ProfilePath())
	return string(data)
}

func blockText(content, start, end string) string {
	i := strings.Index(content, start)
	if i == -1 {
		return ""
	}
	j := strings.Index(content[i:], end)
	if j == -1 {
		return ""
	}
	return content[i : i+j+len(end)]
}

func toolMarkers(tool string) (string, string) {
	return fmt.Sprintf("# --- Flowstate Telemetry: %s ---", tool),
		fmt.Sprintf("# --- End Flowstate Telemetry: %s ---", tool)
}

// writeBlock replaces the block between start and end. secret is redacted
// from dry-run output.
func writeBlock(start, end, block string, dryRun bool, secret string) error {
	profilePath := ProfilePath()

	if dryRun {
		shown := block
		if secret != "" {
			shown = strings.ReplaceAll(shown, secret, "<redacted>")
		}
		fmt.Printf("[dry-run] Would append to %s:\n%s\n", profilePath, shown)
		return nil
	}

	existing, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading profile: %w", err)
	}

	content := removeExistingBlock(string(existing), start, end)

	// A blank line separates the block from the user's own lines;
	// removeExistingBlock drops it again.
	if len(content) > 0 {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n"
	}
	content += block + "\n"

	// The profile must stay readable by the user's shell; it holds no
	// secret (the key is read from the 0600 env file). An existing file
	// keeps its mode.
	return os.WriteFile(profilePath, []byte(content), 0644)
}

func removeBlock(start, end string) error {
	profilePath := ProfilePath()

	data, err := os.ReadFile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading profile: %w", err)
	}

	content := removeExistingBlock(string(data), start, end)

	return os.WriteFile(profilePath, []byte(content), 0644)
}

// KeyLoader returns the profile line that loads FLOWSTATE_OTLP_KEY from the
// env file at envFile (mode 0600) when the shell starts, so the key is never
// written into the profile itself, which is usually group/world-readable.
// envFile "" means the default location resolved at run time
// (${XDG_CONFIG_HOME:-$HOME/.config}/flowstate-telemetry/env, as the hook
// scripts do). It is one POSIX sh line with no subprocess, so bash, zsh and
// sh profiles can all use it; with envFile "" it has no single quotes either,
// so MDM templates can embed it in single-quoted strings. The file is parsed,
// never sourced.
func KeyLoader(envFile string) string {
	path := `"${XDG_CONFIG_HOME:-$HOME/.config}/flowstate-telemetry/env"`
	if envFile != "" {
		path = shQuote(envFile)
	}
	return "_fs_env=" + path + `; if [ -r "$_fs_env" ]; then while IFS= read -r _fs_l || [ -n "$_fs_l" ]; do case "$_fs_l" in FLOWSTATE_OTLP_KEY=*) export FLOWSTATE_OTLP_KEY="${_fs_l#FLOWSTATE_OTLP_KEY=}"; break ;; esac; done < "$_fs_env"; fi; unset _fs_env _fs_l`
}

// buildEnvBlock renders the shared profile block.
//
// The key itself is not in the block: it lives in the hook env file
// (hooks.EnvFilePath, mode 0600), written by install, and the block loads it
// from there (KeyLoader).
//
// Only FLOWSTATE_* variables carry the endpoint and key: no OpenTelemetry SDK
// reads them. OTEL_EXPORTER_OTLP_HEADERS is never exported, because every
// OTel SDK started from the shell would then send the Flowstate key to its
// own collector (and a user's own value would be clobbered). Each tool gets
// the key from its own settings, or from a wrapper function scoped to that
// one command (WriteToolBlock).
//
// OTEL_RESOURCE_ATTRIBUTES is not exported either: it would attach
// flowstate.email and flowstate.team_id to every OTel program's telemetry,
// whatever collector it goes to. Tools get it scoped, like the key.
func buildEnvBlock(cfg config.Config) string {
	var lines []string
	lines = append(lines, markerStart)
	lines = append(lines, fmt.Sprintf("export FLOWSTATE_OTLP_ENDPOINT=%q", cfg.BaseEndpoint()))

	lines = append(lines, "# FLOWSTATE_OTLP_KEY is read from the env file (mode 0600), not stored here.")
	lines = append(lines, KeyLoader(hooks.EnvFilePath()))

	lines = append(lines, markerEnd)

	return strings.Join(lines, "\n")
}

// ResourceAttributes returns the OTEL_RESOURCE_ATTRIBUTES value (Flowstate
// team and email only), or "" when neither is set. It deliberately carries
// no service.name: each tool sets its own. Callers scope it to one tool
// (settings or a wrapper function); it is never exported globally.
func ResourceAttributes(cfg config.Config) string {
	var attrs []string
	if cfg.TeamID != "" {
		attrs = append(attrs, fmt.Sprintf("flowstate.team_id=%s", cfg.TeamID))
	}
	if cfg.Email != "" {
		attrs = append(attrs, fmt.Sprintf("flowstate.email=%s", cfg.Email))
	}
	return strings.Join(attrs, ",")
}

func removeExistingBlock(content, start, end string) string {
	startIdx := strings.Index(content, start)
	if startIdx == -1 {
		return content
	}

	rel := strings.Index(content[startIdx:], end)
	if rel == -1 {
		return content
	}

	endIdx := startIdx + rel + len(end)

	// The block's own line break.
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}

	// The blank separator line writeBlock puts before the block: drop it
	// only when the line before the block really is empty. Dropping the
	// previous line's own newline would join it to the line after the block.
	if startIdx >= 1 && content[startIdx-1] == '\n' && (startIdx == 1 || content[startIdx-2] == '\n') {
		startIdx--
	}

	return content[:startIdx] + content[endIdx:]
}

// EnvAssignment renders NAME='value' for a wrapper function, single-quoting
// value so it is used literally.
func EnvAssignment(name, value string) string {
	return name + "=" + shQuote(value)
}

// WrapperFunction renders a shell function that runs the real command with
// extra environment assignments, so OTEL_* variables reach that one tool and
// not every OpenTelemetry SDK started from the shell. zsh and bash use the
// `function` keyword so an existing alias of the same name cannot break the
// profile; other shells get the POSIX form.
func WrapperFunction(command string, assignments []string) string {
	body := fmt.Sprintf("%s command %s \"$@\"; }", strings.Join(assignments, " "), command)
	p := ProfilePath()
	if strings.HasSuffix(p, ".zshrc") || strings.HasSuffix(p, ".bashrc") {
		return fmt.Sprintf("function %s { %s", command, body)
	}
	return fmt.Sprintf("%s() { %s", command, body)
}
