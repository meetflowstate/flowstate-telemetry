package cmd

import (
	"fmt"
	"os"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/mdm"
	"github.com/spf13/cobra"
)

var (
	flagMDMFormat string
	flagMDMTool   string
)

var generateMDMCmd = &cobra.Command{
	Use:   "generate-mdm",
	Short: "Generate MDM deployment artefacts for fleet-wide rollout",
	Long: `Generate MDM deployment artefacts for mass deployment of Flowstate
telemetry configuration across developer machines. Supports Jamf, Intune,
Ansible, and Puppet output formats.`,
	RunE: runGenerateMDM,
}

func init() {
	generateMDMCmd.Flags().StringVar(&flagMDMFormat, "format", "", "Output format: jamf, intune, ansible, puppet (required)")
	generateMDMCmd.Flags().StringVar(&flagMDMTool, "tool", "", "Generate config for specific tool only")
	_ = generateMDMCmd.MarkFlagRequired("format")
	rootCmd.AddCommand(generateMDMCmd)
}

func runGenerateMDM(cmd *cobra.Command, args []string) error {
	cfg := config.FromEnv(flagEndpoint, flagKey)
	cfg.Verbose = flagVerbose

	if cfg.Key == "" {
		fmt.Fprintln(os.Stderr, "Warning: no API key set. The generated artefact will have an empty key placeholder.")
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
