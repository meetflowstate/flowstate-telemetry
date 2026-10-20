package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
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
	return writeBlock(markerStart, markerEnd, buildEnvBlock(cfg), dryRun)
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
	return writeBlock(start, end, block, dryRun)
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

func toolMarkers(tool string) (string, string) {
	return fmt.Sprintf("# --- Flowstate Telemetry: %s ---", tool),
		fmt.Sprintf("# --- End Flowstate Telemetry: %s ---", tool)
}

func writeBlock(start, end, block string, dryRun bool) error {
	profilePath := ProfilePath()

	if dryRun {
		fmt.Printf("[dry-run] Would append to %s:\n%s\n", profilePath, block)
		return nil
	}

	existing, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading profile: %w", err)
	}

	content := removeExistingBlock(string(existing), start, end)

	if !strings.HasSuffix(content, "\n") && len(content) > 0 {
		content += "\n"
	}
	content += "\n" + block + "\n"

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

// buildEnvBlock renders the shared profile block.
//
// OTEL_EXPORTER_OTLP_HEADERS is exported because Copilot Chat (and the
// Gemini/Qwen CLIs, as a fallback) can only take OTLP headers from the
// environment. Every Flowstate-configured tool uses the same key, so sharing
// it is safe for them; tools with their own header config (Claude Code,
// Codex, OpenCode) override it.
//
// OTEL_RESOURCE_ATTRIBUTES deliberately carries no service.name: each tool
// sets its own, and overriding it would make every tool look the same.
func buildEnvBlock(cfg config.Config) string {
	var lines []string
	lines = append(lines, markerStart)
	lines = append(lines, fmt.Sprintf("export FLOWSTATE_OTLP_ENDPOINT=%q", cfg.BaseEndpoint()))

	if cfg.Key != "" {
		lines = append(lines, fmt.Sprintf("export FLOWSTATE_OTLP_KEY=%q", cfg.Key))
		lines = append(lines, fmt.Sprintf("export OTEL_EXPORTER_OTLP_HEADERS=%q", cfg.HeadersEnv()))
	}

	if attrs := ResourceAttributes(cfg); attrs != "" {
		lines = append(lines, fmt.Sprintf("export OTEL_RESOURCE_ATTRIBUTES=%q", attrs))
	}
	lines = append(lines, markerEnd)

	return strings.Join(lines, "\n")
}

// ResourceAttributes returns the OTEL_RESOURCE_ATTRIBUTES value (Flowstate
// team and email only), or "" when neither is set.
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

	// Also remove trailing newline after the block
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}

	// Remove leading newline before the block
	if startIdx > 0 && content[startIdx-1] == '\n' {
		startIdx--
	}

	return content[:startIdx] + content[endIdx:]
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
