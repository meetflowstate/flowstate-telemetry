package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
)

// OpenCodeTool configures telemetry for the OpenCode editor.
//
// `experimental.openTelemetry` turns on OpenCode's built-in exporter, which
// reads only OTEL_EXPORTER_OTLP_ENDPOINT and OTEL_EXPORTER_OTLP_HEADERS from
// its environment and posts OTLP/HTTP JSON logs and traces to
// <endpoint>/v1/{logs,traces}. opencode.json has no env block, so the
// variables are scoped to an `opencode` wrapper function in the shell profile
// (exporting OTEL_EXPORTER_OTLP_ENDPOINT would redirect every other OTel SDK
// started from the shell).
//
// Earlier releases wrote an `env` block (ignored by OpenCode) and the
// `opencode-plugin-otel` plugin (unpublished from npm); Install removes both.
type OpenCodeTool struct{}

const legacyOpenCodePlugin = "opencode-plugin-otel"

func (o *OpenCodeTool) Name() string { return "opencode" }

func (o *OpenCodeTool) configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "opencode")
}

func (o *OpenCodeTool) configPath() string {
	return filepath.Join(o.configDir(), "opencode.json")
}

func (o *OpenCodeTool) Detect() bool {
	_, err := exec.LookPath("opencode")
	return err == nil
}

// openCodeBlock renders the profile lines for the opencode wrapper.
func openCodeBlock(cfg config.Config) []string {
	return []string{
		"# Scopes OpenTelemetry settings to OpenCode only.",
		shell.WrapperFunction("opencode", []string{
			fmt.Sprintf(`OTEL_EXPORTER_OTLP_ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-%s}"`, cfg.BaseEndpoint()),
			fmt.Sprintf(`OTEL_EXPORTER_OTLP_HEADERS="%s=${FLOWSTATE_OTLP_KEY:-}"`, config.AuthHeader),
		}),
	}
}

// removeLegacy strips what earlier releases of this CLI wrote.
func (o *OpenCodeTool) removeLegacy(cfg map[string]interface{}) {
	for _, key := range []string{"plugin", "plugins"} {
		plugins, ok := cfg[key].([]interface{})
		if !ok {
			continue
		}
		var kept []interface{}
		for _, p := range plugins {
			if ps, ok := p.(string); ok && ps == legacyOpenCodePlugin {
				continue
			}
			kept = append(kept, p)
		}
		if len(kept) == 0 {
			delete(cfg, key)
		} else {
			cfg[key] = kept
		}
	}
	if envBlock, ok := cfg["env"].(map[string]interface{}); ok {
		delete(envBlock, "OTEL_EXPORTER_OTLP_ENDPOINT")
		delete(envBlock, "OTEL_EXPORTER_OTLP_HEADERS")
		if len(envBlock) == 0 {
			delete(cfg, "env")
		}
	}
}

func (o *OpenCodeTool) Install(cfg config.Config) error {
	opencodeCfg, err := readJSONFile(o.configPath())
	if err != nil {
		return err
	}

	o.removeLegacy(opencodeCfg)
	experimental, ok := opencodeCfg["experimental"].(map[string]interface{})
	if !ok {
		experimental = make(map[string]interface{})
	}
	experimental["openTelemetry"] = true
	opencodeCfg["experimental"] = experimental

	if cfg.DryRun {
		fmt.Printf("[dry-run] Would set experimental.openTelemetry=true in %s\n", o.configPath())
		return shell.WriteToolBlock(o.Name(), openCodeBlock(cfg), true)
	}

	if err := writeJSONFile(o.configPath(), opencodeCfg, 0644); err != nil {
		return err
	}
	return shell.WriteToolBlock(o.Name(), openCodeBlock(cfg), false)
}

func (o *OpenCodeTool) Remove() error {
	opencodeCfg, err := readJSONFile(o.configPath())
	if err != nil {
		return err
	}

	o.removeLegacy(opencodeCfg)
	if experimental, ok := opencodeCfg["experimental"].(map[string]interface{}); ok {
		delete(experimental, "openTelemetry")
		if len(experimental) == 0 {
			delete(opencodeCfg, "experimental")
		}
	}

	if _, err := os.Stat(o.configPath()); err == nil {
		if err := writeJSONFile(o.configPath(), opencodeCfg, 0644); err != nil {
			return err
		}
	}
	return shell.RemoveToolBlock(o.Name())
}

func (o *OpenCodeTool) otelEnabled() (bool, error) {
	opencodeCfg, err := readJSONFile(o.configPath())
	if err != nil {
		return false, err
	}
	experimental, _ := opencodeCfg["experimental"].(map[string]interface{})
	enabled, _ := experimental["openTelemetry"].(bool)
	return enabled, nil
}

func (o *OpenCodeTool) Status() ToolStatus {
	st := ToolStatus{Detected: o.Detect()}
	if !st.Detected {
		return st
	}

	enabled, err := o.otelEnabled()
	if err != nil {
		st.Details = fmt.Sprintf("error reading config: %v", err)
		return st
	}
	st.Configured = enabled && shell.HasToolBlock(o.Name())

	return st
}

func (o *OpenCodeTool) Verify(cfg config.Config) VerifyResult {
	if !o.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	enabled, err := o.otelEnabled()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read config: %v", err)}
	}
	if !enabled {
		return VerifyResult{OK: false, Message: "experimental.openTelemetry not enabled in opencode.json"}
	}
	if !shell.HasToolBlock(o.Name()) {
		return VerifyResult{OK: false, Message: fmt.Sprintf("opencode wrapper missing from %s", shell.ProfilePath())}
	}

	return VerifyResult{
		OK:      true,
		Message: "experimental.openTelemetry=true · OTLP/HTTP JSON logs+traces",
		Notes: []string{
			"upstream marks experimental.openTelemetry deprecated; it may stop working in OpenCode 2.x",
			"only `opencode` started from a shell that reads " + shell.ProfilePath() + " is covered",
		},
	}
}
