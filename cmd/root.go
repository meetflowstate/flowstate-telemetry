package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	flagEndpoint string
	flagKey      string
	flagVerbose  bool
)

// Build metadata. Set by goreleaser via -ldflags at release time:
//
//	-X github.com/meetflowstate/flowstate-telemetry/cmd.version={{.Version}}
//	-X github.com/meetflowstate/flowstate-telemetry/cmd.commit={{.Commit}}
//	-X github.com/meetflowstate/flowstate-telemetry/cmd.date={{.Date}}
//
// Defaults are used when the binary is built locally without ldflags
// (e.g. `go build`, `go run`) so `--version` always returns something
// useful instead of an empty string.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// VersionString returns a human-readable build identifier.
//
// Format: "<version> (<short-commit>, built <date>, <go-version>, <os>/<arch>)"
//
// Used by `flowstate-telemetry --version`, the `version` subcommand, and
// any future support-bundle / diagnostics output.
func VersionString() string {
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf(
		"%s (%s, built %s, %s, %s/%s)",
		version, short, date, runtime.Version(), runtime.GOOS, runtime.GOARCH,
	)
}

var rootCmd = &cobra.Command{
	Use:     "flowstate-telemetry",
	Short:   "Configure developer machines to emit AI coding tool telemetry to Flowstate",
	Version: VersionString(),
	Long: `flowstate-telemetry configures developer machines to emit OpenTelemetry
data from AI coding tools (Claude Code, Copilot Chat, Gemini CLI, Codex,
Cursor, Windsurf, Aider, Qwen Code, OpenCode) to Flowstate's OTel collector.

It can be run interactively on a developer's machine or used to generate
MDM deployment artefacts for fleet-wide rollout.`,
}

// versionCmd exposes the build metadata as an explicit subcommand for
// callers (and MDM scripts) that prefer `flowstate-telemetry version`
// over `--version`. Both produce the same output.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version, commit, and build date",
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), VersionString())
	},
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

	// Cobra's default --version template prints "<binary> version <Version>".
	// Override to print just the VersionString so it's easy to parse in
	// shell scripts (`if [[ "$(flowstate-telemetry --version)" == 1.1.0* ]]`).
	rootCmd.SetVersionTemplate("{{.Version}}\n")

	rootCmd.AddCommand(versionCmd)
}
