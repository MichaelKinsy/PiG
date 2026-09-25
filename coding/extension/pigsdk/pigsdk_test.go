package pigsdk

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/mod/modfile"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

func TestSDKStagingWaitsForBuildLock(t *testing.T) {
	operations := map[string]func(string) error{
		"ensure all": EnsureSynced,
		"ensure one": func(root string) error { return EnsureSyncedLang(root, "go") },
		"force sync": func(root string) error {
			_, err := Sync(root)
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			release, err := pigsdklock.AcquireBuild(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- operation(root) }()
			select {
			case err := <-done:
				_ = release()
				t.Fatalf("SDK staging completed while build lock was held: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("SDK staging did not resume after build lock release")
			}
		})
	}
}

func TestSyncStagesAllLanguages(t *testing.T) {
	root := t.TempDir()
	counts, err := Sync(root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Go: flat module files.
	if counts["go"] != len(sdk.BundledFiles()) {
		t.Fatalf("go staged %d files, want %d", counts["go"], len(sdk.BundledFiles()))
	}
	for _, name := range sdk.BundledFiles() {
		if _, err := os.Stat(filepath.Join(root, "state", "pigsdk", "sdk", name)); err != nil {
			t.Fatalf("missing staged go/%s: %v", name, err)
		}
	}
	// Python: subdir package must be recreated on disk.
	if _, err := os.Stat(filepath.Join(root, "state", "pigsdk", "sdk-py", "pig_sdk", "__init__.py")); err != nil {
		t.Fatalf("missing staged python pig_sdk/__init__.py: %v", err)
	}
	// Rust: crate root + src subdir.
	for _, rel := range []string{"Cargo.toml", filepath.Join("src", "lib.rs")} {
		if _, err := os.Stat(filepath.Join(root, "state", "pigsdk", "sdk-rs", rel)); err != nil {
			t.Fatalf("missing staged rust %s: %v", rel, err)
		}
	}
	for _, lang := range []string{"go", "python", "rust"} {
		dir, err := SDKDirFor(root, lang)
		if err != nil {
			t.Fatalf("SDKDirFor(%s): %v", lang, err)
		}
		if _, err := os.Stat(filepath.Join(dir, markerFile)); err != nil {
			t.Fatalf("missing %s marker: %v", lang, err)
		}
	}
}

func TestEnsureSyncedIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSynced(root); err != nil {
		t.Fatalf("first EnsureSynced: %v", err)
	}
	marker := filepath.Join(SDKDir(root), markerFile)
	info, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat marker: %v", err)
	}
	first := info.ModTime()

	if err := EnsureSynced(root); err != nil {
		t.Fatalf("second EnsureSynced: %v", err)
	}
	info, err = os.Stat(marker)
	if err != nil {
		t.Fatalf("re-stat marker: %v", err)
	}
	if !info.ModTime().Equal(first) {
		t.Fatal("warm EnsureSynced re-wrote the marker; should have skipped")
	}
}

func TestEnsureSyncedWarmPathDoesNotAcquireStageLock(t *testing.T) {
	operations := map[string]func(string) error{
		"all languages": EnsureSynced,
		"one language":  func(root string) error { return EnsureSyncedLang(root, "go") },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := EnsureSynced(root); err != nil {
				t.Fatalf("initial EnsureSynced: %v", err)
			}
			lockPath := filepath.Join(root, "state", "pigsdk", ".transaction.lock")
			if err := os.Remove(lockPath); err != nil {
				t.Fatalf("remove prior lock file: %v", err)
			}

			if err := operation(root); err != nil {
				t.Fatalf("warm ensure: %v", err)
			}
			if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
				t.Fatalf("warm ensure acquired the stage lock: %v", err)
			}
		})
	}
}

func TestEnsureSyncedStalePathAcquiresStageLock(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSynced(root); err != nil {
		t.Fatalf("initial EnsureSynced: %v", err)
	}
	lockPath := filepath.Join(root, "state", "pigsdk", ".transaction.lock")
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove prior lock file: %v", err)
	}
	marker := filepath.Join(SDKDir(root), markerFile)
	if err := os.WriteFile(marker, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale marker: %v", err)
	}

	if err := EnsureSynced(root); err != nil {
		t.Fatalf("repair stale SDK: %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("stale ensure did not acquire the stage lock: %v", err)
	}
}

func BenchmarkEnsureSyncedWarm(b *testing.B) {
	root := b.TempDir()
	if err := EnsureSynced(root); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if err := EnsureSynced(root); err != nil {
			b.Fatal(err)
		}
	}
}

func TestEnsureSyncedReStagesOnDrift(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSyncedLang(root, "python"); err != nil {
		t.Fatalf("EnsureSyncedLang python: %v", err)
	}
	dir, err := SDKDirFor(root, "python")
	if err != nil {
		t.Fatal(err)
	}
	obsolete := filepath.Join(dir, "obsolete-sdk-source.go")
	if err := os.WriteFile(obsolete, []byte("package obsolete\n"), 0o644); err != nil {
		t.Fatalf("write obsolete SDK source: %v", err)
	}
	marker := filepath.Join(dir, markerFile)
	if err := os.WriteFile(marker, []byte("stale-hash\n"), 0o644); err != nil {
		t.Fatalf("corrupt marker: %v", err)
	}
	if err := EnsureSyncedLang(root, "python"); err != nil {
		t.Fatalf("re-stage EnsureSyncedLang python: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(data) == "stale-hash\n" {
		t.Fatal("EnsureSyncedLang did not re-stage after marker drift")
	}
	if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
		t.Fatalf("obsolete SDK source survived re-stage: %v", err)
	}
}

func TestEnsureSyncedLangUnknown(t *testing.T) {
	if err := EnsureSyncedLang(t.TempDir(), "cobol"); err == nil {
		t.Fatal("EnsureSyncedLang accepted an unknown language")
	}
}

// TestStagedSDKBuildsOffline is the property that matters for Go: an extension
// whose go.mod replaces the SDK with the staged directory must `go build` with
// no network and no pig source checkout. This is what `pig extension init`
// produces on a clean binary-only host.
func TestStagedSDKBuildsOffline(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	configRoot := t.TempDir()
	if err := EnsureSyncedLang(configRoot, "go"); err != nil {
		t.Fatalf("EnsureSyncedLang go: %v", err)
	}
	staged := SDKDir(configRoot)

	extDir := t.TempDir()
	goMod := "module example.com/interview-ext\n\ngo 1.26\n\n" +
		"require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\n" +
		"replace github.com/MichaelKinsy/PiG/extensions/sdk => " + modfile.AutoQuote(filepath.ToSlash(staged)) + "\n"
	if err := os.WriteFile(filepath.Join(extDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	main := `package main

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New("interview-ext")
	e.Command("interview", "start", func(ctx sdk.Context, args string) error { return nil })
	return e
}

func main() { _ = Extension().Run() }
`
	if err := os.WriteFile(filepath.Join(extDir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "ext"), ".")
	cmd.Dir = extDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline build against staged SDK failed: %v\n%s", err, out)
	}
}
