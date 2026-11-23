package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/internal/execfile"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// sharedRustFixtures builds each standalone testdata/<name> Rust SDK fixture once per test process under
// testbudget.Once (which bounds the compile by the go test deadline, not a per-case wait), into the package's
// shared CARGO_TARGET_DIR under a per-fixture file lock, and copies the executable into fixtureRoot/bin so
// another checkout sharing the target directory cannot overwrite a running test's binary (the same form as
// test/extension-conformance/fixture_compile_test.go).
var (
	sharedRustFixturesMu sync.Mutex
	sharedRustFixtures   = map[string]*testbudget.Once[string]{}
)

func sharedRustFixtureOnce(name string) *testbudget.Once[string] {
	sharedRustFixturesMu.Lock()
	defer sharedRustFixturesMu.Unlock()
	once := sharedRustFixtures[name]
	if once == nil {
		once = &testbudget.Once[string]{}
		sharedRustFixtures[name] = once
	}
	return once
}

// buildRustSDKFixture builds testdata/<name>, a Rust SDK extension that is a program of its own, once per test
// process outside any case's wait budget and returns the test process's copy of the executable.
func buildRustSDKFixture(t *testing.T, name string) string {
	t.Helper()
	bin, err := sharedRustFixtureOnce(name).Do(t, func(ctx context.Context) (string, error) {
		return compileRustSDKFixture(ctx, name)
	})
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

func compileRustSDKFixture(ctx context.Context, name string) (string, error) {
	if _, err := exec.LookPath("cargo"); err != nil {
		return "", fmt.Errorf("cargo is required for %s: %w", name, err)
	}
	src := filepath.Join(fixtureSourceRoot, "coding", "cli", "testdata", name)
	target := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if target == "" {
		target = filepath.Join(src, "target")
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(src, target)
	}
	binDir := filepath.Join(fixtureRoot, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return "", err
	}
	dst := testExecutable(filepath.Join(binDir, name))
	return buildRustFixtureCopy(ctx, target, name, dst, func(ctx context.Context) error {
		cargo := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet")
		cargo.Dir = src
		if out, err := cargo.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %w\n%s", name, err, out)
		}
		return nil
	})
}

func buildRustFixtureCopy(ctx context.Context, target, program, dst string, build func(context.Context) error) (string, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create the Cargo target: %w", err)
	}
	lock := flock.New(filepath.Join(target, "."+program+".lock"))
	locked, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		return "", fmt.Errorf("wait for the fixture build lock of %s: %w", target, err)
	}
	defer func() { _ = lock.Unlock() }()
	if err := build(ctx); err != nil {
		return "", err
	}
	built := filepath.Join(target, "release", testExecutable(program))
	if err := copyFixtureExecutable(built, dst); err != nil {
		return "", fmt.Errorf("copy the built %s: %w", program, err)
	}
	return dst, nil
}

func copyFixtureExecutable(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return execfile.Write(to, os.O_CREATE|os.O_TRUNC, 0o755, in)
}

func TestBuildRustFixtureCopyIsolatesTheBinaryFromLaterBuilds(t *testing.T) {
	target := t.TempDir()
	dst := filepath.Join(t.TempDir(), testExecutable("probe-rs"))
	built := filepath.Join(target, "release", testExecutable("probe-rs"))
	got, err := buildRustFixtureCopy(t.Context(), target, "probe-rs", dst, func(context.Context) error {
		if err := os.MkdirAll(filepath.Dir(built), 0o755); err != nil {
			return err
		}
		return os.WriteFile(built, []byte("first-build"), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != dst {
		t.Fatalf("dst = %q, want %q", got, dst)
	}
	if err := os.WriteFile(built, []byte("second-checkout-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first-build" {
		t.Fatalf("copied binary = %q, want first-build", data)
	}
}
