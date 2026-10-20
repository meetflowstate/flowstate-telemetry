package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
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
configuration from AI coding tools and cleaning up shell environment variables.

--all cleans every tool's files whether or not the tool is still installed or
detected, so no key is left behind in a settings file.`,
	RunE: runRemove,
}

func init() {
	removeCmd.Flags().StringVar(&flagRemoveTool, "tool", "", "Remove only this tool (e.g. claude-code, copilot-chat)")
	removeCmd.Flags().BoolVar(&flagRemoveAll, "all", false, "Remove from every tool, detected or not")
	rootCmd.AddCommand(removeCmd)
}

func runRemove(cmd *cobra.Command, args []string) error {
	if !flagRemoveAll && flagRemoveTool == "" {
		return fmt.Errorf("specify --tool <name> or --all")
	}

	var toolsToRemove []tools.Tool
	// Configured before removal, for the report: with --all every tool is
	// cleaned, detected or not (the key may sit in ~/.claude/settings.json
	// after the claude command itself was uninstalled). Each Remove is a
	// no-op when none of its files exist.
	configured := map[string]bool{}

	if flagRemoveAll {
		toolsToRemove = tools.All()
	} else {
		t, ok := tools.ByName(flagRemoveTool)
		if !ok {
			return fmt.Errorf("unknown tool: %s", flagRemoveTool)
		}
		toolsToRemove = append(toolsToRemove, t)
	}
	for _, t := range toolsToRemove {
		configured[t.Name()] = !flagRemoveAll || t.Status().Configured
	}

	var succeeded, failed int

	for _, t := range toolsToRemove {
		if err := t.Remove(); err != nil {
			// A file --all cannot parse, for a tool not detected as
			// configured, holds nothing of ours that we could find: warn and
			// leave it. A named or configured tool still fails.
			if flagRemoveAll && !configured[t.Name()] && errors.Is(err, tools.ErrUnparseable) {
				fmt.Fprintf(os.Stderr, "  %s: warning: %v (not configured; left unchanged)\n", t.Name(), err)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: error: %v\n", t.Name(), err)
			failed++
			continue
		}
		switch {
		case configured[t.Name()]:
			fmt.Printf("  %s: removed\n", t.Name())
			succeeded++
		case flagVerbose:
			fmt.Printf("  %s: cleaned (not detected as configured)\n", t.Name())
		}
	}

	// Remove shell env vars
	if flagRemoveAll {
		if err := shell.RemoveEnvVars(); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not clean shell profile: %v\n", err)
		} else {
			fmt.Printf("  Shell env vars removed from %s\n", shell.ProfilePath())
		}
		if err := hooks.RemoveEnvFile(); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not remove %s: %v\n", hooks.EnvFilePath(), err)
		}
		if err := shell.RemoveGUIEnv(); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not remove GUI env vars: %v\n", err)
		}
	}

	fmt.Println()
	fmt.Printf("Summary: %d removed, %d failed\n", succeeded, failed)

	if failed > 0 {
		return fmt.Errorf("%d tool(s) failed to remove", failed)
	}

	return nil
}
