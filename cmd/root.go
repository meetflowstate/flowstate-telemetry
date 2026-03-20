package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	flagEndpoint string
	flagKey      string
	flagVerbose  bool
)

var rootCmd = &cobra.Command{
	Use:   "flowstate-telemetry",
	Short: "Configure developer machines to emit AI coding tool telemetry to Flowstate",
	Long: `flowstate-telemetry configures developer machines to emit OpenTelemetry
data from AI coding tools (Claude Code, Copilot Chat, Gemini CLI, Codex,
Cursor, Windsurf, Aider, Qwen Code, OpenCode) to Flowstate's OTel collector.

It can be run interactively on a developer's machine or used to generate
MDM deployment artefacts for fleet-wide rollout.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagEndpoint, "endpoint", "", "OTel collector endpoint (env: FLOWSTATE_OTLP_ENDPOINT)")
	rootCmd.PersistentFlags().StringVar(&flagKey, "key", "", "Flowstate API key (env: FLOWSTATE_OTLP_KEY)")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "Enable debug logging")
}
