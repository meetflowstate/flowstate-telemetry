package cmd

import (
	"fmt"
	"os"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/tools"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify telemetry configuration for all detected tools",
	Long: `Verify checks this machine's configuration without sending anything:
the environment (left-over global OTEL_* variables, the retired endpoint, the
shell profile, the --gui-env LaunchAgent, machine-wide files) and, for each
detected AI tool, its local settings files and wrapper functions.

Exit code 0 if every check passes. Exit code 1 if any check fails or has a
warning.`,
	RunE: runVerify,
}

func init() {
	rootCmd.AddCommand(verifyCmd)
}

func runVerify(cmd *cobra.Command, args []string) error {
	cfg := config.FromEnv(flagEndpoint, flagKey)
	cfg.Verbose = flagVerbose

	hasErrors := false
	hasWarnings := false

	report := func(name string, result tools.VerifyResult) {
		if result.OK {
			if result.Warning != "" {
				fmt.Printf("\u26a0  %-16s %s\n", name, result.Warning)
				hasWarnings = true
			} else {
				fmt.Printf("\u2713  %-16s %s\n", name, result.Message)
			}
		} else {
			fmt.Printf("\u2717  %-16s %s\n", name, result.Message)
			if result.Warning != "" {
				fmt.Printf("\u26a0  %-16s %s\n", "", result.Warning)
			}
			hasErrors = true
		}
		for _, note := range result.Notes {
			fmt.Printf("   %-16s note: %s\n", "", note)
		}
	}

	if cfg.IgnoredLegacyEndpoint != "" {
		fmt.Printf("   %-16s note: ignoring retired endpoint %s; checking against %s\n", "", cfg.IgnoredLegacyEndpoint, cfg.BaseEndpoint())
	}
	report("environment", tools.VerifyEnvironment(cfg))

	for _, t := range tools.All() {
		if !t.Detect() {
			fmt.Printf("\u2013  %-16s not detected on PATH\n", t.Name())
			continue
		}
		report(t.Name(), t.Verify(cfg))
	}

	if hasErrors || hasWarnings {
		fmt.Fprintln(os.Stderr)
		if hasErrors {
			fmt.Fprintln(os.Stderr, "Some checks failed. Run 'flowstate-telemetry install --all' to fix.")
		}
		if hasWarnings {
			fmt.Fprintln(os.Stderr, "Some checks have warnings; see the lines marked \u26a0 above.")
		}
		os.Exit(1)
	}

	return nil
}
