package pilock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// replaceLegacy holds the old OS lock through removal and rechecks staleness after opening. It never creates a regular sidecar or truncates an existing file.
func replaceLegacy(path string, observed fs.FileInfo, stale time.Duration) (err error) {
	file, err := openLegacy(path)
	if errors.Is(err, fs.ErrNotExist) {
		return mkdir(path, 0)
	}
	if err != nil {
		// Another upgrader can replace the observed file before open, which reports EISDIR on Unix or access/sharing errors on Windows. Retry that ownership change, not a malformed legacy file.
		if current, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) || (statErr == nil && (!current.Mode().IsRegular() || !os.SameFile(observed, current))) {
			return ErrLocked
		}
		return fmt.Errorf("open legacy lock %s: %w", path, err)
	}
	defer func() {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
	}()
	if err := lockLegacy(file); err != nil {
		return fmt.Errorf("check legacy lock %s: %w", path, err)
	}
	// The descriptor and name must still identify the observed empty regular file. A competing upgrader can already have replaced it with a directory.
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrLocked
	}
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || opened.Size() != 0 || !current.Mode().IsRegular() || current.Size() != 0 || !os.SameFile(observed, opened) || !os.SameFile(opened, current) {
		return ErrLocked
	}
	if !opened.ModTime().Before(time.Now().Add(-stale)) {
		return ErrLocked
	}
	if err := removeLegacy(file, path); err != nil {
		return fmt.Errorf("remove idle legacy lock %s: %w", path, err)
	}
	// Windows completes deletion when the locked handle closes. No store work starts unless the subsequent mkdir wins against all other writers.
	err = file.Close()
	file = nil
	if err != nil {
		return err
	}
	return mkdir(path, 0)
}
