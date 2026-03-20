package cmd

import (
	"fmt"

	"github.com/meetflowstate/flowstate-telemetry/internal/tools"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show telemetry configuration status for all AI tools",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	fmt.Printf("%-18s %-10s %-12s %-8s\n", "Tool", "Detected", "Configured", "Prompts")
	fmt.Println("────────────────────────────────────────────────────")

	for _, t := range tools.All() {
		st := t.Status()

		detected := symbolFor(st.Detected, false)
		configured := configuredSymbol(st.Detected, st.Configured)
		prompts := promptsSymbol(st.Detected, st.Configured, st.Prompts)

		fmt.Printf("%-18s %-10s %-12s %-8s\n", t.Name(), detected, configured, prompts)
	}

	return nil
}

func symbolFor(val bool, _ bool) string {
	if val {
		return "\u2713"
	}
	return "\u2717"
}

func configuredSymbol(detected, configured bool) string {
	if !detected {
		return "\u2013"
	}
	if configured {
		return "\u2713"
	}
	return "\u2717"
}

func promptsSymbol(detected, configured, prompts bool) string {
	if !detected {
		return "\u2013"
	}
	if !configured {
		return "\u2013"
	}
	if prompts {
		return "\u2713"
	}
	return "\u2717"
}
