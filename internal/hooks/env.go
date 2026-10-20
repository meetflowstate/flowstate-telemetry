package hooks

import (
	"fmt"
	"os"
	"path/filepath"

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

// WriteEnvFile writes the endpoint and key for hook scripts (mode 0600).
func WriteEnvFile(cfg config.Config) error {
	path := EnvFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	content := fmt.Sprintf("FLOWSTATE_OTLP_ENDPOINT=%s\nFLOWSTATE_OTLP_KEY=%s\n", cfg.BaseEndpoint(), cfg.Key)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Chmod(path, 0600)
}

// RemoveEnvFile deletes the hook env file. A missing file is not an error.
func RemoveEnvFile() error {
	if err := os.Remove(EnvFilePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
