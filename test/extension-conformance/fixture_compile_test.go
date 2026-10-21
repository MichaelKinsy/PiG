package extensionconformance

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// Share only build artifacts. Every harness owns fresh processes, connections, and state; build failures are retained for the package run.
var sdkFixtureBinary = sync.OnceValues(func() (string, error) {
	bin := filepath.Join(fixtureRoot, testExecutableName("sdk-fixture"))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join(fixtureSourceRoot, "test", "extension-conformance", "testfixture", "cmd")
	// Root-module fixtures require the workspace's local SDK, not the published release. Workspace mode rejects -mod=mod.
	flags := strings.Join(slices.DeleteFunc(strings.Fields(os.Getenv("GOFLAGS")), func(f string) bool { return f == "-mod=mod" }), " ")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK="+filepath.Join(fixtureSourceRoot, "go.work"), "GOFLAGS="+flags)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build sdk-fixture: %w\n%s", err, out)
	}
	return bin, nil
})

// rustFixtureBinary builds the Rust SDK fixture and returns a copy of the program that belongs to this test run.
//
// Cargo leaves the program at <target>/release/rust-sdk-fixture, and the shared Cargo target (testmain_test.go: testenv.SharedCargoTargetDir) is named for the
// SDK's lockfiles, not for the checkout. Another checkout of the repository on the same machine (a worktree, another developer's or agent's clone, a CI job that
// shares the cache) builds its own fixture into that same path, which has other tools than this checkout's: a test that started the path after the other build
// ran the other checkout's extension and failed with "Tool order_probe not found" in every round that followed, while the iterations before it passed. So the
// build and the copy happen with the target's fixture lock held, and the tests start the copy.
var rustFixtureBinary = sync.OnceValues(func() (string, error) {
	if _, err := exec.LookPath("cargo"); err != nil {
		return "", fmt.Errorf("cargo is required for SDK conformance: %w", err)
	}
	src := filepath.Join(fixtureSourceRoot, "test", "extension-conformance", "testdata", "rust-sdk-fixture")
	target := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if target == "" {
		target = filepath.Join(src, "target")
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(src, target)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	return buildFixtureCopy(ctx, target, filepath.Join(fixtureRoot, testExecutableName("rust-sdk-fixture")), func(ctx context.Context) error {
		cmd := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet")
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build rust-sdk-fixture: %w\n%s", err, out)
		}
		return nil
	})
})

// buildFixtureCopy runs build with the Cargo target's fixture lock held, which every checkout's test run takes, and copies the program it left at
// <target>/release/rust-sdk-fixture to dst. A build of another checkout that follows replaces the shared program and leaves the copy as it was.
func buildFixtureCopy(ctx context.Context, target, dst string, build func(context.Context) error) (string, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create the Cargo target: %w", err)
	}
	lock := flock.New(filepath.Join(target, ".rust-sdk-fixture.lock"))
	locked, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		return "", fmt.Errorf("wait for the fixture build lock of %s: %w", target, err)
	}
	defer func() { _ = lock.Unlock() }()
	if err := build(ctx); err != nil {
		return "", err
	}
	built := filepath.Join(target, "release", testExecutableName("rust-sdk-fixture"))
	if err := copyExecutable(built, dst); err != nil {
		return "", fmt.Errorf("copy the built rust-sdk-fixture: %w", err)
	}
	return dst, nil
}

func copyExecutable(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
