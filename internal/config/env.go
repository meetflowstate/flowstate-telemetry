package config

import (
	"os"
	"strings"
)

const (
	// DefaultEndpoint is the base URL of Flowstate's OTLP/HTTP receiver.
	// Signal paths (/v1/logs, /v1/metrics, /v1/traces) are appended to it.
	// The receiver runs on Cloudflare Workers and does not accept gRPC.
	DefaultEndpoint = "https://otel.flowstate.host"

	// AuthHeader is the request header carrying the Flowstate key. The
	// receiver also accepts "Authorization: Bearer <key>".
	AuthHeader = "x-flowstate-key"
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
	GUIEnv   bool
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
		Endpoint: strings.TrimRight(endpoint, "/"),
		Key:      key,
		TeamID:   teamID,
		Email:    email,
	}
}

// BaseEndpoint returns the receiver base URL without a trailing slash.
func (c Config) BaseEndpoint() string {
	return strings.TrimRight(c.Endpoint, "/")
}

// SignalEndpoint returns the full OTLP/HTTP URL for one signal ("logs",
// "metrics" or "traces"), for tools that take a per-signal URL verbatim.
func (c Config) SignalEndpoint(signal string) string {
	return c.BaseEndpoint() + "/v1/" + signal
}

// HeadersEnv returns the OTEL_EXPORTER_OTLP_HEADERS value carrying the key.
func (c Config) HeadersEnv() string {
	return AuthHeader + "=" + c.Key
}
