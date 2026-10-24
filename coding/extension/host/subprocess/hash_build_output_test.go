package subprocess

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// cargoCacheTag is the CACHEDIR.TAG cargo writes at the root of every target directory it creates.
const cargoCacheTag = "Signature: 8a477f597d28d172789f06886806bc55\n# This file is a cache directory tag created by cargo.\n"

// writeFixtureTree lays out a Go extension module whose go.mod replaces the module at ../replaced, the way the conformance fixtures replace the repository root, and returns the extension directory and the replaced directory.
func writeFixtureTree(t *testing.T) (extension, replaced string) {
	t.Helper()
	base := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ext/go.mod", "module example.com/ext\n\ngo 1.26\n\nrequire example.com/replaced v0.0.0\n\nreplace example.com/replaced => ../replaced\n")
	write("ext/main.go", "package main\n")
	write("replaced/go.mod", "module example.com/replaced\n\ngo 1.26\n")
	write("replaced/replaced.go", "package replaced\n")
	return filepath.Join(base, "ext"), filepath.Join(base, "replaced")
}

// A tree marked with CACHEDIR.TAG is build output, not source: no directory below it is opened, so one that cannot be opened (the Unix counterpart of a temp directory deleted mid-listing) cannot fail the hash, and a Go file below it cannot change it. The Rust fixture build writes tmp/test-fixtures/rust-target inside the repository root that the Go fixtures replace.
func TestHashSourceDirSkipsCacheDirectoryTreesOfReplacedModules(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission denial requires a non-root Unix user")
	}
	extension, replaced := writeFixtureTree(t)
	target := filepath.Join(replaced, "tmp", "test-fixtures", "rust-target")
	deps := filepath.Join(target, "release", "deps")
	if err := os.MkdirAll(filepath.Join(deps, "rmetaAbC123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "CACHEDIR.TAG"), []byte(cargoCacheTag), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := hashSourceDir(extension, "go")
	if err != nil {
		t.Fatal(err)
	}
	busy := filepath.Join(deps, "rmetaAbC123")
	if err := os.Chmod(busy, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(busy, 0o755) })
	got, err := hashSourceDir(extension, "go")
	if err != nil {
		t.Fatalf("hash failed on an unreadable directory inside a cargo target directory: %v", err)
	}
	if got != want {
		t.Fatalf("hash changed with an unreadable directory in a cache tree: %s, want %s", got, want)
	}
	if err := os.Chmod(busy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps, "generated.go"), []byte("package deps\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err = hashSourceDir(extension, "go"); err != nil || got != want {
		t.Fatalf("a Go file in a cache tree changed the hash: %s, %v; want %s", got, err, want)
	}
	// Without the signature the tag marks nothing, and a directory that cannot be read still fails the hash.
	if err := os.WriteFile(filepath.Join(target, "CACHEDIR.TAG"), []byte("not a cache tag\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(deps, "generated.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(busy, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := hashSourceDir(extension, "go"); err == nil {
		t.Fatalf("an unreadable directory in an untagged tree produced reusable hash %s", got)
	}
}

// A directory that is listed and then deleted held no source. Whatever build runs beside the hash (cargo's rmeta temp directories under target/release/deps, with or without a CACHEDIR.TAG yet in place) cannot fail it or change it.
func TestHashSourceDirIgnoresConcurrentChurnInReplacedModule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tagged bool
	}{{"cargo target", true}, {"untagged build output", false}} {
		t.Run(tc.name, func(t *testing.T) {
			extension, replaced := writeFixtureTree(t)
			churn := filepath.Join(replaced, "tmp", "test-fixtures", "rust-target")
			if err := os.MkdirAll(churn, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.tagged {
				if err := os.WriteFile(filepath.Join(churn, "CACHEDIR.TAG"), []byte(cargoCacheTag), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := hashSourceDir(extension, "go")
			if err != nil {
				t.Fatal(err)
			}
			stop, done := make(chan struct{}), make(chan error, 1)
			go func() {
				deps := filepath.Join(churn, "release", "deps")
				var busy []string
				for i := 0; ; i++ {
					select {
					case <-stop:
						done <- nil
						return
					default:
					}
					temp := filepath.Join(deps, "rmeta"+strconv.Itoa(i))
					if err := os.MkdirAll(temp, 0o755); err != nil {
						done <- err
						return
					}
					if err := os.WriteFile(filepath.Join(temp, "lib.rmeta"), []byte("x"), 0o644); err != nil {
						done <- err
						return
					}
					if err := removeChurnDir(temp, &busy); err != nil {
						done <- err
						return
					}
				}
			}()
			deadline := time.Now().Add(500 * time.Millisecond)
			for time.Now().Before(deadline) {
				got, err := hashSourceDir(extension, "go")
				if err != nil || got != want {
					close(stop)
					<-done
					t.Fatalf("hash changed or failed during churn: %s, %v; want %s", got, err, want)
				}
			}
			close(stop)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A source file that cannot be read is not a vanished directory: the hash fails closed, so a build never caches under a key that skipped an input.
func TestHashSourceDirFailsClosedOnUnreadableSourceFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission denial requires a non-root Unix user")
	}
	extension, replaced := writeFixtureTree(t)
	source := filepath.Join(replaced, "replaced.go")
	if err := os.Chmod(source, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0o644) })
	if got, err := hashSourceDir(extension, "go"); err == nil {
		t.Fatalf("an unreadable source file produced reusable hash %s", got)
	}
}

// removeChurnDir removes the churn directory temp, and any that an earlier call could not remove. Windows refuses to delete an
// entry the hash walk has open (a sharing violation, or an access denial on one pending deletion): that is the operating system's
// delete semantics, not what the test checks, so such a directory is kept for the next call and the t.TempDir cleanup.
func removeChurnDir(temp string, retry *[]string) error {
	pending := append([]string(nil), *retry...)
	pending = append(pending, temp)
	var busy []string
	for _, path := range pending {
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if !churnRemovalBusy(err) {
				return err
			}
			busy = append(busy, path)
		}
	}
	*retry = busy
	return nil
}
