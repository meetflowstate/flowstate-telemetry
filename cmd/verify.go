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
	Long: `Verify checks the configuration of each detected AI tool and
reports whether telemetry is correctly configured. For tools with OTel
endpoints, it optionally sends a test event.

Exit code 0 if all detected tools pass. Exit code 1 if any have warnings or errors.`,
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

	for _, t := range tools.All() {
		if !t.Detect() {
			fmt.Printf("\u2013  %-16s not detected on PATH\n", t.Name())
			continue
		}

		result := t.Verify(cfg)

		if result.OK {
			if result.Warning != "" {
				fmt.Printf("\u26a0  %-16s %s\n", t.Name(), result.Warning)
				hasWarnings = true
			} else {
				fmt.Printf("\u2713  %-16s %s\n", t.Name(), result.Message)
			}
		} else {
			fmt.Printf("\u2717  %-16s %s\n", t.Name(), result.Message)
			hasErrors = true
		}
	}

	if hasErrors || hasWarnings {
		fmt.Fprintln(os.Stderr)
		if hasErrors {
			fmt.Fprintln(os.Stderr, "Some tools have configuration errors. Run 'flowstate-telemetry install --all' to fix.")
		}
		if hasWarnings {
			fmt.Fprintln(os.Stderr, "Some tools have warnings. Run 'flowstate-telemetry install --all --prompts' for full capture.")
		}
		os.Exit(1)
	}

	return nil
}
