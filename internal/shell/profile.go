package shell

import (
	"fmt"
	"os"
	"path/filepath"
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
	profilePath := ProfilePath()

	block := buildEnvBlock(cfg)

	if dryRun {
		fmt.Printf("[dry-run] Would append to %s:\n%s\n", profilePath, block)
		return nil
	}

	existing, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading profile: %w", err)
	}

	content := string(existing)

	// Remove existing Flowstate block if present
	content = removeExistingBlock(content)

	// Append new block
	if !strings.HasSuffix(content, "\n") && len(content) > 0 {
		content += "\n"
	}
	content += "\n" + block + "\n"

	return os.WriteFile(profilePath, []byte(content), 0644)
}

// RemoveEnvVars removes the Flowstate telemetry block from the shell profile.
func RemoveEnvVars() error {
	profilePath := ProfilePath()

	data, err := os.ReadFile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading profile: %w", err)
	}

	content := removeExistingBlock(string(data))

	return os.WriteFile(profilePath, []byte(content), 0644)
}

func buildEnvBlock(cfg config.Config) string {
	var lines []string
	lines = append(lines, markerStart)
	lines = append(lines, fmt.Sprintf("export FLOWSTATE_OTLP_ENDPOINT=%q", cfg.Endpoint))

	if cfg.Key != "" {
		lines = append(lines, fmt.Sprintf("export FLOWSTATE_OTLP_KEY=%q", cfg.Key))
	}

	attrs := []string{
		fmt.Sprintf("service.name=flowstate-telemetry"),
	}
	if cfg.TeamID != "" {
		attrs = append(attrs, fmt.Sprintf("flowstate.team_id=%s", cfg.TeamID))
	}
	if cfg.Email != "" {
		attrs = append(attrs, fmt.Sprintf("flowstate.email=%s", cfg.Email))
	}

	lines = append(lines, fmt.Sprintf("export OTEL_RESOURCE_ATTRIBUTES=%q", strings.Join(attrs, ",")))
	lines = append(lines, markerEnd)

	return strings.Join(lines, "\n")
}

func removeExistingBlock(content string) string {
	startIdx := strings.Index(content, markerStart)
	if startIdx == -1 {
		return content
	}

	endIdx := strings.Index(content, markerEnd)
	if endIdx == -1 {
		return content
	}

	endIdx += len(markerEnd)

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
