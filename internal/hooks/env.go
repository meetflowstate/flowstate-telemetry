package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// EnvFilePath is where hook scripts look for the endpoint and key when the
// editor did not inherit them from a shell (editors launched from the Dock
// or a desktop launcher never read ~/.zshrc). The scripts parse it with sed;
// it is never sourced.
func EnvFilePath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "flowstate-telemetry", "env")
}

// WriteEnvFile writes the endpoint and key for hook scripts and the shell
// profile block (mode 0600). With no key in cfg, a key already in the file
// is kept rather than erased.
func WriteEnvFile(cfg config.Config) error {
	path := EnvFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	key := cfg.Key
	if key == "" {
		key = EnvFileKey()
	}
	content := fmt.Sprintf("FLOWSTATE_OTLP_ENDPOINT=%s\nFLOWSTATE_OTLP_KEY=%s\n", cfg.BaseEndpoint(), key)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Chmod(path, 0600)
}

// EnvFileKey returns FLOWSTATE_OTLP_KEY from the env file, or "".
func EnvFileKey() string {
	data, err := os.ReadFile(EnvFilePath())
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "FLOWSTATE_OTLP_KEY="); ok {
			return strings.TrimRight(v, "\r")
		}
	}
	return ""
}

// RemoveEnvFile deletes the hook env file. A missing file is not an error.
func RemoveEnvFile() error {
	if err := os.Remove(EnvFilePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
