package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// testExecutable returns path as a name the platform can start: Windows runs
// only files with an executable extension.
func testExecutable(path string) string {
	if runtime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

// sharedTestBinary builds one pig variant once per test process (testbudget.Once): the first test to ask runs the build under its go test deadline; every later test reads the result.
type sharedTestBinary struct {
	release bool
	built   testbudget.Once[string]
}

func (b *sharedTestBinary) get(t *testing.T) (string, error) {
	t.Helper()
	return b.built.Do(t, func(ctx context.Context) (string, error) { return compilePigBinary(ctx, b.release) })
}

var pigTestBinary = &sharedTestBinary{}
var releaseTestBinary = &sharedTestBinary{release: true}

// Both variants use the Go build cache but retain distinct compile flags. Consumers own processes and homes, not the immutable executable. The build ends when it finishes or when ctx ends at the go test deadline: its length is what the host needs to link pig, so no fixed bound fits it, and a starved host fails with the deadline named instead of with a killed go command.
func compilePigBinary(ctx context.Context, release bool) (string, error) {
	name := "pig"
	// -pgo=off: the allocation and startup gates measure the committed source. The default -pgo=auto picks up a cmd/pig/default.pgo that is not tracked (a developer's profile), and a PGO build inlines and escapes differently: one more turn measured 306 KB with a profile and 316-320 KB without.
	args := []string{"build", "-pgo=off"}
	if release {
		name = "pig-release"
		args = append(args, "-trimpath", "-buildvcs=false")
	}
	binDir := filepath.Join(fixtureRoot, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return "", err
	}
	out := testExecutable(filepath.Join(binDir, name))
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
	out, err := pigTestBinary.get(t)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
