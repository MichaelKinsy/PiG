package sessions

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports apps/p2/plugins/sessions/copy.ts: copies a session's data to a new session ID (`Sessions.clone`,
// `Sessions.fork`). A clone copies the whole session directory (`sessions/<id>/`: its store, copied while the session's
// worker is stopped so the SQLite file and its WAL belong together, and its files, whose immutable blobs are
// hard-linked), so the copy's artifact cards, attachments and downloads resolve under the new ID. Nothing is
// overwritten; directories are 0700, files 0600, links skipped. A fork's files are copied by the source's worker
// (`SessionTransfer.copyFiles`), its history seeded into a fresh store.

// privateCopy copies a private file or directory tree; a missing source is nothing to copy. Uploads under way
// (`.uploads`) are left out; blobs (never changed) are hard-linked where the file system allows it.
func privateCopy(ctx context.Context, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := os.Mkdir(to, 0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".uploads" {
				continue
			}
			if err := privateCopy(ctx, filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())); err != nil {
				return err
			}
		}
	case info.Mode().IsRegular() && strings.Contains(from, string(filepath.Separator)+"blobs"+string(filepath.Separator)):
		if os.Link(from, to) != nil {
			return copyFile(from, to)
		}
	case info.Mode().IsRegular():
		return copyFile(from, to)
	}
	return nil
}

// copyFile copies a regular file to a new private file (never over an existing one).
func copyFile(from, to string) (err error) {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, target.Close()) }()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return os.Chmod(to, 0o600)
}

// CopySessionDirectory copies the session's directory (its store with its WAL, its files); call only while no worker
// has it open.
func CopySessionDirectory(ctx context.Context, dataDir, from, to string) error {
	source, err := p2runtime.SessionDirectory(dataDir, from)
	if err != nil {
		return err
	}
	target, err := p2runtime.SessionDirectory(dataDir, to)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o700); err != nil {
		return err
	}
	return privateCopy(ctx, source, target)
}

// RemoveSessionCopy removes what a failed copy left for the session id (its row is archived, its worker stopped).
func RemoveSessionCopy(dataDir, id string) error {
	directory, err := p2runtime.SessionDirectory(dataDir, id)
	if err != nil {
		return err
	}
	return errors.Join(
		os.RemoveAll(directory),
		// Its attachments materialized on this machine, if any.
		os.RemoveAll(filepath.Join(dataDir, "uploads", id)),
	)
}
