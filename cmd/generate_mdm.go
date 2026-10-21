package cmd

import (
	"fmt"
	"os"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/mdm"
	"github.com/spf13/cobra"
)

var (
	flagMDMFormat         string
	flagMDMMachineCleanup bool
)

var generateMDMCmd = &cobra.Command{
	Use:   "generate-mdm",
	Short: "Generate MDM deployment artefacts for fleet-wide rollout",
	Long: `Generate MDM deployment artefacts for mass deployment of Flowstate
telemetry configuration across developer machines. Supports Jamf, Intune,
Ansible, and Puppet output formats.

The Intune script runs as the logged-on user and cannot remove the
machine-wide variables earlier versions set. --format intune --machine-cleanup
generates a separate script (no key) to deploy once as SYSTEM that removes
them.`,
	RunE: runGenerateMDM,
}

func init() {
	generateMDMCmd.Flags().StringVar(&flagMDMFormat, "format", "", "Output format: jamf, intune, ansible, puppet (required)")
	generateMDMCmd.Flags().BoolVar(&flagMDMMachineCleanup, "machine-cleanup", false, "Intune: generate the run-once SYSTEM script that removes machine-wide variables earlier versions set (no key)")
	_ = generateMDMCmd.MarkFlagRequired("format")
	rootCmd.AddCommand(generateMDMCmd)
}

func runGenerateMDM(cmd *cobra.Command, args []string) error {
	if flagMDMMachineCleanup {
		if flagMDMFormat != "intune" {
			return fmt.Errorf("--machine-cleanup is only for --format intune")
		}
		return mdm.GenerateIntuneMachineCleanup(os.Stdout)
	}

	cfg := config.FromEnv(flagEndpoint, flagKey)
	cfg.Verbose = flagVerbose
	if err := cfg.Validate(false); err != nil {
		return err
	}

	if cfg.Key == "" {
		fmt.Fprintln(os.Stderr, "Warning: no API key set. The generated artefact will have an empty key placeholder.")
	}
	fmt.Fprintln(os.Stderr, "Note: the artefact embeds ONE key. Every user it is deployed to is attributed to that key's owner;")
	fmt.Fprintln(os.Stderr, "      per-user attribution needs a per-user key (substitute it per device/user in your MDM).")
	if cfg.IgnoredLegacyEndpoint != "" {
		fmt.Fprintf(os.Stderr, "Note: ignoring the retired endpoint %s; using %s\n", cfg.IgnoredLegacyEndpoint, cfg.BaseEndpoint())
	}

	switch flagMDMFormat {
	case "jamf":
		return mdm.GenerateJamf(os.Stdout, cfg)
	case "intune":
		return mdm.GenerateIntune(os.Stdout, cfg)
	case "ansible":
		return mdm.GenerateAnsible(os.Stdout, cfg)
	case "puppet":
		return mdm.GeneratePuppet(os.Stdout, cfg)
	default:
		return fmt.Errorf("unsupported format: %s (supported: jamf, intune, ansible, puppet)", flagMDMFormat)
	}
}
