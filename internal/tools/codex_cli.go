package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

// CodexCLI configures telemetry for OpenAI's Codex CLI.
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

// codexConfig represents the TOML structure for Codex CLI configuration.
type codexConfig struct {
	OTel *codexOTel `toml:"otel,omitempty"`
}

type codexOTel struct {
	Environment   string                       `toml:"environment"`
	LogUserPrompt bool                         `toml:"log_user_prompt"`
	Exporter      map[string]codexOTelExporter `toml:"exporter,omitempty"`
}

type codexOTelExporter struct {
	Endpoint string            `toml:"endpoint"`
	Headers  map[string]string `toml:"headers,omitempty"`
}

func (c *CodexCLI) readConfig() (*codexConfig, error) {
	data, err := os.ReadFile(c.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &codexConfig{}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg codexConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

func (c *CodexCLI) writeConfig(cfg *codexConfig) error {
	if err := os.MkdirAll(c.configDir(), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(c.configPath(), data, 0644)
}

func (c *CodexCLI) Install(cfg config.Config) error {
	codexCfg, err := c.readConfig()
	if err != nil {
		return err
	}

	codexCfg.OTel = &codexOTel{
		Environment:   "production",
		LogUserPrompt: cfg.Prompts,
		Exporter: map[string]codexOTelExporter{
			"otlp-grpc": {
				Endpoint: cfg.Endpoint,
				Headers: map[string]string{
					"x-flowstate-key": cfg.Key,
				},
			},
		},
	}

	if cfg.DryRun {
		data, _ := toml.Marshal(codexCfg)
		fmt.Printf("[dry-run] Would write to %s:\n%s\n", c.configPath(), string(data))
		return nil
	}

	return c.writeConfig(codexCfg)
}

func (c *CodexCLI) Remove() error {
	codexCfg, err := c.readConfig()
	if err != nil {
		return err
	}

	codexCfg.OTel = nil

	return c.writeConfig(codexCfg)
}

func (c *CodexCLI) Status() ToolStatus {
	st := ToolStatus{Detected: c.Detect()}
	if !st.Detected {
		return st
	}

	codexCfg, err := c.readConfig()
	if err != nil {
		st.Details = fmt.Sprintf("error reading config: %v", err)
		return st
	}

	if codexCfg.OTel != nil {
		st.Configured = true
		st.Prompts = codexCfg.OTel.LogUserPrompt
	}

	return st
}

func (c *CodexCLI) Verify(cfg config.Config) VerifyResult {
	if !c.Detect() {
		return VerifyResult{OK: false, Message: "not detected on PATH"}
	}

	codexCfg, err := c.readConfig()
	if err != nil {
		return VerifyResult{OK: false, Message: fmt.Sprintf("cannot read config: %v", err)}
	}

	if codexCfg.OTel == nil {
		return VerifyResult{OK: false, Message: "no [otel] section in config.toml"}
	}

	exporter, ok := codexCfg.OTel.Exporter["otlp-grpc"]
	if !ok {
		return VerifyResult{OK: false, Message: "no otlp-grpc exporter configured"}
	}

	result := VerifyResult{OK: true, Message: fmt.Sprintf("config.toml present · endpoint=%s", exporter.Endpoint)}
	if !codexCfg.OTel.LogUserPrompt {
		result.Warning = "log_user_prompt not enabled (re-run with --prompts)"
	}

	return result
}
