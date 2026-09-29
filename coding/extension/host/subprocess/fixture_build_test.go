package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Only immutable executables are shared. Each consumer still owns its Host, processes, sockets, and working directory. Build failures are retained, not retried.
var wireFixtureBinary = sync.OnceValues(func() (string, error) {
	return compileGoFixture("fixture-ext")
})

var sdkFixtureBinary = sync.OnceValues(func() (string, error) {
	return compileGoFixture("sdk-fixture", "GOWORK=off")
})

// Isolating Pig's HOME must not discard the compiler and module caches. Ask Go before changing HOME so configured go/env paths retain their meaning.
func keepGoBuildCaches(t *testing.T) {
	t.Helper()
	cmd := exec.CommandContext(testbudget.Context(t), "go", "env", "-json", "GOCACHE", "GOMODCACHE")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]string
	if err := json.Unmarshal(out, &paths); err != nil {
		t.Fatal(err)
	}
	for key, value := range paths {
		t.Setenv(key, value)
	}
}

func compileGoFixture(name string, env ...string) (string, error) {
	if err := os.MkdirAll(fixtureRoot, 0o700); err != nil {
		return "", err
	}
	path := testExtensionBinaryPath(fixtureRoot, name)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", path, ".")
	cmd.Dir = filepath.Join("testdata", name)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Env = append(cmd.Env, env...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %w\n%s", name, err, output)
	}
	return path, nil
}
