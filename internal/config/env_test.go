package config

import "testing"

func TestFromEnv_DefaultEndpoint(t *testing.T) {
	t.Setenv("FLOWSTATE_OTLP_ENDPOINT", "")
	t.Setenv("FLOWSTATE_OTLP_KEY", "")
	cfg := FromEnv("", "")
	if cfg.Endpoint != "https://otel.flowstate.host" {
		t.Fatalf("Endpoint = %q", cfg.Endpoint)
	}
}

func TestFromEnv_Overrides(t *testing.T) {
	t.Setenv("FLOWSTATE_OTLP_ENDPOINT", "https://env.example/")
	t.Setenv("FLOWSTATE_OTLP_KEY", "env-key")
	cfg := FromEnv("", "")
	if cfg.Endpoint != "https://env.example" || cfg.Key != "env-key" {
		t.Fatalf("env not honoured: %+v", cfg)
	}
	cfg = FromEnv("https://flag.example", "flag-key")
	if cfg.Endpoint != "https://flag.example" || cfg.Key != "flag-key" {
		t.Fatalf("flags not honoured: %+v", cfg)
	}
}

func TestSignalEndpoint(t *testing.T) {
	cfg := Config{Endpoint: "https://otel.flowstate.host/"}
	if got := cfg.SignalEndpoint("logs"); got != "https://otel.flowstate.host/v1/logs" {
		t.Fatalf("SignalEndpoint = %q", got)
	}
	if got := (Config{Key: "k"}).HeadersEnv(); got != "x-flowstate-key=k" {
		t.Fatalf("HeadersEnv = %q", got)
	}
}

func TestFromEnv_LegacyEndpointIsUnset(t *testing.T) {
	for _, legacy := range []string{
		"https://otel.flowstate.inc", "https://otel.flowstate.inc/", "https://otel.flowstate.inc/v1/logs",
		"http://OTEL.flowstate.inc:4317", "otel.flowstate.inc:4317",
	} {
		t.Setenv("FLOWSTATE_OTLP_ENDPOINT", legacy)
		cfg := FromEnv("", "")
		if cfg.Endpoint != DefaultEndpoint || cfg.IgnoredLegacyEndpoint != legacy {
			t.Errorf("%s: Endpoint = %q, ignored = %q", legacy, cfg.Endpoint, cfg.IgnoredLegacyEndpoint)
		}
		// A legacy flag falls through to the default too.
		if cfg := FromEnv(legacy, ""); cfg.Endpoint != DefaultEndpoint {
			t.Errorf("flag %s: Endpoint = %q", legacy, cfg.Endpoint)
		}
	}
	// A legacy env value does not override a good flag, and vice versa.
	t.Setenv("FLOWSTATE_OTLP_ENDPOINT", "https://otel.flowstate.inc")
	if cfg := FromEnv("https://flag.example", ""); cfg.Endpoint != "https://flag.example" {
		t.Fatalf("flag lost: %+v", cfg)
	}
	t.Setenv("FLOWSTATE_OTLP_ENDPOINT", "https://env.example")
	if cfg := FromEnv("https://otel.flowstate.inc", ""); cfg.Endpoint != "https://env.example" {
		t.Fatalf("env not used after legacy flag: %+v", cfg)
	}
}

func TestLegacyEndpointMatching(t *testing.T) {
	for s, want := range map[string]bool{
		"https://otel.flowstate.host": false, "https://otel.flowstate.inc.example.com": false,
		"https://otel.flowstate.inc": true, "": false, "https://x.otel.flowstate.inc": false,
	} {
		if got := IsLegacyEndpoint(s); got != want {
			t.Errorf("IsLegacyEndpoint(%q) = %v", s, got)
		}
	}
	for s, want := range map[string]bool{
		`export FLOWSTATE_OTLP_ENDPOINT="https://otel.flowstate.inc"`:  true,
		"FLOWSTATE_OTLP_ENDPOINT=otel.flowstate.inc:4317\n":            true,
		`export FLOWSTATE_OTLP_ENDPOINT="https://otel.flowstate.host"`: false,
		"https://otel.flowstate.inc.example.com":                       false,
	} {
		if got := ContainsLegacyEndpoint(s); got != want {
			t.Errorf("ContainsLegacyEndpoint(%q) = %v", s, got)
		}
	}
}
