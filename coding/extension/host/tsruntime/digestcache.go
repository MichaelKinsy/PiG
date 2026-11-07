package tsruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

// The digest cache lets a start whose slot executable is unchanged skip hashing it again, across processes.
//
// pig additive (D19): Pi has no runtime slot whose executable a start verifies.
//
// What it vouches for, and nothing more: "the file with this identity had SHA-256 D when PiG hashed it". Verify still compares D with
// the manifest's digest at every start, still requires a Bun executable's digest to be in the lock before anything else, and still
// runs --version. The identity is the device, inode, size, modification time and status-change time (ctime) of the opened file. A
// write to the file changes its ctime, and so does a rename over it (a new inode), a truncation, or any attribute change; unlike the
// modification time, a process cannot set the ctime, so restoring the modification time with utimes after changing the bytes still
// misses. A copy of the file is another inode. When any identity field or the digest differs, the executable is hashed again and the
// record rewritten.
//
// What it does not defend against:
//   - A writer who can create files in the executable's directory can forge a record for a substitute executable: a record holds only
//     what any reader of the file can stat. Such a writer can also rewrite runtime.json, which is user-writable, and the extensions and
//     settings the user runs, so it is inside the trust boundary. The cache adds no way in for another user: a record that the
//     current user does not own, or that group or others can access, is ignored. For a Bun executable the lock still pins which digest
//     a manifest may name, but a forged record bypasses the comparison of the bytes with it.
//   - A file system that does not keep the inode and ctime truthful (some network and FUSE file systems), or a superuser who writes
//     the device or sets the clock back. The hash is a start-time check, not a defence against them, as it was before.
//   - On every platform but Linux, and on Linux for a script, on Android and where /proc does not show the file, the time between the
//     check and the exec: the executable is started by path after Verify returns, with or without the cache. Elsewhere on Linux the
//     launch runs the file that was hashed (executable.go).
//
// A file is recorded only when, at the moment its hash begins, its newest modification time or ctime is at least settleWindow old
// (settleWindow is far more than any timestamp granularity). A write that completed before then is in the identity and in the bytes
// the hash reads; a write after it carries a timestamp later than the newest one the identity holds, so the identity of the file after
// the hash differs and the check fails. Without the window, a write in the same timestamp tick as the one before the hash leaves the
// same modification time and ctime (git's racily-clean index entries) while the hash reads bytes from between the two writes.
// The window is decided before the hash: deciding it after the hash would let a hash that took longer than the window launder a
// file that was fresh when it began. The next start of a file too young to record hashes it again and records it.
//
// The record is one file per executable, <dir>/.verified-<name>, so a provider that removes an image's directory removes its record.
// It is written through an owner-only temporary file and a rename: simultaneous starts each leave a whole record, and a lost race
// costs one more hash. Every failure to read, parse, trust or write it means hash again: it never fails a start.

// settleWindow is how old the newest modification or ctime of a file must be when its hash begins for a digest of it to be recorded.
// pig additive (D19): Pi has no runtime slot whose executable a start verifies.
const settleWindow = 2 * time.Second

// maxDigestRecordBytes bounds the record read at startup; a larger file is treated as corrupt.
// pig additive (D19): Pi has no runtime slot whose executable a start verifies.
const maxDigestRecordBytes = 4 << 10

// fileIdentity is the file system identity of one opened file: device and inode, size, and modification and status-change times in the file system's nanosecond units.
type fileIdentity struct {
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Changed  int64  `json:"changed"`
}

// digestRecord says that the file with Identity had SHA-256 SHA256.
type digestRecord struct {
	Identity fileIdentity `json:"identity"`
	SHA256   string       `json:"sha256"`
}

// vouches reports whether the record holds for a file with identity that the manifest says has digest want.
func (r digestRecord) vouches(identity fileIdentity, want string) bool {
	return r.Identity == identity && r.SHA256 == want
}

// unchangedBy reports whether current, read from the same open file later, shows the file unchanged since i. Unlinking a file sets
// its ctime, so the ctime counts only while the file is still linked.
func (i fileIdentity) unchangedBy(current fileIdentity, unlinked bool) bool {
	return i.Device == current.Device && i.Inode == current.Inode && i.Size == current.Size && i.Modified == current.Modified && (unlinked || i.Changed == current.Changed)
}

// settledAt reports whether identity is old enough at now to be recorded.
func (i fileIdentity) settledAt(now time.Time) bool {
	return now.UnixNano()-max(i.Modified, i.Changed) >= int64(settleWindow)
}

func digestRecordPath(executable string) string {
	return filepath.Join(filepath.Dir(executable), ".verified-"+filepath.Base(executable))
}

// checkDigest verifies that the file of exe has SHA-256 digest want, without hashing it when the record next to it vouches for the
// file's current identity. A different digest is an error exactly as when the file is hashed. A file that changes while it is hashed
// fails the check: the digest describes no single state of it.
func (v *Verifier) checkDigest(exe *Executable, want string) error {
	file, identity := exe.file, exe.identity
	record := digestRecordPath(exe.path)
	if exe.identErr == nil {
		if cached, ok := readDigestRecord(record); ok && cached.vouches(identity, want) {
			return nil
		}
	}
	// Whether the file may be recorded is decided from the identity read before the hash, at the moment the hash begins: a write after
	// that moment gets a later timestamp than settleWindow after the newest one the identity holds, so it cannot hide in the same
	// timestamp tick as the identity.
	settled := exe.identErr == nil && v.settled(identity)
	actual, err := v.sum(file)
	if err != nil {
		return err
	}
	if actual != want {
		return fmt.Errorf("digest %s, manifest says %s", actual, want)
	}
	if exe.identErr != nil {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if current, err := identify(info); err != nil || current != identity {
		return errors.New(exe.path + " changed while it was being verified")
	}
	if settled {
		// Best effort: a read-only slot, or a full disk, only costs the next start its hash.
		_ = writeDigestRecord(record, digestRecord{Identity: identity, SHA256: actual})
	}
	return nil
}

func sumSHA256(r io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// readDigestRecord returns the record at path, or false when it is missing, unreadable, a symbolic link, too large, not a regular file
// (a FIFO or device is opened without blocking and refused) that the current user owns and no one else can access, or not exactly the record shape.
func readDigestRecord(path string) (digestRecord, bool) {
	file, err := openRecord(path)
	if err != nil {
		return digestRecord{}, false
	}
	defer func() { _ = file.Close() }() // Read-only: close cannot lose data.
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxDigestRecordBytes || !ownedByCurrentUser(info) {
		return digestRecord{}, false
	}
	if ownerOnly, err := ownerfile.OwnerOnly(path, info); err != nil || !ownerOnly {
		return digestRecord{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDigestRecordBytes+1))
	if err != nil || len(data) > maxDigestRecordBytes {
		return digestRecord{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record digestRecord
	if err := decoder.Decode(&record); err != nil || decoder.More() {
		return digestRecord{}, false
	}
	return record, true
}

// writeDigestRecord replaces the record at path through an owner-only temporary file in the same directory. It does not sync: a record
// torn by a crash reads as corrupt, which only costs one hash.
func writeDigestRecord(path string, record digestRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp, err := ownerfile.CreateTemp(filepath.Dir(path), ".verifying-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}
