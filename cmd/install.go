package cmd

import (
	"fmt"
	"os"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/hooks"
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
receiver; gRPC is never configured. The key is scoped to each tool (its own
settings, or a wrapper function in the shell profile); it is never exported
as OTEL_EXPORTER_OTLP_HEADERS, which every OpenTelemetry SDK would send to
its own collector.

The retired endpoint https://otel.flowstate.inc is ignored (treated as unset)
and every file that named it is rewritten.

On macOS, --gui-env also publishes FLOWSTATE_OTLP_ENDPOINT and
FLOWSTATE_OTLP_KEY with 'launchctl setenv' and installs a LaunchAgent that
repeats it at login. Hook scripts already read ~/.config/flowstate-telemetry/env,
so this is rarely needed; the variables are visible to every GUI app you run,
so it is opt-in. It also passes the key on a command line ('launchctl setenv'
now, and the LaunchAgent's 'sh -c' at every login), where other processes can
briefly read it with ps.

The endpoint must be an https URL (http only for localhost); the key, team id
and e-mail may not contain control characters.`,
	RunE: runInstall,
}

func init() {
	installCmd.Flags().StringVar(&flagInstallTool, "tool", "", "Install only this tool (e.g. claude-code, copilot-chat)")
	installCmd.Flags().BoolVar(&flagInstallAll, "all", false, "Install for all detected tools")
	installCmd.Flags().BoolVar(&flagInstallDryRun, "dry-run", false, "Show what would be changed without writing")
	installCmd.Flags().BoolVar(&flagInstallPrompts, "prompts", false, "Log prompt/response content where supported (not recommended: Flowstate drops it, so it only leaves the machine)")
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
	if err := cfg.Validate(true); err != nil {
		return err
	}

	if cfg.GUIEnv && !shell.GUIEnvSupported() {
		return fmt.Errorf("--gui-env is only supported on macOS")
	}

	if cfg.Key == "" && !cfg.DryRun {
		fmt.Fprintln(os.Stderr, "Warning: no API key set. Use --key or FLOWSTATE_OTLP_KEY env var.")
	}
	if cfg.IgnoredLegacyEndpoint != "" {
		fmt.Fprintf(os.Stderr, "Note: ignoring the retired endpoint %s; using %s\n", cfg.IgnoredLegacyEndpoint, cfg.BaseEndpoint())
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
			if n, ok := t.(tools.Noter); ok {
				for _, note := range n.InstallNotes(cfg) {
					fmt.Printf("%s  note: %s\n", prefix, note)
				}
			}
		}
	}

	// Hook scripts in editors and the shell profile block read the key from
	// this file (mode 0600), so the key is never written into the profile.
	// Without a key this refreshes an existing file (it may name the retired
	// endpoint) and keeps the key already in it.
	_, statErr := os.Stat(hooks.EnvFilePath())
	if cfg.Key != "" || statErr == nil {
		if cfg.DryRun {
			fmt.Printf("  [dry-run] Would rewrite %s\n", hooks.EnvFilePath())
		} else if err := hooks.WriteEnvFile(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not rewrite %s: %v\n", hooks.EnvFilePath(), err)
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

	// A LaunchAgent from an earlier install may publish the retired endpoint
	// and OTEL_EXPORTER_OTLP_HEADERS: refresh it even without --gui-env.
	if cfg.GUIEnv || (shell.GUIEnvSupported() && shell.GUIEnvInstalled()) {
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
		fmt.Println("Open a new terminal (or run 'source " + shell.ProfilePath() + "') to load the env vars and wrapper functions.")
	}

	if failed > 0 {
		return fmt.Errorf("%d tool(s) failed to configure", failed)
	}

	return nil
}
