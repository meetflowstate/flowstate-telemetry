package cmd

import (
	"fmt"
	"os"

	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
	"github.com/meetflowstate/flowstate-telemetry/internal/tools"
	"github.com/spf13/cobra"
)

var (
	flagRemoveTool string
	flagRemoveAll  bool
)

var removeCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove Flowstate telemetry configuration from AI tools",
	Long: `Remove reverses the install command, removing Flowstate telemetry
configuration from AI coding tools and cleaning up shell environment variables.`,
	RunE: runRemove,
}

func init() {
	removeCmd.Flags().StringVar(&flagRemoveTool, "tool", "", "Remove only this tool (e.g. claude-code, copilot-chat)")
	removeCmd.Flags().BoolVar(&flagRemoveAll, "all", false, "Remove from all configured tools")
	rootCmd.AddCommand(removeCmd)
}

func runRemove(cmd *cobra.Command, args []string) error {
	if !flagRemoveAll && flagRemoveTool == "" {
		return fmt.Errorf("specify --tool <name> or --all")
	}

	var toolsToRemove []tools.Tool

	if flagRemoveAll {
		for _, t := range tools.All() {
			st := t.Status()
			if st.Configured {
				toolsToRemove = append(toolsToRemove, t)
			} else if flagVerbose {
				fmt.Printf("  Skipping %s (not configured)\n", t.Name())
			}
		}
	} else {
		t, ok := tools.ByName(flagRemoveTool)
		if !ok {
			return fmt.Errorf("unknown tool: %s", flagRemoveTool)
		}
		toolsToRemove = append(toolsToRemove, t)
	}

	if len(toolsToRemove) == 0 {
		fmt.Println("No configured tools found. Nothing to remove.")
		return nil
	}

	var succeeded, failed int

	for _, t := range toolsToRemove {
		if err := t.Remove(); err != nil {
			fmt.Fprintf(os.Stderr, "  %s: error: %v\n", t.Name(), err)
			failed++
		} else {
			fmt.Printf("  %s: removed\n", t.Name())
			succeeded++
		}
	}

	// Remove shell env vars
	if flagRemoveAll {
		if err := shell.RemoveEnvVars(); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not clean shell profile: %v\n", err)
		} else {
			fmt.Printf("  Shell env vars removed from %s\n", shell.ProfilePath())
		}
	}

	fmt.Println()
	fmt.Printf("Summary: %d removed, %d failed\n", succeeded, failed)

	if failed > 0 {
		return fmt.Errorf("%d tool(s) failed to remove", failed)
	}

	return nil
}
