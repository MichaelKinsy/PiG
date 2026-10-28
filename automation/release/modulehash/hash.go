// SPDX-License-Identifier: MIT

// Package modulehash computes the Go publication hashes of tracked module files.
package modulehash

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

// TrackedFiles lists Git-tracked paths relative to dir. Outside a Git checkout,
// it lists all files: a release source archive is already tracked-only.
func TrackedFiles(dir string) ([]string, error) {
	if err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Run(); err == nil { //nolint:gosec // G204: the program and arguments come from the user, the project config, or PiG's own toolchain lookup; the CLI runs as the user with no sandbox and launches them as Pi's child_process.spawn does
		out, err := exec.Command("git", "-C", dir, "ls-files", "-z", "--", ".").Output() //nolint:gosec // G204: the program and arguments come from the user, the project config, or PiG's own toolchain lookup; the CLI runs as the user with no sandbox and launches them as Pi's child_process.spawn does
		if err != nil {
			return nil, fmt.Errorf("list tracked files: %w", err)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"), nil
	}
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

type diskFile struct {
	rel, abs string
	data     []byte
	overlaid bool
}

func (f diskFile) Path() string { return f.rel }
func (f diskFile) Lstat() (fs.FileInfo, error) {
	info, err := os.Lstat(f.abs)
	if err != nil || !f.overlaid {
		return info, err
	}
	return overlayInfo{FileInfo: info, size: int64(len(f.data))}, nil
}
func (f diskFile) Open() (io.ReadCloser, error) {
	if f.overlaid {
		return io.NopCloser(bytes.NewReader(f.data)), nil
	}
	return os.Open(f.abs)
}

type overlayInfo struct {
	fs.FileInfo
	size int64
}

func (f overlayInfo) Size() int64 { return f.size }

// ModuleFiles lists the tracked files of the module at root/dir as module zip inputs, with the overlay semantics of Hashes. The list includes files that Go's zip rules later omit, such as nested modules; modzip.CheckFiles and modzip.Create apply those rules.
func ModuleFiles(root, dir string, overlay map[string][]byte) ([]modzip.File, error) {
	moduleDir := filepath.Join(root, filepath.FromSlash(dir))
	paths, err := TrackedFiles(moduleDir)
	if err != nil {
		return nil, err
	}
	files := make([]modzip.File, 0, len(paths))
	for _, rel := range paths {
		data, ok := overlay[filepath.ToSlash(filepath.Join(dir, rel))]
		files = append(files, diskFile{rel: rel, abs: filepath.Join(moduleDir, filepath.FromSlash(rel)), data: data, overlaid: ok})
	}
	return files, nil
}

// Hashes returns the module zip and go.mod h1 hashes using Go's module zip rules.
// Overlay keys are slash-separated paths relative to root; only tracked files
// participate, with working-tree bytes unless an overlay supplies planned bytes.
func Hashes(root, dir string, mod module.Version, overlay map[string][]byte) (zipHash, modHash string, err error) {
	moduleDir := filepath.Join(root, filepath.FromSlash(dir))
	files, err := ModuleFiles(root, dir, overlay)
	if err != nil {
		return "", "", err
	}
	out, err := os.CreateTemp("", "pig-module-*.zip")
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, os.Remove(out.Name())) }()
	zipErr := modzip.Create(out, mod, files)
	closeErr := out.Close()
	if zipErr != nil {
		return "", "", fmt.Errorf("build %s module zip: %w", dir, zipErr)
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	zipHash, err = dirhash.HashZip(out.Name(), dirhash.Hash1)
	if err != nil {
		return "", "", err
	}
	goMod, ok := overlay[filepath.ToSlash(filepath.Join(dir, "go.mod"))]
	if !ok {
		goMod, err = os.ReadFile(filepath.Join(moduleDir, "go.mod")) //nolint:gosec // G304: the path names a file that the user, the project config, or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so reading it grants nothing the user lacks
		if err != nil {
			return "", "", err
		}
	}
	modHash, err = dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(goMod)), nil
	})
	return zipHash, modHash, err
}
