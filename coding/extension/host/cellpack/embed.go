package cellpack

import (
	"bytes"
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
	loadedCells = nil
	m, err := loadManifest(cells)
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
	if err := extract(cells, m, dest); err != nil {
		return err
	}
	runtimecell.SetPrebuiltResolver(NewResolver(m, dest))
	loadedCells = loadedCellsFromManifest(m, dest)
	return nil
}

func loadedCellsFromManifest(m Manifest, baseDir string) []LoadedCell {
	out := make([]LoadedCell, 0, len(m.Cells))
	for _, c := range m.Cells {
		out = append(out, LoadedCell{
			Language:   c.Language,
			Key:        c.Key,
			Strategy:   c.Strategy,
			BinaryPath: extractedBinaryPath(baseDir, c.Binary),
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
// dest/<Binary> with the executable bit set. Each write is atomic (temp file in
// the destination directory, then rename), so parallel Piglet Binary instances
// sharing the per-user extraction directory never observe a half-written binary
// and never corrupt each other's cell: the paths are content-addressed, so
// concurrent writers produce identical bytes.
func extract(fsys fs.FS, m Manifest, dest string) error {
	for _, c := range m.Cells {
		data, err := fs.ReadFile(fsys, path.Join("cells", c.Binary))
		if err != nil {
			return fmt.Errorf("read embedded cell %s: %w", c.Binary, err)
		}
		out := extractedBinaryPath(dest, c.Binary)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := atomicWriteFile(out, data, 0o755); err != nil {
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

// atomicWriteFile publishes data at path via a temp file in the same directory
// followed by a rename, so a reader never sees a partial file and concurrent
// writers of identical content resolve to one complete file.
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
	return os.Rename(tmpName, path)
}

func extractDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".pig", "piglet-binary-cells")
	return dir, os.MkdirAll(dir, 0o755)
}
