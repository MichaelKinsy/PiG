package signature

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// countHashes counts how often a check hashes executable bytes.
func countHashes(t *testing.T) *int {
	t.Helper()
	count := 0
	original := hashExecutable
	hashExecutable = func(file io.ReaderAt, size int64) (string, error) {
		count++
		return original(file, size)
	}
	t.Cleanup(func() { hashExecutable = original })
	return &count
}

type cacheFixture struct {
	path      string
	signed    []byte
	policy    Policy
	cachePath string
	hashes    *int
}

func newCacheFixture(t *testing.T) cacheFixture {
	t.Helper()
	key := newKey(t)
	path, signed := signedFixture(t, key)
	return cacheFixture{
		path:      path,
		signed:    signed,
		policy:    Policy{Embedded: []ed25519.PublicKey{publicOf(key)}, RequireKnownSigner: true},
		cachePath: filepath.Join(t.TempDir(), "state", "piglet-verify", "verified.json"),
		hashes:    countHashes(t),
	}
}

// check runs one cached check that must pass and remembers it.
func (f cacheFixture) check(t *testing.T) {
	t.Helper()
	f.checkPath(t, f.path, f.policy)
}

func (f cacheFixture) checkPath(t *testing.T, path string, policy Policy) {
	t.Helper()
	check, err := CheckCached(path, policy, f.cachePath)
	if err != nil {
		t.Fatalf("CheckCached() = %v", err)
	}
	if !check.Status.Signed || !check.Status.Embedded {
		t.Fatalf("CheckCached() status = %+v", check.Status)
	}
	if err := check.Remember(); err != nil {
		t.Fatalf("Remember() = %v", err)
	}
}

func (f cacheFixture) wantHashes(t *testing.T, want int) {
	t.Helper()
	if *f.hashes != want {
		t.Fatalf("executable hashed %d times, want %d", *f.hashes, want)
	}
}

func (f cacheFixture) readCache(t *testing.T) verifyCacheFile {
	t.Helper()
	data, err := os.ReadFile(f.cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cache verifyCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	return cache
}

func (f cacheFixture) writeCache(t *testing.T, cache verifyCacheFile) {
	t.Helper()
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.cachePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckCached_SecondCheckOfAnUnchangedBinarySkipsTheHash(t *testing.T) {
	f := newCacheFixture(t)
	f.check(t)
	f.wantHashes(t, 1)
	f.check(t)
	f.check(t)
	f.wantHashes(t, 1)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(f.cachePath)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("cache mode = %v, want 0600", mode)
		}
	}
	resolved, err := filepath.EvalSymlinks(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if cache := f.readCache(t); len(cache.Entries) != 1 || cache.Entries[0].Path != resolved {
		t.Fatalf("cache = %+v, want one entry for %s", cache, resolved)
	}
}

func TestCheckCached_ChangedIdentityOrPolicyRehashes(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, f *cacheFixture){
		"mtime": func(t *testing.T, f *cacheFixture) {
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(f.path, later, later); err != nil {
				t.Fatal(err)
			}
		},
		"size": func(t *testing.T, f *cacheFixture) {
			cache := f.readCache(t)
			cache.Entries[0].Identity.Size++
			f.writeCache(t, cache)
		},
		"replaced file": func(t *testing.T, f *cacheFixture) {
			info, err := os.Stat(f.path)
			if err != nil {
				t.Fatal(err)
			}
			replacement := f.path + ".new"
			if err := os.WriteFile(replacement, f.signed, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, f.path); err != nil {
				t.Fatal(err)
			}
		},
		"trust policy": func(t *testing.T, f *cacheFixture) {
			f.policy.Trust.RequireSignature = true
			f.policy.Trust.Keys = map[string]ed25519.PublicKey{KeyID(f.policy.Embedded[0]): f.policy.Embedded[0]}
		},
		"pinned signer": func(t *testing.T, f *cacheFixture) {
			f.policy.Embedded = append(f.policy.Embedded, publicOf(newKey(t)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCacheFixture(t)
			f.check(t)
			f.check(t)
			f.wantHashes(t, 1)
			change(t, &f)
			f.check(t)
			f.wantHashes(t, 2)
			f.check(t)
			f.wantHashes(t, 2)
		})
	}
}

// A cache entry vouches only for the path it was recorded for, even when
// another path reaches the same file.
func TestCheckCached_EntryForAnotherPathIsNotTrusted(t *testing.T) {
	f := newCacheFixture(t)
	f.check(t)
	link := filepath.Join(t.TempDir(), "pig-linked")
	if err := os.Link(f.path, link); err != nil {
		t.Skipf("hard link unavailable: %v", err)
	}
	f.checkPath(t, link, f.policy)
	f.wantHashes(t, 2)
	if cache := f.readCache(t); len(cache.Entries) != 2 {
		t.Fatalf("cache has %d entries, want one per path", len(cache.Entries))
	}
}

func TestCheckCached_TamperedByteWithChangedIdentityFails(t *testing.T) {
	f := newCacheFixture(t)
	f.check(t)
	file, err := os.OpenFile(f.path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{f.signed[100] ^ 0x01}, 100); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(f.path, later, later); err != nil {
		t.Fatal(err)
	}
	_, err = CheckCached(f.path, f.policy, f.cachePath)
	if err == nil || !strings.Contains(err.Error(), "executable bytes changed after signing") {
		t.Fatalf("CheckCached() of a tampered Binary = %v", err)
	}
	f.wantHashes(t, 2)
}

// currentIdentity reads the file identity CheckCached would record for path.
func currentIdentity(t *testing.T, path string) fileIdentity {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identify(file, info)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// A writer that changes a byte in place and restores the modification time
// keeps the path, device, inode, size, and modification time. Only the
// status-change time (on Windows, the change time) records the write, so
// the next start must hash the file and refuse it.
func TestCheckCached_InPlaceWriteThatRestoresTheModificationTimeRehashes(t *testing.T) {
	f := newCacheFixture(t)
	f.check(t)
	info, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	recorded := f.readCache(t).Entries[0].Identity
	// A coarse file system clock can give the write the recorded change time; rewrite until the clock has advanced.
	deadline := time.Now().Add(5 * time.Second)
	for {
		file, err := os.OpenFile(f.path, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteAt([]byte{f.signed[100] ^ 0x01}, 100); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(f.path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		current := currentIdentity(t, f.path)
		if current.Device != recorded.Device || current.Inode != recorded.Inode || current.Size != recorded.Size || current.Modified != recorded.Modified {
			t.Fatalf("in-place write changed more than the change time: recorded %+v, now %+v", recorded, current)
		}
		if current.Changed != recorded.Changed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the file identity never recorded the in-place write: %+v", current)
		}
	}
	_, err = CheckCached(f.path, f.policy, f.cachePath)
	if err == nil || !strings.Contains(err.Error(), "executable bytes changed after signing") {
		t.Fatalf("CheckCached() of a Binary rewritten in place = %v", err)
	}
	f.wantHashes(t, 2)
}

func TestCheckCached_UnusableCacheMeansAFullCheck(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, f cacheFixture){
		"corrupt": func(t *testing.T, f cacheFixture) {
			if err := os.WriteFile(f.cachePath, []byte(`{"entries":[{"path":`), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"unknown field": func(t *testing.T, f cacheFixture) {
			cache := f.readCache(t)
			data, err := json.Marshal(cache)
			if err != nil {
				t.Fatal(err)
			}
			data = append([]byte(`{"version":1,`), data[1:]...)
			if err := os.WriteFile(f.cachePath, data, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"unreadable": func(t *testing.T, f cacheFixture) {
			if err := os.Remove(f.cachePath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(f.cachePath, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"not owner-only": func(t *testing.T, f cacheFixture) {
			if runtime.GOOS == "windows" {
				t.Skip("Windows owner-only files use a DACL, not mode bits")
			}
			if err := os.Chmod(f.cachePath, 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCacheFixture(t)
			f.check(t)
			damage(t, f)
			check, err := CheckCached(f.path, f.policy, f.cachePath)
			if err != nil || !check.Status.Signed {
				t.Fatalf("CheckCached() = %+v, %v", check.Status, err)
			}
			f.wantHashes(t, 2)
		})
	}
}

func TestCheckCached_FailedVerificationIsNeverRemembered(t *testing.T) {
	for name, fail := range map[string]func(t *testing.T, f *cacheFixture){
		"tampered": func(t *testing.T, f *cacheFixture) {
			tampered := append([]byte(nil), f.signed...)
			tampered[100] ^= 0x01
			if err := os.WriteFile(f.path, tampered, 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"unknown signer": func(t *testing.T, f *cacheFixture) {
			f.policy.Embedded = []ed25519.PublicKey{publicOf(newKey(t))}
		},
		"revoked signer": func(t *testing.T, f *cacheFixture) {
			f.policy.Trust.Revoked = map[string]bool{KeyID(f.policy.Embedded[0]): true}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCacheFixture(t)
			fail(t, &f)
			check, err := CheckCached(f.path, f.policy, f.cachePath)
			if err == nil {
				t.Fatal("CheckCached() passed a Binary that must fail")
			}
			if err := check.Remember(); err != nil {
				t.Fatalf("Remember() after a failed check = %v", err)
			}
			if _, err := os.Stat(f.cachePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a failed check wrote the cache: %v", err)
			}
			if _, err := CheckCached(f.path, f.policy, f.cachePath); err == nil {
				t.Fatal("second CheckCached() passed a Binary that must fail")
			}
			f.wantHashes(t, 2)
		})
	}
}

// A file that changes while it is hashed is not remembered under the
// identity it had before.
func TestCheckCached_FileChangedDuringTheHashIsNotRemembered(t *testing.T) {
	f := newCacheFixture(t)
	counting := hashExecutable
	hashExecutable = func(file io.ReaderAt, size int64) (string, error) {
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(f.path, later, later); err != nil {
			t.Fatal(err)
		}
		return counting(file, size)
	}
	check, err := CheckCached(f.path, f.policy, f.cachePath)
	if err != nil {
		t.Fatal(err)
	}
	hashExecutable = counting
	if err := check.Remember(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file changed during its hash was remembered: %v", err)
	}
	f.check(t)
	f.check(t)
	f.wantHashes(t, 2)
}

// pig verify and pig piglet verify call Check, which always hashes.
func TestCheck_IgnoresTheVerificationCache(t *testing.T) {
	f := newCacheFixture(t)
	f.check(t)
	for range 2 {
		if _, err := Check(f.path, f.policy); err != nil {
			t.Fatal(err)
		}
	}
	f.wantHashes(t, 3)
}

func TestCheckCached_KeepsOneEntryPerPathAndEvictsTheOldest(t *testing.T) {
	f := newCacheFixture(t)
	dir := t.TempDir()
	paths := make([]string, verifyCacheEntries+2)
	for i := range paths {
		paths[i] = filepath.Join(dir, "pig-"+string(rune('a'+i)))
		if err := os.WriteFile(paths[i], f.signed, 0o755); err != nil {
			t.Fatal(err)
		}
		f.checkPath(t, paths[i], f.policy)
	}
	f.checkPath(t, paths[len(paths)-1], f.policy)
	cache := f.readCache(t)
	if len(cache.Entries) != verifyCacheEntries {
		t.Fatalf("cache has %d entries, want %d", len(cache.Entries), verifyCacheEntries)
	}
	kept := map[string]bool{}
	for _, entry := range cache.Entries {
		kept[filepath.Base(entry.Path)] = true
	}
	for i, path := range paths {
		if want := i >= 2; kept[filepath.Base(path)] != want {
			t.Fatalf("entry for %s kept = %v, want %v (cache %+v)", filepath.Base(path), !want, want, cache)
		}
	}
}

func TestCheckCached_RememberFailureKeepsTheResult(t *testing.T) {
	f := newCacheFixture(t)
	blocker := filepath.Dir(f.cachePath)
	if err := os.MkdirAll(filepath.Dir(blocker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	check, err := CheckCached(f.path, f.policy, f.cachePath)
	if err != nil || !check.Status.Signed {
		t.Fatalf("CheckCached() = %+v, %v", check.Status, err)
	}
	if err := check.Remember(); err == nil {
		t.Fatal("Remember() into a blocked state directory reported no error")
	}
}
