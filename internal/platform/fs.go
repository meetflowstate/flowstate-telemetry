package platform

import (
	"errors"
	"os"
)

// writeFile writes data to path with the given mode, creating or truncating
// as needed. Used by platform implementations to drop service definitions.
func writeFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}

// removeFile deletes path. A missing file is not an error — UninstallService
// is contractually idempotent.
func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
