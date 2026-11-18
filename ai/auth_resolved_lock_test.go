//go:build unix

package ai

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

func setKey(t *testing.T, s *AuthStorage, provider, key string) {
	t.Helper()
	if _, err := s.Modify(t.Context(), provider, func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: key}, nil
	}); err != nil {
		t.Fatalf("Modify %s: %v", provider, err)
	}
}

func fixtureKey(t *testing.T, s *AuthStorage, provider string) string {
	t.Helper()
	c, err := s.Read(t.Context(), provider)
	if err != nil || c == nil {
		t.Fatalf("Read %s = %v, %v", provider, c, err)
	}
	return c.Key
}

func noTemps(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "credentials-tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// holdsLockBeside reports whether the lock folder exists beside path while fn runs under store's lock.
func holdsLockBeside(t *testing.T, s *AuthStorage, want, notWant string) {
	t.Helper()
	err := s.withFileLock(t.Context(), false, func(*pilock.Lock) error {
		if fi, err := os.Stat(want + ".lock"); err != nil || !fi.IsDir() {
			t.Errorf("no lock folder beside %s: %v", want, err)
		}
		if _, err := os.Lstat(notWant + ".lock"); err == nil {
			t.Errorf("lock taken at the link path %s", notWant)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// R1 (B1): the resolved folder cannot be written, the link's folder can. The write fails naming the resolved folder.
func TestR1UnwritableResolvedFolderFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	s1, _, _, _, target := linkedStores(t, `{"p":{"type":"api_key","key":"fixture-old"}}`)
	dir := filepath.Dir(target)
	before, _ := os.ReadFile(target)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := s1.Modify(t.Context(), "p", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "fixture-new"}, nil
	})
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want one naming %s", err, dir)
	}
	t.Logf("B1 lock error: %v", err)
	after, _ := os.ReadFile(target)
	if string(before) != string(after) {
		t.Fatal("target changed")
	}
	noTemps(t, dir)
}

// R3a: permission bits are kept.
func TestR3aPermissionBitsKept(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o640} {
		s1, _, _, _, target := linkedStores(t, `{}`)
		if err := os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}
		setKey(t, s1, "p", "fixture-refresh-A")
		fi, _ := os.Stat(target)
		if fi.Mode().Perm() != mode {
			t.Errorf("mode = %v, want %v", fi.Mode().Perm(), mode)
		}
	}
}

// R5: a dangling link.
func TestR5DanglingLink(t *testing.T) {
	s1, s2, link1, _, target := linkedStores(t, "")
	if _, err := os.Stat(target); err == nil {
		t.Fatal("target should be missing")
	}
	before, _ := resolveAuthPath(link1)
	holdsLockBeside(t, s1, target, link1)
	setKey(t, s1, "p", "fixture-refresh-A")
	if after, _ := resolveAuthPath(link1); after != before {
		t.Fatalf("lock path changed when the target was created: %q then %q", before, after)
	}
	fi, err := os.Stat(target)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("target after first write: %v, %v", fi, err)
	}
	if li, err := os.Lstat(link1); err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link is no longer a link: %v, %v", li, err)
	}
	if got := fixtureKey(t, s2, "p"); got != "fixture-refresh-A" {
		t.Fatalf("second store sees %q", got)
	}
}

// R6: a relative link target.
func TestR6RelativeLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "own"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "own", "auth.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "w"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "w", "auth.json")
	if err := os.Symlink("../own/auth.json", link); err != nil {
		t.Fatal(err)
	}
	s, err := NewAuthStorage(link)
	if err != nil {
		t.Fatal(err)
	}
	holdsLockBeside(t, s, target, link)
	setKey(t, s, "p", "fixture-refresh-A")
	if got, _ := os.ReadFile(target); !strings.Contains(string(got), "fixture-refresh-A") {
		t.Fatalf("target = %s", got)
	}
}

// R6b: relative dangling link resolves by hand too.
func TestR6bRelativeDanglingLink(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "w"), 0o700)
	link := filepath.Join(root, "w", "auth.json")
	if err := os.Symlink("../own/auth.json", link); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(root, "own"), 0o700)
	got, err := resolveAuthPath(link)
	if want := filepath.Join(filepath.Join(root, "own"), "auth.json"); err != nil || got != want {
		t.Fatalf("resolve = %q, %v; want %q", got, err, want)
	}
}

// R7: a chain, link to link to file.
func TestR7LinkChain(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	target := filepath.Join(real, "final.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mid := filepath.Join(real, "mid.json")
	top := filepath.Join(real, "top.json")
	if err := errors.Join(os.Symlink(target, mid), os.Symlink(mid, top)); err != nil {
		t.Fatal(err)
	}
	s, err := NewAuthStorage(top)
	if err != nil {
		t.Fatal(err)
	}
	holdsLockBeside(t, s, target, top)
	setKey(t, s, "p", "fixture-refresh-A")
	for _, l := range []string{top, mid} {
		if li, _ := os.Lstat(l); li.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is no longer a link", l)
		}
	}
	// the same, dangling at the end
	_ = os.Remove(target)
	if got, err := resolveAuthPath(top); err != nil || got != target {
		t.Fatalf("dangling chain resolves to %q, %v; want %q", got, err, target)
	}
}

// R7b: a store reached through a linked FOLDER.
func TestR7bLinkedFolder(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	_ = os.MkdirAll(filepath.Join(real, "agent"), 0o700)
	if err := os.Symlink(filepath.Join(real, "agent"), filepath.Join(real, "agentlink")); err != nil {
		t.Fatal(err)
	}
	for _, withFile := range []bool{true, false} {
		target := filepath.Join(real, "agent", "auth.json")
		_ = os.Remove(target)
		if withFile {
			_ = os.WriteFile(target, []byte(`{}`), 0o600)
		}
		via := filepath.Join(real, "agentlink", "auth.json")
		s, err := NewAuthStorage(via)
		if err != nil {
			t.Fatal(err)
		}
		holdsLockBeside(t, s, target, "/nonexistent-never") // the folder link makes via.lock the same folder
		setKey(t, s, "p", "fixture-refresh-A")
	}
}

// R9: after a replace, another store on another link sees a new revision and the new record.
func TestR9RevisionAfterReplace(t *testing.T) {
	s1, s2, _, link2, _ := linkedStores(t, `{"p":{"type":"api_key","key":"fixture-old"}}`)
	if got := fixtureKey(t, s2, "p"); got != "fixture-old" {
		t.Fatal(got)
	}
	rev1, _ := authFileRevision(link2)
	setKey(t, s1, "p", "fixture-refresh-A")
	rev2, _ := authFileRevision(link2)
	if rev1 == rev2 {
		t.Fatal("revision did not change")
	}
	if got := fixtureKey(t, s2, "p"); got != "fixture-refresh-A" {
		t.Fatalf("second store reads %q", got)
	}
}

// R10: no link: the lock is beside the path as today.
func TestR10NoLink(t *testing.T) {
	s, path := authPortFile(t, `{}`)
	real, _ := filepath.Abs(path) // a path that is no link comes back as filepath.Abs gives it: no folder is resolved
	got, err := resolveAuthPath(path)
	if err != nil || got != real {
		t.Fatalf("resolve = %q, %v", got, err)
	}
	holdsLockBeside(t, s, real, "/nonexistent-never")
}

// R11: a cycle is an error naming ELOOP; nothing is created.
func TestR11LinkCycle(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a.json"), filepath.Join(root, "b.json")
	if err := errors.Join(os.Symlink(b, a), os.Symlink(a, b)); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveAuthPath(a); err == nil || !errors.Is(err, syscall.ELOOP) || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("resolve err = %v", err)
	}
	s, err := NewAuthStorage(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Modify(t.Context(), "p", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "fixture-x"}, nil
	}); err == nil {
		t.Fatal("want an error")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatalf("created files: %v", entries)
	}
}

// R12: about 30 stores through 30 links to one target: one write, then every store reloads at once. All finish without an error, within the bound.
func TestR12ThirtyStoresOneTarget(t *testing.T) {
	const n = 30
	const bound = 15 * time.Second
	root := t.TempDir()
	target := filepath.Join(root, "owner", "auth.json")
	_ = os.MkdirAll(filepath.Dir(target), 0o700)
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stores := make([]*AuthStorage, n)
	for i := range stores {
		dir := filepath.Join(root, "w"+strconv.Itoa(i))
		_ = os.MkdirAll(dir, 0o700)
		link := filepath.Join(dir, "auth.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		s, err := NewAuthStorage(link)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
	}
	start := time.Now()
	setKey(t, stores[0], "p", "fixture-refresh-A")
	errs := make(chan error, n)
	for _, s := range stores {
		go func() {
			c, err := s.Read(t.Context(), "p")
			if err == nil && (c == nil || c.Key != "fixture-refresh-A") {
				err = errors.New("store did not see the record")
			}
			errs <- err
		}()
	}
	for range stores {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	took := time.Since(start)
	t.Logf("R12: %d stores, one write and %d concurrent reloads took %v (bound %v)", n, n, took, bound)
	if took > bound {
		t.Fatalf("took %v, bound %v", took, bound)
	}
}

// R13: a lock folder left by a dead holder. While it is fresh the second store keeps its snapshot; once its mtime is older than the stale time the store takes it over. (internal/pilock has its own takeover rows; this is the auth level.)
func TestR13DeadHolder(t *testing.T) {
	s1, s2, _, _, target := linkedStores(t, `{"p":{"type":"api_key","key":"fixture-old"}}`)
	if got := fixtureKey(t, s2, "p"); got != "fixture-old" {
		t.Fatal(got)
	}
	lockDir := target + ".lock"
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fresh holder: the writer cannot take it and the reader keeps its snapshot.
	if err := s1.withFileLock(t.Context(), false, func(*pilock.Lock) error { return nil }); err == nil {
		t.Fatal("took a fresh lock held by another")
	}
	if c, ok, err := s2.Get("p"); err != nil || !ok || c.Key != "fixture-old" {
		t.Fatalf("snapshot while locked = %v %v %v", c, ok, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatal(err)
	}
	setKey(t, s1, "p", "fixture-refresh-A")
	if got := fixtureKey(t, s2, "p"); got != "fixture-refresh-A" {
		t.Fatal(got)
	}
}

// R14: the same target through a linked parent folder and through the plain path: the two stores exclude each other.
func TestR14TwoSpellings(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	_ = os.MkdirAll(filepath.Join(real, "agent"), 0o700)
	_ = os.Symlink(filepath.Join(real, "agent"), filepath.Join(real, "agentlink"))
	plain, via := filepath.Join(real, "agent", "auth.json"), filepath.Join(real, "agentlink", "auth.json")
	_ = os.WriteFile(plain, []byte(`{}`), 0o600)
	s1, _ := NewAuthStorage(plain)
	s2, _ := NewAuthStorage(via)
	ran := false
	err := s1.withFileLock(t.Context(), false, func(*pilock.Lock) error {
		return s2.withFileLock(t.Context(), false, func(*pilock.Lock) error { ran = true; return nil })
	})
	if ran || err == nil {
		t.Fatalf("the two spellings did not exclude (ran=%v err=%v)", ran, err)
	}
}

// R5b: resolveAuthPath is called at each lock and write: a link re-pointed between two writes is followed.
func TestR5bRepointedLinkIsFollowed(t *testing.T) {
	root := t.TempDir()
	t1, t2 := filepath.Join(root, "one.json"), filepath.Join(root, "two.json")
	for _, f := range []string{t1, t2} {
		_ = os.WriteFile(f, []byte(`{}`), 0o600)
	}
	link := filepath.Join(root, "link.json")
	_ = os.Symlink(t1, link)
	s, err := NewAuthStorage(link)
	if err != nil {
		t.Fatal(err)
	}
	setKey(t, s, "p", "fixture-refresh-A")
	_ = os.Remove(link)
	_ = os.Symlink(t2, link)
	setKey(t, s, "q", "fixture-refresh-B")
	d1, _ := os.ReadFile(t1)
	d2, _ := os.ReadFile(t2)
	if strings.Contains(string(d1), "fixture-refresh-B") || !strings.Contains(string(d2), "fixture-refresh-B") {
		t.Fatalf("one=%s two=%s: the second write did not follow the new link", d1, d2)
	}
}

// R1b: reads behind an unwritable resolved folder. A fresh store must not answer "no credentials" in silence: the error names the resolved folder. A store that already has a snapshot keeps it.
func TestR1bFreshReadBehindUnwritableFolderNamesIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	_, _, link1, link2, target := linkedStores(t, `{"p":{"type":"api_key","key":"fixture-old"}}`)
	// store with a snapshot, made before the folder is closed
	warm, err := NewAuthStorage(link2)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(target)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	// fresh process stand-in: new link, never loaded
	link3 := filepath.Join(filepath.Dir(filepath.Dir(link1)), "workerC", "auth.json")
	_ = os.MkdirAll(filepath.Dir(link3), 0o700)
	if err := os.Symlink(target, link3); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewAuthStorage(link3)
	if err != nil {
		t.Fatal(err)
	}
	cred, ok, err := fresh.Get("p")
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("fresh Get = %v, %v, %v; want an error naming %s", cred, ok, err, dir)
	}
	t.Logf("R1b fresh Get error: %v", err)
	_, rerr := fresh.Read(t.Context(), "p")
	if rerr == nil || !strings.Contains(rerr.Error(), dir) {
		t.Fatalf("fresh Read err = %v", rerr)
	}
	t.Logf("R1b fresh Read error: %v", rerr)
	if c, ok, err := warm.Get("p"); err != nil || !ok || c.Key != "fixture-old" {
		t.Fatalf("warm store = %v %v %v; want its snapshot", c, ok, err)
	}
}
