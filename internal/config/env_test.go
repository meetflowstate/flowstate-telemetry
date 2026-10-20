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

func TestValidate(t *testing.T) {
	ok := []struct {
		endpoint string
		loopback bool
	}{
		{"https://otel.flowstate.host", false},
		{"https://otel.flowstate.host:8443/base/", false},
		{"https://[::1]:8787", false},
		{"http://localhost:8787", true},
		{"http://127.0.0.1:8787", true},
	}
	for _, c := range ok {
		if err := (Config{Endpoint: c.endpoint, Key: "fs_key"}).Validate(c.loopback); err != nil {
			t.Errorf("%q rejected: %v", c.endpoint, err)
		}
	}
	for _, c := range []struct {
		endpoint string
		loopback bool
	}{
		{"http://otel.flowstate.host", true},
		{"http://localhost:8787", false},
		{"ftp://otel.flowstate.host", false},
		{"otel.flowstate.host", false},
		{"https://", false},
		{`https://x.example/"$(id)"`, false},
		{"https://x.example/`id`", false},
		{"https://x.example/'q'", false},
		{"https://x.example/a b", false},
		{"https://x.example/\nx", false},
		{"https://x.example/\\", false},
		{"https://x.example/?q=1", false},
		{"https://x.example/#f", false},
		{"https://user@x.example", false},
	} {
		if err := (Config{Endpoint: c.endpoint}).Validate(c.loopback); err == nil {
			t.Errorf("%q accepted", c.endpoint)
		}
	}
	for _, bad := range []Config{
		{Endpoint: DefaultEndpoint, Key: "k\ney"},
		{Endpoint: DefaultEndpoint, Key: "k\rey"},
		{Endpoint: DefaultEndpoint, Key: "k\x00"},
		{Endpoint: DefaultEndpoint, Key: "k\x7f"},
		{Endpoint: DefaultEndpoint, Key: "k\tey"},
		{Endpoint: DefaultEndpoint, Email: "a@b\n"},
		{Endpoint: DefaultEndpoint, TeamID: "t\n"},
	} {
		if err := bad.Validate(false); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// Quotes, $, backticks and $(...) in a key are fine: every artefact
	// quotes the key for its own language.
	if err := (Config{Endpoint: DefaultEndpoint, Key: "k'e\"y$x`id`$(id)\\"}).Validate(false); err != nil {
		t.Errorf("hostile-but-printable key rejected: %v", err)
	}
}
