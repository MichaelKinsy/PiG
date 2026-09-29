package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// testExecutable returns path as a name the platform can start: Windows runs
// only files with an executable extension.
func testExecutable(path string) string {
	if runtime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

var pigTestBinary = sync.OnceValues(func() (string, error) { return compilePigBinary(false) })
var releaseTestBinary = sync.OnceValues(func() (string, error) { return compilePigBinary(true) })

// Both variants use the Go build cache but retain distinct compile flags. Consumers own processes and homes, not the immutable executable.
func compilePigBinary(release bool) (string, error) {
	name := "pig"
	args := []string{"build"}
	if release {
		name = "pig-release"
		args = append(args, "-trimpath", "-buildvcs=false")
	}
	binDir := filepath.Join(fixtureRoot, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return "", err
	}
	out := testExecutable(filepath.Join(binDir, name))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", append(args, "-o", out, ".")...)
	cmd.Dir = filepath.Join(fixtureSourceRoot, "cmd", "pig")
	if release {
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	}
	if data, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %w\n%s", name, err, data)
	}
	return out, nil
}

func buildPigBinaryForSignalTest(t *testing.T) string {
	t.Helper()
	out, err := pigTestBinary()
	if err != nil {
		t.Fatal(err)
	}
	return out
}
