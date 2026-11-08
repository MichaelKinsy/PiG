package app

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Ports packages/chord/src/node/app/blobs.ts
//
// The blobs of one host's files (Files v2): immutable byte files under `<files>/blobs/`, named by id. A blob is written whole
// beside its place and moved there (a reader never sees part of one), never changed, and removed once no version references
// it. Blobs a write left without its index commit (a crash between the two) are swept.

// Blobs are the blob files of one host, in Dir (`<files>/blobs`).
type Blobs struct {
	Dir string
}

// NewBlobs is the blob files of dir.
func NewBlobs(dir string) *Blobs { return &Blobs{Dir: dir} }

// Path is where blob id lives.
func (blobs *Blobs) Path(id string) string { return filepath.Join(blobs.Dir, id) }

// Prepare makes the directory (mode 0700) before a blob moves in.
func (blobs *Blobs) Prepare() error { return os.MkdirAll(blobs.Dir, 0o700) }

// Write writes bytes as blob id: into staging (mode 0600), then moved into place.
func (blobs *Blobs) Write(id string, bytes []byte, staging string) error {
	if err := blobs.Prepare(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(staging), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(bytes); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(staging, blobs.Path(id))
}

// Read is up to length bytes of blob id (of size bytes) from offset: fewer at its end.
func (blobs *Blobs) Read(id string, size, offset, length int64) ([]byte, error) {
	file, err := os.Open(blobs.Path(id))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	buffer := make([]byte, max(0, min(length, size-offset)))
	read, err := file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buffer[:read], nil
}

// Remove removes the blobs ids (a missing one: nothing).
func (blobs *Blobs) Remove(ids []string) error {
	for _, id := range ids {
		if err := os.Remove(blobs.Path(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Sweep removes every blob file known does not know: what writes left that never committed.
func (blobs *Blobs) Sweep(known func(id string) (bool, error)) error {
	entries, err := os.ReadDir(blobs.Dir)
	if err != nil {
		// No directory yet: nothing to sweep.
		return nil
	}
	for _, entry := range entries {
		ok, err := known(entry.Name())
		if err != nil {
			return err
		}
		if !ok {
			if err := os.Remove(blobs.Path(entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// LinkInto puts blob id into target (another host's blob directory): a hard link (blobs never change, so two hosts may share
// the file), a copy where the file system refuses links. A blob already there is an error (EEXIST).
func (blobs *Blobs) LinkInto(id, target string) error {
	err := os.Link(blobs.Path(id), filepath.Join(target, id))
	if err == nil || errors.Is(err, fs.ErrExist) {
		return err
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.EEXIST {
		return err
	}
	return copyExclusive(blobs.Path(id), filepath.Join(target, id))
}

// copyExclusive copies from to to, which must not exist (COPYFILE_EXCL).
func copyExclusive(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		_ = os.Remove(to)
		return err
	}
	return target.Close()
}
