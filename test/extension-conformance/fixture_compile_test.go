package extensionconformance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
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

var rustFixtureBinary = sync.OnceValues(func() (string, error) {
	if _, err := exec.LookPath("cargo"); err != nil {
		return "", fmt.Errorf("cargo is required for SDK conformance: %w", err)
	}
	src := filepath.Join(fixtureSourceRoot, "test", "extension-conformance", "testdata", "rust-sdk-fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build rust-sdk-fixture: %w\n%s", err, out)
	}
	target := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if target == "" {
		target = filepath.Join(src, "target")
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(src, target)
	}
	bin := filepath.Join(target, "release", testExecutableName("rust-sdk-fixture"))
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("rust-sdk-fixture binary missing: %w", err)
	}
	return bin, nil
})
