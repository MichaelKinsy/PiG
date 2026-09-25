package release

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig additive (D18): managed release operations pin directory handles and reject symlink components. Rooted operations also prevent a concurrent replacement from escaping the opened directory.
func openStore(kind string, create bool) (*os.Root, error) {
	home := codingagent.ConfigRoot()
	if create {
		if err := os.MkdirAll(home, 0o755); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return openDirectory(root, filepath.Join(kind, "piglets"), create)
}

func openDirectory(root *os.Root, path string, create bool) (*os.Root, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for part := range strings.SplitSeq(filepath.Clean(path), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		if part == ".." || part == "" {
			_ = current.Close()
			return nil, fmt.Errorf("invalid managed directory %q", path)
		}
		if create {
			if err := current.Mkdir(part, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				_ = current.Close()
				return nil, err
			}
		}
		info, err := current.Lstat(part)
		if err == nil && !info.IsDir() {
			err = fmt.Errorf("managed directory %s is not a directory (symlinks are refused)", part)
		}
		if err != nil {
			_ = current.Close()
			return nil, err
		}
		next, err := current.OpenRoot(part)
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func openManagedFile(kind, path string) (*os.File, error) {
	root, err := openStore(kind, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	parent, err := openDirectory(root, filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed file %s is not regular (symlinks are refused)", path)
	}
	return parent.Open(name)
}

func readManagedFile(kind, path string) ([]byte, error) {
	file, err := openManagedFile(kind, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func stageIn(root *os.Root, input io.Reader, mode os.FileMode) (string, error) {
	name := ".piglet-pull-" + rand.Text() + ".stage"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = root.Remove(name)
		}
	}()
	if _, err := io.Copy(file, input); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	complete = true
	return name, nil
}

func lockStore() (*os.File, error) {
	root, err := openStore("receipts", true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	const name = ".release.lock"
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Piglet release lock is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("Piglet release store is busy; retry after the other operation finishes: %w", err)
	}
	return file, nil
}
