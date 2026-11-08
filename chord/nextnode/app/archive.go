package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Data archives (slice 13 §4, decisions "The app kit, point 5" and "App kit naming and export"): one tar of the data a host
// created, `manifest.json` (the app, its API version, the kit's format version, the address), `store.archive` (the store's export
// archive, slice 6a, as JSONL: one consistent point, the export holding the commit queue while it reads) and `files/` (its finished
// files). A running worker packs its own open store; without one the server packs the directory under the worker's lock. Unpacking
// makes a new directory for an address that has none, refusing every path that would leave it.
//
// Ports packages/chord/src/node/app/archive.ts (Pi main c23b9ec1). Forced Go differences (D455, as tar.go): streams are io.Readers
// and the tar's entries a push; a context is a context.Context.

const (
	// ArchiveFormat is the kit's archive format version.
	ArchiveFormat = 1
	// archiveStore is the store file of a host directory.
	archiveStore = "store.sqlite"
	// kitStore is the kit's own store file of a host directory.
	kitStore = "kit.sqlite"
	// importChunk is how much of a store archive is read at a time.
	importChunk = 64 * 1024
)

// ArchiveManifest is what an archive says about itself.
type ArchiveManifest struct {
	Format  float64        `json:"format"`
	App     string         `json:"app"`
	API     string         `json:"api"`
	Address ArchiveAddress `json:"address"`
}

// ArchiveAddress is the address an archive holds the data of.
type ArchiveAddress struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

// text is the manifest as `JSON.stringify` writes it.
func (m ArchiveManifest) text() []byte {
	encoded, _ := chordjson.Marshal(delta.JsonObjectOf(
		"format", m.Format, "app", m.App, "api", m.API,
		"address", delta.JsonObjectOf("kind", m.Address.Kind, "key", m.Address.Key),
	))
	return encoded
}

// PackData packs the host directory dir of a host that does not run (its worker's lock held: nothing writes it) into the archive
// at path: manifest, its store's export and its files.
func PackData(ctx context.Context, dir string, manifest ArchiveManifest, path string) error {
	if !archiveExists(dir) {
		return fmt.Errorf("exportData: %s has no data", manifest.Address.Key)
	}
	database, err := node.OpenNodeSqliteDatabase(filepath.Join(dir, archiveStore), node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return err
	}
	opened, err := store.OpenStore(store.SqliteBackend(database), ctx, store.StoreOptions{})
	if err != nil {
		return errors.Join(err, database.Close())
	}
	kit, kitClose, err := openKitStore(ctx, dir)
	if err != nil {
		return errors.Join(err, opened.Close(ctx))
	}
	packed := PackStore(ctx, opened, kit, filepath.Join(dir, "files"), manifest, path)
	return errors.Join(packed, kitClose(), opened.Close(ctx))
}

// openKitStore opens the kit's own store of a host directory (kit.sqlite, the Go kit's Files tables and documents; see StartWorker) when
// it has one: nil and a no-op close otherwise.
func openKitStore(ctx context.Context, dir string) (store.Store, func() error, error) {
	path := filepath.Join(dir, kitStore)
	if !archiveExists(path) {
		return nil, func() error { return nil }, nil
	}
	database, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return nil, nil, err
	}
	opened, err := store.OpenStore(store.SqliteBackend(database), ctx, store.StoreOptions{})
	if err != nil {
		return nil, nil, errors.Join(err, database.Close())
	}
	return opened, func() error { return opened.Close(ctx) }, nil
}

// PackStore packs the open store, the kit's store beside it (nil: none) and the files directory files into the archive at path (mode 0600, staged beside it and renamed):
// manifest, the store's export (one consistent point, written while the host runs on) and the finished files (uploads under way
// left out).
func PackStore(ctx context.Context, opened, kit store.Store, files string, manifest ArchiveManifest, path string) (err error) {
	staging, err := os.MkdirTemp(filepath.Dir(path), ".chord-export-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	exported := filepath.Join(staging, "store.archive")
	if err := writeStoreArchive(ctx, opened, exported); err != nil {
		return err
	}
	info, err := os.Stat(exported)
	if err != nil {
		return err
	}
	var kitExported string
	var kitInfo os.FileInfo
	if kit != nil {
		kitExported = filepath.Join(staging, "kit.archive")
		if err := writeStoreArchive(ctx, kit, kitExported); err != nil {
			return err
		}
		if kitInfo, err = os.Stat(kitExported); err != nil {
			return err
		}
	}
	text := manifest.text()
	written := filepath.Join(staging, "archive.tar")
	out, err := os.OpenFile(written, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	var walked error
	entries := func(yield func(TarEntry) bool) {
		if !yield(TarEntry{Path: "manifest.json", Size: int64(len(text)), Data: bytes.NewReader(text)}) {
			return
		}
		stored, err := os.Open(exported)
		if err != nil {
			walked = err
			return
		}
		defer func() { _ = stored.Close() }()
		if !yield(TarEntry{Path: "store.archive", Size: info.Size(), Data: stored}) {
			return
		}
		if kit != nil {
			kitStored, err := os.Open(kitExported)
			if err != nil {
				walked = err
				return
			}
			defer func() { _ = kitStored.Close() }()
			if !yield(TarEntry{Path: "kit.archive", Size: kitInfo.Size(), Data: kitStored}) {
				return
			}
		}
		walked = packedFiles(files, yield)
	}
	err = errors.Join(WriteTar(out, entries), walked)
	if closed := out.Close(); err == nil {
		err = closed
	}
	if err != nil {
		return err
	}
	return os.Rename(written, path)
}

// writeStoreArchive writes the store's export, as JSONL, to path (mode 0600).
func writeStoreArchive(ctx context.Context, opened store.Store, path string) (err error) {
	export, err := opened.Export(ctx)
	if err != nil {
		return err
	}
	source := store.EncodeJsonl(export)
	defer func() { err = errors.Join(err, source.Close()) }()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	for {
		chunk, ok, err := source.Next(ctx)
		if err != nil || !ok {
			return err
		}
		if _, err := out.Write(chunk); err != nil {
			return err
		}
	}
}

// packedFiles yields the files under root as entries `files/<relative path>` (hidden ones, uploads under way, left out), each opened
// only when the archive comes to it (one descriptor at a time) and its size taken from the open file: a file deleted while a running
// worker is exported is skipped (finished files never change; Opus L7).
func packedFiles(root string, yield func(TarEntry) bool) error {
	paths, err := pathsUnder(root)
	if err != nil {
		return err
	}
	for _, path := range paths {
		handle, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		info, err := handle.Stat()
		if err != nil {
			return errors.Join(err, handle.Close())
		}
		if !info.Mode().IsRegular() {
			if err := handle.Close(); err != nil {
				return err
			}
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return errors.Join(err, handle.Close())
		}
		more := yield(TarEntry{Path: "files/" + filepath.ToSlash(relative), Size: info.Size(), Data: io.LimitReader(handle, info.Size())})
		if err := handle.Close(); err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
	return nil
}

// pathsUnder is the paths of the files under root, sorted, hidden ones left out (a directory gone meanwhile: none of it).
func pathsUnder(root string) ([]string, error) {
	var paths []string
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		// JS compares names by UTF-16 code units.
		slices.SortFunc(entries, func(a, b os.DirEntry) int {
			return slices.Compare(utf16.Encode([]rune(a.Name())), utf16.Encode([]rune(b.Name())))
		})
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			switch {
			case entry.IsDir():
				if err := walk(path); err != nil {
					return err
				}
			case entry.Type().IsRegular():
				paths = append(paths, path)
			}
		}
		return nil
	}
	return paths, walk(root)
}

// ImportLimits is what an import takes at most: unpacked bytes (4 GiB by default) and entries (100,000 by default); zero is the
// default.
type ImportLimits struct {
	Bytes   int64
	Entries int
}

// UnpackData unpacks the archive at archive into the new host directory dir (refused when it exists): its manifest checked by
// accept (an error refuses it), the store made from `store.archive`, `files/` restored, within limits. Staged beside dir (mode 0700,
// files 0600) and renamed into place at the end, so a refused or broken archive leaves nothing.
func UnpackData(ctx context.Context, archive, dir string, accept func(ArchiveManifest) error, limits ImportLimits) (err error) {
	if archiveExists(dir) {
		return fmt.Errorf("importData: %s exists already (an address is imported once)", dir)
	}
	if err := PrivateDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dir), ".chord-import-")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(staging))
		}
	}()
	storeArchive, err := unpackEntries(ctx, archive, staging, accept, limits)
	if err != nil {
		return err
	}
	if kitArchive := filepath.Join(staging, "kit.archive"); archiveExists(kitArchive) {
		if err := PrivateStore(filepath.Join(staging, kitStore)); err != nil {
			return err
		}
		if err := importStoreArchive(ctx, kitArchive, filepath.Join(staging, kitStore)); err != nil {
			return err
		}
		if err := os.Remove(kitArchive); err != nil {
			return err
		}
	}
	if err := PrivateStore(filepath.Join(staging, archiveStore)); err != nil {
		return err
	}
	if err := importStoreArchive(ctx, storeArchive, filepath.Join(staging, archiveStore)); err != nil {
		return err
	}
	if err := os.Remove(storeArchive); err != nil {
		return err
	}
	return os.Rename(staging, dir)
}

// importStoreArchive makes the store at path from the JSONL archive at archive.
func importStoreArchive(ctx context.Context, archive, path string) error {
	database, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return err
	}
	file, err := os.Open(archive)
	if err != nil {
		return errors.Join(err, database.Close())
	}
	opened, err := store.ImportStore(store.SqliteBackend(database), store.DecodeJsonl(&fileBytes{file: file}), ctx, store.StoreOptions{})
	if err != nil {
		return errors.Join(err, database.Close())
	}
	return opened.Close(ctx)
}

// fileBytes is a file as a store.ByteSource.
type fileBytes struct{ file *os.File }

func (f *fileBytes) Next(ctx context.Context) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	chunk := make([]byte, importChunk)
	n, err := f.file.Read(chunk)
	if n > 0 {
		return chunk[:n], true, nil
	}
	if errors.Is(err, io.EOF) {
		return nil, false, nil
	}
	return nil, false, err
}

func (f *fileBytes) Close() error { return f.file.Close() }

// unpackEntries writes the entries of archive into staging: the manifest first (checked by accept before any data is written),
// then `store.archive`, `kit.archive` (optional) and `files/…`, within limits (counted from the headers, before an entry is written). It returns the path of
// the staged `store.archive`.
func unpackEntries(ctx context.Context, archive, staging string, accept func(ArchiveManifest) error, limits ImportLimits) (string, error) {
	maxBytes, maxEntries := limits.Bytes, limits.Entries
	if maxBytes == 0 {
		maxBytes = 4 << 30
	}
	if maxEntries == 0 {
		maxEntries = 100_000
	}
	file, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	var storeArchive string
	var haveManifest bool
	var bytes int64
	var entries int
	err = ReadTar(file, func(entry TarRead) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entries++; entries > maxEntries {
			return fmt.Errorf("importData: the archive has more than %d entries", maxEntries)
		}
		if entry.Kind == TarDirectory {
			return nil
		}
		if bytes += entry.Size; bytes > maxBytes {
			return fmt.Errorf("importData: the archive unpacks to more than %d bytes", maxBytes)
		}
		if !haveManifest && entry.Path != "manifest.json" {
			return errors.New("importData: the archive does not start with manifest.json")
		}
		switch {
		case entry.Path == "manifest.json":
			// A trust boundary: the archive's own description, checked by accept.
			text, err := io.ReadAll(io.LimitReader(entry.Data, manifestLimit+1))
			if err != nil {
				return err
			}
			if len(text) > manifestLimit {
				return errors.New("importData: manifest.json is too large")
			}
			var manifest ArchiveManifest
			if err := json.Unmarshal(text, &manifest); err != nil {
				return fmt.Errorf("importData: manifest.json: %w", err)
			}
			haveManifest = true
			return accept(manifest)
		case entry.Path == "store.archive":
			storeArchive = filepath.Join(staging, "store.archive")
			return writeStaged(storeArchive, entry.Data)
		case entry.Path == "kit.archive":
			return writeStaged(filepath.Join(staging, "kit.archive"), entry.Data)
		case strings.HasPrefix(entry.Path, "files/"):
			// ReadTar refused absolute paths and `..`: files/<path> stays inside the staging directory.
			target := filepath.Join(staging, filepath.FromSlash(entry.Path))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			return writeStaged(target, entry.Data)
		default:
			return fmt.Errorf("importData: the archive holds %s, which an address archive does not", entry.Path)
		}
	})
	if err != nil {
		return "", err
	}
	if !haveManifest || storeArchive == "" {
		return "", errors.New("importData: the archive has no manifest.json or store.archive")
	}
	return storeArchive, nil
}

// manifestLimit is the most a manifest.json may hold: 64 KiB.
const manifestLimit = 64 * 1024

// writeStaged writes data to path (mode 0600).
func writeStaged(path string, data io.Reader) (err error) {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, data)
	return err
}

// archiveExists is whether path exists.
func archiveExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
