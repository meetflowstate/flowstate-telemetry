package config

import "os"

const (
	DefaultEndpoint = "https://otel.flowstate.inc"
)

// Config holds the resolved configuration for the CLI.
type Config struct {
	Endpoint string
	Key      string
	TeamID   string
	Email    string
	Prompts  bool
	DryRun   bool
	Verbose  bool
}

// FromEnv resolves configuration from environment variables, using the
// provided overrides for any values that were set via flags.
func FromEnv(flagEndpoint, flagKey string) Config {
	endpoint := flagEndpoint
	if endpoint == "" {
		endpoint = os.Getenv("FLOWSTATE_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	key := flagKey
	if key == "" {
		key = os.Getenv("FLOWSTATE_OTLP_KEY")
	}

	teamID := os.Getenv("FLOWSTATE_TEAM_ID")
	email := os.Getenv("FLOWSTATE_EMAIL")

	return Config{
		Endpoint: endpoint,
		Key:      key,
		TeamID:   teamID,
		Email:    email,
	}
}
