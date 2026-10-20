package config

import (
	"net/url"
	"os"
	"regexp"
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

	// legacyHost is the retired gRPC receiver. Releases before the OTLP/HTTP
	// receiver exported FLOWSTATE_OTLP_ENDPOINT=https://otel.flowstate.inc
	// from the shell profile, so a re-install would otherwise inherit it.
	legacyHost = "otel.flowstate.inc"
)

// legacyPattern finds the retired receiver host in free text (profiles,
// env files, plists), with or without scheme, port or path.
var legacyPattern = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9.-])otel\.flowstate\.inc($|[^A-Za-z0-9.-])`)

// IsLegacyEndpoint reports whether endpoint points at the retired
// otel.flowstate.inc receiver (any scheme, port or path).
func IsLegacyEndpoint(endpoint string) bool {
	s := strings.TrimSpace(endpoint)
	if s == "" {
		return false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ContainsLegacyEndpoint(endpoint)
	}
	return strings.EqualFold(u.Hostname(), legacyHost)
}

// ContainsLegacyEndpoint reports whether text mentions the retired receiver.
func ContainsLegacyEndpoint(text string) bool {
	return legacyPattern.MatchString(text)
}

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

	// IgnoredLegacyEndpoint is the retired endpoint FromEnv found (flag or
	// FLOWSTATE_OTLP_ENDPOINT) and replaced with DefaultEndpoint, for messages.
	IgnoredLegacyEndpoint string
}

// FromEnv resolves configuration from environment variables, using the
// provided overrides for any values that were set via flags. The retired
// otel.flowstate.inc endpoint counts as unset, so upgrading installs move to
// DefaultEndpoint.
func FromEnv(flagEndpoint, flagKey string) Config {
	var ignored string
	endpoint := ""
	for _, candidate := range []string{flagEndpoint, os.Getenv("FLOWSTATE_OTLP_ENDPOINT")} {
		if IsLegacyEndpoint(candidate) {
			if ignored == "" {
				ignored = candidate
			}
			continue
		}
		if candidate != "" {
			endpoint = candidate
			break
		}
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

		IgnoredLegacyEndpoint: ignored,
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
