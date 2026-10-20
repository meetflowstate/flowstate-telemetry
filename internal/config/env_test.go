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
