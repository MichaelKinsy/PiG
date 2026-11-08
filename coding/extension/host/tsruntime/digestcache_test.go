//go:build linux || openbsd || dragonfly || solaris || darwin || freebsd || netbsd

package tsruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// cacheFixture is one slot executable and the processes that start it. Every verifier it makes is a new process as far as the digest
// cache is concerned: it shares nothing with the others but the files.
type cacheFixture struct {
	t      *testing.T
	dir    string
	exe    string
	digest string
	hashes atomic.Int32
	probes atomic.Int32
}

const fixtureBytes = 64 << 10

func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	f := &cacheFixture{t: t, dir: t.TempDir()}
	f.exe = filepath.Join(f.dir, "pig-ts-host")
	f.digest = f.write(f.exe, content('a'))
	return f
}

// content is fixtureBytes of b, so that tamper variants keep the size.
func content(b byte) []byte { return []byte(strings.Repeat(string(b), fixtureBytes)) }

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (f *cacheFixture) write(path string, data []byte) string {
	f.t.Helper()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return digestOf(data)
}

// process returns a verifier of a new process that starts a day after the executable was written, so that its digest is recorded.
func (f *cacheFixture) process(lock ...LockEntry) *Verifier {
	v := NewVerifier(lock)
	v.probe = func(context.Context, string) (string, error) {
		f.probes.Add(1)
		return "1.4.2", nil
	}
	v.sum = func(r io.Reader) (string, error) {
		f.hashes.Add(1)
		return sumSHA256(r)
	}
	v.settled = func(identity fileIdentity) bool { return identity.settledAt(time.Now().Add(24 * time.Hour)) }
	return v
}

func (f *cacheFixture) manifest() Manifest {
	return Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: "linux-x64", Provider: "test"}
}

// start runs one start check of exe against digest in a new process and returns how many times the executable was hashed.
func (f *cacheFixture) start(exe, digest string) (hashes int, reason Reason, err error) {
	f.t.Helper()
	f.hashes.Store(0)
	reason, err = verifyClosed(f.process(), f.t.Context(), f.manifest(), exe, digest)
	return int(f.hashes.Load()), reason, err
}

func (f *cacheFixture) record() string { return digestRecordPath(f.exe) }

// prime runs the first start, which hashes the executable and records it.
func (f *cacheFixture) prime() {
	f.t.Helper()
	hashes, reason, err := f.start(f.exe, f.digest)
	if hashes != 1 || err != nil {
		f.t.Fatalf("first start: %d hashes, reason %q, err %v; want 1 and nil", hashes, reason, err)
	}
	if _, err := os.Stat(f.record()); err != nil {
		f.t.Fatalf("first start left no record: %v", err)
	}
}

// pastTick waits until the file's ctime is far enough in the past that a change to it gets another ctime, whatever the file system's
// timestamp granularity.
func pastTick(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identify(info)
	if err != nil {
		t.Fatal(err)
	}
	for time.Now().UnixNano() < identity.Changed+int64(50*time.Millisecond) {
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *cacheFixture) mtime(path string) time.Time {
	f.t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return info.ModTime()
}

// A second process starts an unchanged executable without hashing it, and still runs the start check.
func TestSecondProcessSkipsTheHashOfAnUnchangedExecutable(t *testing.T) {
	f := newCacheFixture(t)
	f.prime()
	f.probes.Store(0)
	hashes, reason, err := f.start(f.exe, f.digest)
	if hashes != 0 || err != nil || reason != "" {
		t.Fatalf("second process: %d hashes, reason %q, err %v; want 0 hashes and success", hashes, reason, err)
	}
	if f.probes.Load() != 1 {
		t.Fatalf("second process ran the --version probe %d times, want 1", f.probes.Load())
	}
	// A third process still skips it.
	if hashes, _, err := f.start(f.exe, f.digest); hashes != 0 || err != nil {
		t.Fatalf("third process: %d hashes, err %v", hashes, err)
	}
}

// The cache vouches for the digest only: a cache hit does not skip the probe, so a runtime that stopped starting still fails.
func TestCacheHitStillRunsAndHonoursTheProbe(t *testing.T) {
	f := newCacheFixture(t)
	f.prime()
	v := f.process()
	v.probe = func(context.Context, string) (string, error) { return "", errors.New("exit status 1") }
	f.hashes.Store(0)
	reason, err := verifyClosed(v, t.Context(), f.manifest(), f.exe, f.digest)
	if reason != ReasonStartFailed || err == nil || f.hashes.Load() != 0 {
		t.Fatalf("reason %q, err %v, %d hashes; want start_failed from the probe with no hash", reason, err, f.hashes.Load())
	}
}

// The executable and the record are the same files for every process, so a changed executable is caught however it was changed.
func TestTamperedExecutableMissesTheCacheAndFailsTheDigest(t *testing.T) {
	tampers := map[string]func(t *testing.T, f *cacheFixture){
		// Same inode, size and modification time: only the ctime tells.
		"bytes changed in place with the modification time restored": func(t *testing.T, f *cacheFixture) {
			mtime := f.mtime(f.exe)
			pastTick(t, f.exe)
			file, err := os.OpenFile(f.exe, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteAt([]byte("X"), 100); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(f.exe, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		},
		// Same size and modification time: a new inode and ctime.
		"replaced by a rename with the same size and the modification time restored": func(t *testing.T, f *cacheFixture) {
			mtime := f.mtime(f.exe)
			pastTick(t, f.exe)
			replacement := filepath.Join(f.dir, "replacement")
			f.write(replacement, content('b'))
			if err := os.Chtimes(replacement, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, f.exe); err != nil {
				t.Fatal(err)
			}
		},
		"truncated": func(t *testing.T, f *cacheFixture) {
			pastTick(t, f.exe)
			if err := os.Truncate(f.exe, fixtureBytes/2); err != nil {
				t.Fatal(err)
			}
		},
		"extended": func(t *testing.T, f *cacheFixture) {
			pastTick(t, f.exe)
			file, err := os.OpenFile(f.exe, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString("more"); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		},
		// The record travels with the copy, as it does when a directory is copied with cp -a; the copy is another inode on the same device.
		"copied with the record and the modification time to another path": func(t *testing.T, f *cacheFixture) {
			other := t.TempDir()
			mtime := f.mtime(f.exe)
			copied := filepath.Join(other, filepath.Base(f.exe))
			f.write(copied, content('b'))
			if err := os.Chtimes(copied, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			record, err := os.ReadFile(f.record())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(digestRecordPath(copied), record, 0o600); err != nil {
				t.Fatal(err)
			}
			f.exe = copied
		},
	}
	for name, tamper := range tampers {
		t.Run(name, func(t *testing.T) {
			f := newCacheFixture(t)
			f.prime()
			tamper(t, f)
			hashes, reason, err := f.start(f.exe, f.digest)
			if hashes != 1 || reason != ReasonVerifyFailed || err == nil || !strings.Contains(err.Error(), "manifest says "+f.digest) {
				t.Fatalf("after the tamper: %d hashes, reason %q, err %v; want 1 hash and verify_failed with the digest mismatch", hashes, reason, err)
			}
		})
	}
}

// A file replaced by one with the same bytes is hashed again, and passes: the cache is conservative, never wrong.
func TestRewrittenExecutableWithTheSameBytesIsHashedAgainAndPasses(t *testing.T) {
	f := newCacheFixture(t)
	f.prime()
	pastTick(t, f.exe)
	f.write(f.exe, content('a'))
	hashes, reason, err := f.start(f.exe, f.digest)
	if hashes != 1 || err != nil || reason != "" {
		t.Fatalf("%d hashes, reason %q, err %v; want 1 hash and success", hashes, reason, err)
	}
	if hashes, _, err := f.start(f.exe, f.digest); hashes != 0 || err != nil {
		t.Fatalf("the next start: %d hashes, err %v; want the new state recorded", hashes, err)
	}
}

// Every component of the identity, and the digest, is part of the key: a record that differs in one of them vouches for nothing.
func TestRecordVouchesOnlyForEveryIdentityComponentAndTheDigest(t *testing.T) {
	mutations := map[string]func(r *digestRecord){
		"device":   func(r *digestRecord) { r.Identity.Device++ },
		"inode":    func(r *digestRecord) { r.Identity.Inode++ },
		"size":     func(r *digestRecord) { r.Identity.Size++ },
		"modified": func(r *digestRecord) { r.Identity.Modified++ },
		"changed":  func(r *digestRecord) { r.Identity.Changed++ },
		"digest":   func(r *digestRecord) { r.SHA256 = strings.Repeat("0", 64) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := newCacheFixture(t)
			f.prime()
			record, ok := readDigestRecord(f.record())
			if !ok {
				t.Fatal("no record after the first start")
			}
			mutate(&record)
			if err := writeDigestRecord(f.record(), record); err != nil {
				t.Fatal(err)
			}
			if hashes, _, err := f.start(f.exe, f.digest); hashes != 1 || err != nil {
				t.Fatalf("a record that differs in %s: %d hashes, err %v; want 1 hash and success", name, hashes, err)
			}
			// The hash rewrote the record, so the next start trusts it again.
			if hashes, _, err := f.start(f.exe, f.digest); hashes != 0 || err != nil {
				t.Fatalf("after the rewrite: %d hashes, err %v; want 0", hashes, err)
			}
		})
	}
}

// A record that is not exactly what a start of this executable wrote is ignored, and the start that ignores it rewrites it.
func TestUntrustedRecordIsIgnoredAndRewritten(t *testing.T) {
	good := func(t *testing.T, f *cacheFixture) []byte {
		data, err := os.ReadFile(f.record())
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	replace := func(t *testing.T, f *cacheFixture, data []byte, mode os.FileMode) {
		if err := os.Remove(f.record()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.record(), data, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f.record(), mode); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]func(t *testing.T, f *cacheFixture){
		"garbage":   func(t *testing.T, f *cacheFixture) { replace(t, f, []byte("not json"), 0o600) },
		"empty":     func(t *testing.T, f *cacheFixture) { replace(t, f, nil, 0o600) },
		"truncated": func(t *testing.T, f *cacheFixture) { data := good(t, f); replace(t, f, data[:len(data)/2], 0o600) },
		"trailing data": func(t *testing.T, f *cacheFixture) {
			replace(t, f, append(good(t, f), []byte(`{}`)...), 0o600)
		},
		"unknown field": func(t *testing.T, f *cacheFixture) {
			replace(t, f, []byte(strings.Replace(string(good(t, f)), `"sha256"`, `"extra":1,"sha256"`, 1)), 0o600)
		},
		"too large": func(t *testing.T, f *cacheFixture) {
			replace(t, f, append(good(t, f), []byte(strings.Repeat(" ", maxDigestRecordBytes))...), 0o600)
		},
		"readable by others": func(t *testing.T, f *cacheFixture) { replace(t, f, good(t, f), 0o644) },
		"writable by group":  func(t *testing.T, f *cacheFixture) { replace(t, f, good(t, f), 0o660) },
		// What a start of another executable, in this directory's name, wrote.
		"another file's record": func(t *testing.T, f *cacheFixture) {
			other := newCacheFixture(t)
			other.write(other.exe, content('c'))
			pastTick(t, other.exe)
			other.digest = digestOf(content('c'))
			other.prime()
			replace(t, f, good(t, other), 0o600)
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCacheFixture(t)
			f.prime()
			corrupt(t, f)
			if hashes, _, err := f.start(f.exe, f.digest); hashes != 1 || err != nil {
				t.Fatalf("%d hashes, err %v; want 1 hash and success", hashes, err)
			}
			if info, err := os.Stat(f.record()); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatalf("record after the start: %v, %v; want a regular 0600 file", info, err)
			}
			if hashes, _, err := f.start(f.exe, f.digest); hashes != 0 || err != nil {
				t.Fatalf("after the rewrite: %d hashes, err %v; want 0", hashes, err)
			}
		})
	}
}

// A record path that cannot be replaced (here a directory) costs a hash at every start and fails none.
func TestUnreplaceableRecordCostsOnlyTheHash(t *testing.T) {
	f := newCacheFixture(t)
	if err := os.Mkdir(f.record(), 0o700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if hashes, _, err := f.start(f.exe, f.digest); hashes != 1 || err != nil {
			t.Fatalf("%d hashes, err %v; want 1 hash and success", hashes, err)
		}
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("directory holds %d entries, want the executable and the directory at the record path, no temporary file", len(entries))
	}
}

// A record that another user owns is not trusted, even when it is owner-only and names the file's identity: the cache adds no way in for
// another user who can write the executable's directory.
func TestRecordOfAnotherUserIsNotTrusted(t *testing.T) {
	own := t.TempDir()
	info, err := os.Stat(own)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCurrentUser(info) {
		t.Fatal("a directory this process created is not owned by the current user")
	}
	if os.Geteuid() != 0 {
		root, err := os.Stat("/")
		if err != nil {
			t.Fatal(err)
		}
		if ownedByCurrentUser(root) {
			t.Fatalf("/ is owned by uid 0, not by the current user %d", os.Geteuid())
		}
		return
	}
	// As root the record can be handed to another user.
	f := newCacheFixture(t)
	f.prime()
	if err := os.Chown(f.record(), 65534, -1); err != nil {
		t.Fatal(err)
	}
	if hashes, _, err := f.start(f.exe, f.digest); hashes != 1 || err != nil {
		t.Fatalf("a record of another user: %d hashes, err %v; want 1 hash", hashes, err)
	}
}

// A manifest that names another digest fails exactly as before, whatever the record holds.
func TestChangedManifestDigestFailsAndRecordKeepsOnlyTheTrueDigest(t *testing.T) {
	f := newCacheFixture(t)
	f.prime()
	wrong := strings.Repeat("b", 64)
	hashes, reason, err := f.start(f.exe, wrong)
	want := "digest " + f.digest + ", manifest says " + wrong
	if hashes != 1 || reason != ReasonVerifyFailed || err == nil || err.Error() != want {
		t.Fatalf("%d hashes, reason %q, err %v; want 1 hash and %q", hashes, reason, err, want)
	}
	record, ok := readDigestRecord(f.record())
	if !ok || record.SHA256 != f.digest {
		t.Fatalf("record after the failed start: %+v, %v; want the executable's true digest", record, ok)
	}
	// The true digest still passes from the record.
	if hashes, _, err := f.start(f.exe, f.digest); hashes != 0 || err != nil {
		t.Fatalf("true digest after the failed one: %d hashes, err %v", hashes, err)
	}
}

// A record, however it came to be, does not make a Bun executable run that the lock does not pin: the lock check comes before the
// digest and the probe.
func TestLockCheckStaysBeforeTheCache(t *testing.T) {
	f := newCacheFixture(t)
	m := f.manifest()
	m.Kind, m.Version = KindBun, "1.4.2"
	locked := LockEntry{Runtime: "bun", Version: "1.4.2", Platform: m.Platform, SHA256: f.digest}
	if reason, err := verifyClosed(f.process(locked), t.Context(), m, f.exe, f.digest); err != nil || reason != "" {
		t.Fatalf("locked first start: %q, %v", reason, err)
	}
	if _, ok := readDigestRecord(f.record()); !ok {
		t.Fatal("no record after a locked start")
	}
	f.hashes.Store(0)
	f.probes.Store(0)
	reason, err := verifyClosed(f.process(), t.Context(), m, f.exe, f.digest)
	if reason != ReasonVerifyFailed || err == nil || !strings.Contains(err.Error(), "not in the runtime lock") || f.hashes.Load() != 0 || f.probes.Load() != 0 {
		t.Fatalf("unlocked start over a record: reason %q, err %v, %d hashes, %d probes; want verify_failed from the lock before hash and probe", reason, err, f.hashes.Load(), f.probes.Load())
	}
}

// A file that is too new to tell apart from a second write in the same timestamp tick is hashed at every start until it settles. The
// clock is the file's newest time plus an offset, so the test does not depend on how long it takes to run.
func TestUnsettledExecutableIsNotRecorded(t *testing.T) {
	f := newCacheFixture(t)
	info, err := os.Stat(f.exe)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identify(info)
	if err != nil {
		t.Fatal(err)
	}
	newest := time.Unix(0, max(identity.Modified, identity.Changed))
	start := func(now time.Time) {
		t.Helper()
		v := f.process()
		v.settled = func(identity fileIdentity) bool { return identity.settledAt(now) }
		f.hashes.Store(0)
		if _, err := verifyClosed(v, t.Context(), f.manifest(), f.exe, f.digest); err != nil || f.hashes.Load() != 1 {
			t.Fatalf("start at %v after the newest time: %d hashes, err %v; want 1 hash", now.Sub(newest), f.hashes.Load(), err)
		}
	}
	for _, age := range []time.Duration{0, settleWindow - 1} {
		start(newest.Add(age))
		if _, err := os.Stat(f.record()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a file %v old was recorded: %v", age, err)
		}
	}
	start(newest.Add(settleWindow))
	if _, ok := readDigestRecord(f.record()); !ok {
		t.Fatalf("a file %v old was not recorded", settleWindow)
	}
}

// Bytes that change while they are hashed fail the check and are not recorded: the identity read before no longer describes what was
// read, and the digest describes no single state of the file.
func TestExecutableChangedWhileHashedFailsAndIsNotRecorded(t *testing.T) {
	f := newCacheFixture(t)
	pastTick(t, f.exe)
	v := f.process()
	v.sum = func(r io.Reader) (string, error) {
		sum, err := sumSHA256(r)
		file, openErr := os.OpenFile(f.exe, os.O_WRONLY|os.O_APPEND, 0)
		if openErr != nil {
			t.Error(openErr)
			return sum, err
		}
		_, writeErr := file.WriteString("x")
		if err := errors.Join(writeErr, file.Close()); err != nil {
			t.Error(err)
		}
		return sum, err
	}
	// The digest it read is the one the manifest names, but the executable changed after it was read.
	reason, err := verifyClosed(v, t.Context(), f.manifest(), f.exe, f.digest)
	if reason != ReasonVerifyFailed || err == nil || !strings.Contains(err.Error(), "changed while it was being verified") {
		t.Fatalf("reason %q, err %v; want verify_failed for a file that changed while it was hashed", reason, err)
	}
	if _, err := os.Stat(f.record()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a record of an executable that changed while it was hashed: %v", err)
	}
}

// Whether a file is settled is decided when its hash begins. A file that is too young then is not recorded even when the hash takes
// longer than the settle window: a write in the same timestamp tick as the newest time in its identity can land while the hash reads.
func TestFileUnsettledWhenTheHashBeginsIsNotRecordedHoweverLongTheHashTakes(t *testing.T) {
	f := newCacheFixture(t)
	info, err := os.Stat(f.exe)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identify(info)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, max(identity.Modified, identity.Changed)).Add(time.Millisecond)
	v := f.process()
	v.settled = func(i fileIdentity) bool { return i.settledAt(now) }
	v.sum = func(r io.Reader) (string, error) {
		// The hash outlasts the settle window.
		now = now.Add(settleWindow + time.Second)
		return sumSHA256(r)
	}
	if reason, err := verifyClosed(v, t.Context(), f.manifest(), f.exe, f.digest); err != nil {
		t.Fatalf("%s: %v", reason, err)
	}
	if _, err := os.Stat(f.record()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file that was %v old when its hash began was recorded after the hash: %v", time.Millisecond, err)
	}
}

// A record that is a symbolic link is ignored, even one that points to a valid record of this file.
func TestSymlinkedRecordIsIgnored(t *testing.T) {
	f := newCacheFixture(t)
	f.prime()
	real := filepath.Join(f.dir, "real-record")
	if err := os.Rename(f.record(), real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, f.record()); err != nil {
		t.Fatal(err)
	}
	if _, ok := readDigestRecord(f.record()); ok {
		t.Fatal("a symbolic link was read as a record")
	}
	if hashes, _, err := f.start(f.exe, f.digest); hashes != 1 || err != nil {
		t.Fatalf("%d hashes, err %v; want 1 hash and success", hashes, err)
	}
}

// A FIFO at the record path is refused without blocking on it.
func TestFIFORecordIsIgnoredWithoutBlocking(t *testing.T) {
	f := newCacheFixture(t)
	if err := syscall.Mkfifo(f.record(), 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := readDigestRecord(f.record())
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a FIFO was read as a record")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO record blocked")
	}
	// The start does not block on it either: it hashes and replaces the FIFO with its record.
	finished := make(chan int, 1)
	go func() {
		hashes, _, _ := f.start(f.exe, f.digest)
		finished <- hashes
	}()
	select {
	case hashes := <-finished:
		if hashes != 1 {
			t.Fatalf("%d hashes, want 1", hashes)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a start blocked on a FIFO record")
	}
}

// A regular owner-only record is still read.
func TestRegularRecordIsStillRead(t *testing.T) {
	f := newCacheFixture(t)
	f.prime()
	if record, ok := readDigestRecord(f.record()); !ok || record.SHA256 != f.digest {
		t.Fatalf("record %+v, %v; want the recorded digest", record, ok)
	}
}

// A slot the user cannot write only loses the cache.
func TestReadOnlySlotStillVerifies(t *testing.T) {
	f := newCacheFixture(t)
	if err := os.Chmod(f.dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.dir, 0o755) })
	hashes, reason, err := f.start(f.exe, f.digest)
	if hashes != 1 || err != nil || reason != "" {
		t.Fatalf("%d hashes, reason %q, err %v; want 1 hash and success", hashes, reason, err)
	}
	if os.Geteuid() != 0 {
		if _, err := os.Stat(f.record()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a record in a read-only directory: %v", err)
		}
	}
}

// Many processes that start the same new executable together each leave a whole record, none leaves a partial one or a stray file,
// and the next start trusts the result. Run under -race.
func TestConcurrentStartsLeaveOneWholeRecord(t *testing.T) {
	f := newCacheFixture(t)
	const starts = 16
	var wg sync.WaitGroup
	errs := make(chan error, starts)
	begin := make(chan struct{})
	for range starts {
		wg.Go(func() {
			<-begin
			if _, err := verifyClosed(f.process(), t.Context(), f.manifest(), f.exe, f.digest); err != nil {
				errs <- err
			}
		})
	}
	close(begin)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 2 {
		t.Fatalf("directory holds %v, want the executable and its record", names)
	}
	record, ok := readDigestRecord(f.record())
	if !ok || record.SHA256 != f.digest {
		t.Fatalf("record after %d simultaneous starts: %+v, %v", starts, record, ok)
	}
	if hashes, _, err := f.start(f.exe, f.digest); hashes != 0 || err != nil {
		t.Fatalf("start after the race: %d hashes, err %v; want 0", hashes, err)
	}
}

// A start that races a replacement of the executable never lets an older record vouch for the new file.
func TestConcurrentReplacementNeverReusesTheOldRecord(t *testing.T) {
	f := newCacheFixture(t)
	f.prime()
	oldRecord, err := os.ReadFile(f.record())
	if err != nil {
		t.Fatal(err)
	}
	pastTick(t, f.exe)
	replacement := filepath.Join(f.dir, "replacement")
	newDigest := f.write(replacement, content('b'))
	if err := os.Rename(replacement, f.exe); err != nil {
		t.Fatal(err)
	}
	// A slow start that hashed the old file lands its record after the replacement.
	if err := os.WriteFile(f.record(), oldRecord, 0o600); err != nil {
		t.Fatal(err)
	}
	if hashes, _, err := f.start(f.exe, newDigest); hashes != 1 || err != nil {
		t.Fatalf("%d hashes, err %v; want the new file hashed", hashes, err)
	}
	if hashes, _, err := f.start(f.exe, f.digest); err == nil {
		t.Fatalf("the old digest passed for the new file with %d hashes", hashes)
	}
}
