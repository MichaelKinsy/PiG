package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/src/node/app/files-copy.ts
//
// Copying a host's files (decision "Files v2"): one host's `Files` (its index and its blobs) into a new host directory, for
// an app that makes a new address from an existing one (p2's clone and fork). The index is the part of the source store's
// export that is `Files`' own (its tables and documents), imported as the new directory's store; each blob is linked (blobs
// never change, so two hosts may share the file) or copied while the export holds the source's commit queue, so no removal
// can take a blob the copied index still names.
//
// Go forced differences: Pi's async generator of parts is a store.PartSource; `await`s block.

// FilesSource is the host whose files are copied: its open store and its files directory.
type FilesSource struct {
	Store store.Store
	Files string
}

// CopyFiles copies the files of the host whose open store is source.Store and whose files directory is source.Files into the
// new host directory target (refused when it exists): `target/store.sqlite` holding only the `Files` index,
// `target/files/blobs/` its blobs. Staged beside `target` and renamed into place, so a failed copy leaves nothing. Runs where
// the source store is open (its worker, or whoever holds its lock); the source may go on meanwhile.
func CopyFiles(source FilesSource, target string, ctx context.Context) (err error) {
	if _, statErr := os.Stat(target); statErr == nil {
		return fmt.Errorf("copyFiles: %s exists already", target)
	}
	if err := PrivateDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".chord-files-copy-")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	blobs := NewBlobs(filepath.Join(source.Files, "blobs"))
	into := filepath.Join(staging, "files", "blobs")
	if err = os.MkdirAll(into, 0o700); err != nil {
		return err
	}
	if err = PrivateStore(filepath.Join(staging, "store.sqlite")); err != nil {
		return err
	}
	database, err := node.OpenNodeSqliteDatabase(filepath.Join(staging, "store.sqlite"), node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return err
	}
	exported, err := source.Store.Export(ctx)
	if err != nil {
		_ = database.Close()
		return err
	}
	parts := &filesParts{iterator: exported, blobs: blobs, into: into, documents: map[int64]struct{}{}}
	imported, err := store.ImportStore(store.SqliteBackend(database), parts, ctx, store.StoreOptions{})
	if err != nil {
		return err
	}
	if err = imported.Close(ctx); err != nil {
		return err
	}
	return os.Rename(staging, target)
}

// owner is who owns `Files`' tables (`<owner>:<table>`) and documents in a store: its contract.
const owner = filesName

// filesParts are the parts of a store's export that are `Files`' own: the manifest, its tables' parts, its documents' parts,
// and an `end` counting them. Each blob row's file is put into `into` before its part is passed on.
type filesParts struct {
	iterator  store.PartIterator
	blobs     *Blobs
	into      string
	documents map[int64]struct{}
	writes    int64
	pending   []store.ArchivePart
}

// Next is the next part to import.
func (parts *filesParts) Next(ctx context.Context) (any, bool, error) {
	for {
		if len(parts.pending) > 0 {
			next := parts.pending[0]
			parts.pending = parts.pending[1:]
			return next, true, nil
		}
		part, ok, err := parts.iterator.Next(ctx)
		if err != nil || !ok {
			return nil, false, err
		}
		kept := parts.keeps(part)
		if _, isEnd := part.(*store.EndPart); isEnd {
			parts.pending = append(parts.pending, &store.EndPart{Writes: parts.writes})
			continue
		}
		if !kept {
			continue
		}
		if put, isPut := part.(*store.PutWrite); isPut && put.Table == owner+":blobs" {
			// A blob row's key: the kit's own column, a string.
			if err := parts.blobs.LinkInto(put.Row.Value("id").(string), parts.into); err != nil {
				return nil, false, err
			}
		}
		if _, isManifest := part.(*store.ManifestPart); !isManifest {
			parts.writes++
		}
		return part, true, nil
	}
}

// Close ends the export early.
func (parts *filesParts) Close() error { return parts.iterator.Close() }

// keeps reports whether part belongs to `Files` (the manifest does too); its documents' ids are remembered.
func (parts *filesParts) keeps(part store.ArchivePart) bool {
	switch part := part.(type) {
	case *store.ManifestPart:
		return true
	case *store.TableDefinitionWrite:
		return hasPrefix(part.Table, owner+":")
	case *store.PutWrite:
		return hasPrefix(part.Table, owner+":")
	case *store.IndexWrite:
		return hasPrefix(part.Table, owner+":")
	case *store.DocumentCreate:
		if part.Record.Owner == owner {
			parts.documents[part.Record.ID] = struct{}{}
			return true
		}
		return false
	case *store.DocumentRevisionWrite:
		_, ok := parts.documents[part.ID]
		return ok
	}
	return false
}

func hasPrefix(text, prefix string) bool {
	return len(text) >= len(prefix) && text[:len(prefix)] == prefix
}
