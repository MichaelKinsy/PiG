package cellpack

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/fsretry"
)

// cells holds this binary's embedded packed-cell binaries and their manifest.
// Stock pig commits only an empty manifest; a Piglet Binary build stages the built
// binaries and a populated manifest into this directory before building.
//
//go:embed cells
var cells embed.FS

// LoadedCell is an embedded cell extracted from the Piglet Binary binary and ready to
// start without source or a language toolchain.
type LoadedCell struct {
	Language   string
	Key        string
	Strategy   string
	BinaryPath string
	Extensions []ExtEntry
}

var loadedCells []LoadedCell

// LoadedCells returns the embedded cells extracted by Register. The returned
// slice is a copy, so callers may sort or filter it.
func LoadedCells() []LoadedCell {
	out := append([]LoadedCell(nil), loadedCells...)
	for i := range out {
		out[i].Extensions = append([]ExtEntry(nil), out[i].Extensions...)
	}
	return out
}

// Register wires this binary's embedded cells into the host prebuilt-cell seam
// and records source-free cell entries that startup can load directly. It is a
// no-op when the embedded manifest is empty (stock pig), so it never changes
// stock behavior.
func Register() error {
	return registerFrom(cells)
}

// registerFrom is Register for the cells embedded in fsys.
func registerFrom(fsys fs.FS) error {
	loadedCells = nil
	m, err := loadManifest(fsys)
	if err != nil {
		return err
	}
	if len(m.Cells) == 0 {
		return nil
	}
	dest, err := extractDir()
	if err != nil {
		return err
	}
	if err := extract(fsys, m, dest); err != nil {
		return err
	}
	runtimecell.SetPrebuiltResolver(NewResolver(m, dest))
	loadedCells = loadedCellsFromManifest(m, dest)
	return nil
}

func loadedCellsFromManifest(m Manifest, baseDir string) []LoadedCell {
	out := make([]LoadedCell, 0, len(m.Cells))
	for _, c := range m.Cells {
		binaryPath := extractedBinaryPath(baseDir, c.Binary)
		if c.Language == nodeLanguage {
			binaryPath = nodeSourcesDir(baseDir, c.Digest)
		}
		out = append(out, LoadedCell{
			Language:   c.Language,
			Key:        c.Key,
			Strategy:   c.Strategy,
			BinaryPath: binaryPath,
			Extensions: append([]ExtEntry(nil), c.Extensions...),
		})
	}
	return out
}

func loadManifest(fsys fs.FS) (Manifest, error) {
	data, err := fs.ReadFile(fsys, "cells/manifest.json")
	if err != nil {
		return Manifest{}, fmt.Errorf("read embedded cell manifest: %w", err)
	}
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("parse embedded cell manifest: %w", err)
	}
	return m, nil
}

// extract writes each embedded cell binary (addressed under cells/<Binary>) to
// dest/<Binary> with the executable bit set, unless the file already holds
// exactly those bytes. A cell binary that is already in place is left alone:
// another Piglet Binary instance may be executing it, and Windows refuses to
// replace the file of a running image ("Access is denied"), so rewriting an
// identical file at every start would stop a second instance from starting.
// A file that differs is replaced through a temp file in the destination
// directory and a rename, so a reader never observes a half-written binary;
// only a transient lock on the rename is retried (internal/fsretry), and a
// rename that fails while a peer has meanwhile published the same bytes
// succeeds. A Node cell's archive is checked against its digest and unpacked
// into the directory nodeSourcesDir names.
func extract(fsys fs.FS, m Manifest, dest string) error {
	for _, c := range m.Cells {
		data, err := fs.ReadFile(fsys, path.Join("cells", c.Binary))
		if err != nil {
			return fmt.Errorf("read embedded cell %s: %w", c.Binary, err)
		}
		if c.Language == nodeLanguage {
			if err := extractNodeCell(data, c, dest); err != nil {
				return fmt.Errorf("extract Node cell %s: %w", c.Binary, err)
			}
			continue
		}
		out := extractedBinaryPath(dest, c.Binary)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := writeCellBinary(out, data, 0o755); err != nil {
			return fmt.Errorf("extract cell %s: %w", c.Binary, err)
		}
	}
	return nil
}

// extractedBinaryPath is where the cell binary embedded at cells/<binary> is
// written under dest. Windows starts only files with an executable extension,
// so there the binary is written as <binary>.exe unless it already ends in .exe.
func extractedBinaryPath(dest, binary string) string {
	out := filepath.Join(dest, filepath.FromSlash(binary))
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(out), ".exe") {
		out += ".exe"
	}
	return out
}

// renameFile publishes a written temp file; a test replaces it to make the rename fail.
var renameFile = func(oldpath, newpath string) error {
	//portlint:allow ctxbackground extraction runs in Register at startup, before any session or context exists; the retry is bounded by fsretry.RenameBudget
	return fsretry.Rename(context.Background(), oldpath, newpath, nil)
}

// writeCellBinary leaves path alone when it already holds data with the
// wanted executable bits, and otherwise publishes data there.
func writeCellBinary(path string, data []byte, mode os.FileMode) error {
	if holdsExactly(path, data, mode) {
		return nil
	}
	err := atomicWriteFile(path, data, mode)
	if err != nil && holdsExactly(path, data, mode) {
		// A peer published the same bytes while this write was refused.
		return nil
	}
	return err
}

// holdsExactly reports whether path is a regular file with data's bytes and, off Windows, the execute bits of mode.
func holdsExactly(path string, data []byte, mode os.FileMode) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat() //perf:ok once per embedded cell binary at startup
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&mode&0o111 != mode&0o111 {
		return false
	}
	var chunk [64 << 10]byte
	for len(data) > 0 {
		n, err := file.Read(chunk[:min(len(chunk), len(data))])
		if n > 0 {
			if !bytes.Equal(chunk[:n], data[:n]) {
				return false
			}
			data = data[n:]
		}
		if err != nil {
			return len(data) == 0
		}
	}
	return true
}

// atomicWriteFile publishes data at path via a temp file in the same directory
// followed by a rename, so a reader never sees a partial file and concurrent
// writers of identical content resolve to one complete file. A POSIX rename
// replaces a file that a process is executing; Windows refuses it, which is why
// writeCellBinary does not call this for a file that is already right.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".extract-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameFile(tmpName, path)
}

// extractDir is the absolute piglet-binary-cells directory under the PiG configuration root ($PIG_HOME, else $XDG_CONFIG_HOME/pig, else ~/.pig). It is absolute because the host starts a cell binary in the session's working directory, which a relative path would be resolved against.
func extractDir() (string, error) {
	dir, err := filepath.Abs(filepath.Join(codingagent.ConfigRoot(), "piglet-binary-cells"))
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0o755)
}
