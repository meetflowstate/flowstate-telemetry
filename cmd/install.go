package cmd

import (
	"fmt"
	"os"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
	"github.com/meetflowstate/flowstate-telemetry/internal/tools"
	"github.com/spf13/cobra"
)

var (
	flagInstallTool    string
	flagInstallAll     bool
	flagInstallDryRun  bool
	flagInstallPrompts bool
	flagInstallGUIEnv  bool
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Configure AI tool telemetry for detected tools",
	Long: `Install configures telemetry for AI coding tools on this machine.

Use --all to configure every detected tool, or --tool <name> to target
a specific tool. Use --dry-run to preview changes without writing files.

Every tool sends OTLP over HTTP (protobuf or JSON) to the Flowstate
receiver; gRPC is never configured.

On macOS, apps opened from the Dock (VS Code, Cursor, Windsurf) do not read
the shell profile. --gui-env also publishes FLOWSTATE_OTLP_ENDPOINT,
FLOWSTATE_OTLP_KEY and OTEL_EXPORTER_OTLP_HEADERS with 'launchctl setenv'
and installs a LaunchAgent that repeats it at login. Those variables are
then visible to every GUI app you run, so it is opt-in.`,
	RunE: runInstall,
}

func init() {
	installCmd.Flags().StringVar(&flagInstallTool, "tool", "", "Install only this tool (e.g. claude-code, copilot-chat)")
	installCmd.Flags().BoolVar(&flagInstallAll, "all", false, "Install for all detected tools")
	installCmd.Flags().BoolVar(&flagInstallDryRun, "dry-run", false, "Show what would be changed without writing")
	installCmd.Flags().BoolVar(&flagInstallPrompts, "prompts", false, "Enable prompt/content capture where supported")
	installCmd.Flags().BoolVar(&flagInstallGUIEnv, "gui-env", false, "macOS: also expose the env vars to Dock-launched apps via launchctl setenv")
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	if !flagInstallAll && flagInstallTool == "" {
		return fmt.Errorf("specify --tool <name> or --all")
	}

	cfg := config.FromEnv(flagEndpoint, flagKey)
	cfg.Prompts = flagInstallPrompts
	cfg.DryRun = flagInstallDryRun
	cfg.Verbose = flagVerbose
	cfg.GUIEnv = flagInstallGUIEnv

	if cfg.GUIEnv && !shell.GUIEnvSupported() {
		return fmt.Errorf("--gui-env is only supported on macOS")
	}

	if cfg.Key == "" && !cfg.DryRun {
		fmt.Fprintln(os.Stderr, "Warning: no API key set. Use --key or FLOWSTATE_OTLP_KEY env var.")
	}

	var toolsToInstall []tools.Tool

	if flagInstallAll {
		for _, t := range tools.All() {
			if t.Detect() {
				toolsToInstall = append(toolsToInstall, t)
			} else if flagVerbose {
				fmt.Printf("  Skipping %s (not detected)\n", t.Name())
			}
		}
	} else {
		t, ok := tools.ByName(flagInstallTool)
		if !ok {
			return fmt.Errorf("unknown tool: %s", flagInstallTool)
		}
		toolsToInstall = append(toolsToInstall, t)
	}

	if len(toolsToInstall) == 0 {
		fmt.Println("No tools detected. Nothing to configure.")
		return nil
	}

	var succeeded, failed int

	for _, t := range toolsToInstall {
		prefix := "  "
		if cfg.DryRun {
			prefix = "  [dry-run] "
		}

		if err := t.Install(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "%s%s: error: %v\n", prefix, t.Name(), err)
			failed++
		} else {
			fmt.Printf("%s%s: configured\n", prefix, t.Name())
			succeeded++
		}
	}

	// Write shell env vars
	fmt.Println()
	if err := shell.WriteEnvVars(cfg, cfg.DryRun); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not write shell profile: %v\n", err)
	} else {
		profilePath := shell.ProfilePath()
		if cfg.DryRun {
			fmt.Printf("  [dry-run] Shell env vars would be written to %s\n", profilePath)
		} else {
			fmt.Printf("  Shell env vars written to %s\n", profilePath)
		}
	}

	if cfg.GUIEnv {
		if err := shell.InstallGUIEnv(cfg, cfg.DryRun); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not set GUI environment: %v\n", err)
		} else if !cfg.DryRun {
			fmt.Printf("  GUI env vars set via launchctl (LaunchAgent %s); restart GUI apps to pick them up\n", shell.GUIEnvAgentPath())
		}
	}

	// Summary
	fmt.Println()
	fmt.Printf("Summary: %d configured, %d failed\n", succeeded, failed)
	if !cfg.DryRun {
		fmt.Println("Run 'flowstate-telemetry status' to verify configuration.")
		fmt.Println("Run 'source " + shell.ProfilePath() + "' to load env vars in current shell.")
	}

	if failed > 0 {
		return fmt.Errorf("%d tool(s) failed to configure", failed)
	}

	return nil
}
