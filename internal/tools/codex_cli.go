package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

// CodexCLI configures telemetry for OpenAI's Codex CLI.
//
// Codex takes one exporter per signal and uses each endpoint verbatim (it
// does not append /v1/<signal>), so every exporter carries its full URL.
// The [otel] tables are replaced textually so the rest of config.toml,
// comments included, is left exactly as it was. [analytics] is never
// touched.
type CodexCLI struct{}

func (c *CodexCLI) Name() string { return "codex-cli" }

func (c *CodexCLI) configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func (c *CodexCLI) configPath() string {
	return filepath.Join(c.configDir(), "config.toml")
}

func (c *CodexCLI) Detect() bool {
	if _, err := exec.LookPath("codex"); err == nil {
		return true
	}
	if _, err := os.Stat(c.configDir()); err == nil {
		return true
	}
	return false
}

// codexSignals maps each [otel] exporter key to its OTLP/HTTP signal.
var codexSignals = []struct{ key, signal string }{
	{"exporter", "logs"},
	{"metrics_exporter", "metrics"},
	{"trace_exporter", "traces"},
}

func (c *CodexCLI) readRaw() (string, error) {
	data, err := os.ReadFile(c.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading config: %w", err)
	}
	return string(data), nil
}

func (c *CodexCLI) readConfig() (map[string]interface{}, error) {
	raw, err := c.readRaw()
	if err != nil {
		return nil, err
	}
	cfg := map[string]interface{}{}
	if err := toml.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

// renderCodexOTel renders the [otel] table Flowstate owns.
func renderCodexOTel(cfg config.Config) string {
	var b strings.Builder
	b.WriteString("[otel]\n")
	b.WriteString("environment = \"production\"\n")
	fmt.Fprintf(&b, "log_user_prompt = %t\n", cfg.Prompts)
	for _, s := range codexSignals {
		fmt.Fprintf(&b, "%s = { otlp-http = { endpoint = %s, protocol = \"binary\", headers = { %s = %s } } }\n",
			s.key, tomlString(cfg.SignalEndpoint(s.signal)), tomlString(config.AuthHeader), tomlString(cfg.Key))
	}
	return b.String()
}

// otelHeader matches a table header for [otel] or any [otel.*] sub-table.
var (
	tableHeader = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
	otelTable   = regexp.MustCompile(`^"?otel"?(\s*\..*)?$`)
)

// stripCodexOTel removes every [otel] / [otel.*] table from raw TOML text,
// keeping all other lines (and comments) untouched. Comment and blank lines
// at the end of an [otel] table that run straight into a following non-otel
// table header belong to that table, so they are kept.
func stripCodexOTel(raw string) string {
	lines := strings.SplitAfter(raw, "\n")
	var out, pending []string
	inOTel := false
	for _, line := range lines {
		if m := tableHeader.FindStringSubmatch(line); m != nil {
			wasOTel := inOTel
			inOTel = otelTable.MatchString(m[1])
			if wasOTel && !inOTel {
				out = appendPending(out, pending)
			}
			pending = nil
		}
		if !inOTel {
			out = append(out, line)
			continue
		}
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			pending = append(pending, line)
		} else {
			pending = nil
		}
	}
	return strings.TrimRight(strings.Join(out, ""), "\n")
}

// appendPending appends the comment block kept from the end of an [otel]
// table, without doubling a blank line that out already ends with.
func appendPending(out, pending []string) []string {
	if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		for len(pending) > 0 && strings.TrimSpace(pending[0]) == "" {
			pending = pending[1:]
		}
	}
	return append(out, pending...)
}

// mergeCodexOTel returns raw with its [otel] tables replaced by block.
func mergeCodexOTel(raw, block string) (string, error) {
	rest := stripCodexOTel(raw)
	var check map[string]interface{}
	if err := toml.Unmarshal([]byte(rest), &check); err != nil {
		return "", fmt.Errorf("parsing config: %w", err)
	}
	if _, ok := check["otel"]; ok {
		return "", fmt.Errorf("config.toml sets otel keys outside an [otel] table; move them under [otel] and re-run")
	}
	merged := block
	if rest != "" {
		merged = rest + "\n\n" + block
	}
	if err := toml.Unmarshal([]byte(merged), &check); err != nil {
		return "", fmt.Errorf("generated config does not parse: %w", err)
	}
	return merged, nil
}

func (c *CodexCLI) Install(cfg config.Config) error {
	raw, err := c.readRaw()
	if err != nil {
		return err
	}
	merged, err := mergeCodexOTel(raw, renderCodexOTel(cfg))
	if err != nil {
		return err
	}

	if cfg.DryRun {
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", c.configPath(), redactKey(merged, cfg.Key))
		return nil
	}

	if err := os.MkdirAll(c.configDir(), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	// [otel] holds the key: 0600, and an existing file is tightened.
	return writeKeyFile(c.configPath(), []byte(merged))
}

func (c *CodexCLI) Remove() error {
	raw, err := c.readRaw()
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	rest := stripCodexOTel(raw)
	if rest != "" {
		rest += "\n"
	}
	return os.WriteFile(c.configPath(), []byte(rest), 0600)
}

// codexOTel returns the parsed [otel] table, or nil.
func (c *CodexCLI) codexOTel() (map[string]interface{}, error) {
	cfg, err := c.readConfig()
	if err != nil {
		return nil, err
	}
	otel, _ := cfg["otel"].(map[string]interface{})
	return otel, nil
}

// codexHTTPExporter returns the otlp-http table of one exporter key.
func codexHTTPExporter(otel map[string]interface{}, key string) map[string]interface{} {
	exp, _ := otel[key].(map[string]interface{})
	http, _ := exp["otlp-http"].(map[string]interface{})
	return http
}

func (c *CodexCLI) Status() ToolStatus {
	st := ToolStatus{Detected: c.Detect()}
	if !st.Detected {
		return st
	}

	otel, err := c.codexOTel()
	if err != nil {
		st.Details = fmt.Sprintf("error reading config: %v", err)
		return st
	}

	if otel != nil {
		st.Configured = codexHTTPExporter(otel, "exporter") != nil
		st.Prompts, _ = otel["log_user_prompt"].(bool)
	}

	return st
}

func (c *CodexCLI) Verify(cfg config.Config) VerifyResult {
	if !c.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	otel, err := c.codexOTel()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read config: %v", err)}
	}

	if otel == nil {
		return VerifyResult{OK: false, Message: "no [otel] section in config.toml"}
	}

	var warnings []string
	for _, s := range codexSignals {
		exp := codexHTTPExporter(otel, s.key)
		if exp == nil {
			return VerifyResult{OK: false, Message: fmt.Sprintf("otel.%s is not otlp-http (gRPC is not accepted)", s.key)}
		}
		endpoint, _ := exp["endpoint"].(string)
		if r := legacyEndpoint("otel."+s.key+" endpoint", endpoint); r != nil {
			return *r
		}
		if !strings.HasSuffix(endpoint, "/v1/"+s.signal) {
			return VerifyResult{OK: false, Message: fmt.Sprintf("otel.%s endpoint %q must end in /v1/%s", s.key, endpoint, s.signal)}
		}
		if endpoint != cfg.SignalEndpoint(s.signal) {
			warnings = append(warnings, fmt.Sprintf("otel.%s endpoint %s differs from %s", s.key, endpoint, cfg.SignalEndpoint(s.signal)))
		}
		headers, _ := exp["headers"].(map[string]interface{})
		if key, _ := headers[config.AuthHeader].(string); key == "" {
			return VerifyResult{OK: false, Message: fmt.Sprintf("otel.%s has no x-flowstate-key header", s.key)}
		}
	}

	result := VerifyResult{OK: true, Message: fmt.Sprintf("config.toml present · OTLP/HTTP endpoint=%s", cfg.BaseEndpoint())}
	if p, _ := otel["log_user_prompt"].(bool); p {
		warnings = append(warnings, promptsOnWarning("log_user_prompt"))
	}
	if !c.analyticsEnabled() {
		warnings = append(warnings, "[analytics] enabled = false: Codex exports no OTLP metrics (tokens, cost, tool calls) with it off; set it to true or remove it")
	}
	result.Warning = strings.Join(warnings, "; ")

	return result
}

// analyticsEnabled reports whether Codex's [analytics] is on (the default).
// Codex only exports OTLP metrics when it is.
func (c *CodexCLI) analyticsEnabled() bool {
	cfg, err := c.readConfig()
	if err != nil {
		return true
	}
	analytics, _ := cfg["analytics"].(map[string]interface{})
	enabled, ok := analytics["enabled"].(bool)
	return !ok || enabled
}

// tomlString renders s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
