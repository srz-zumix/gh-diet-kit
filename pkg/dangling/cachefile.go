package dangling

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a temporary file in the same directory
// followed by a rename, so that concurrent readers never observe a partially
// written entry. Cache entries are written from multiple PR workers in parallel.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp file %s: %w", tmp, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close temp file %s: %w", tmp, err)
	}
	if err = os.Chmod(tmp, perm); err != nil {
		return fmt.Errorf("chmod temp file %s: %w", tmp, err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}
