package cli

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// requireExtensionTool fails the test loud when a required extension
// toolchain is missing. Node is required extension-hosting coverage for this
// slice, not optional: a machine running this suite is expected to have it,
// and a silent skip would let exec-all-modes coverage quietly disappear.
func requireExtensionTool(t *testing.T, name string) string {
	t.Helper()
	path, err := osexec.LookPath(name)
	if err != nil {
		t.Fatalf("required extension tool %s is unavailable: %v", name, err)
	}
	return path
}

// buildExecSDKFixture compiles the Go-SDK exec fixture, mirroring
// coding/extension/host/subprocess's buildSDKFixture helper, so the
// exec-all-modes regression covers the Go SDK path (extensions/sdk's
// sdk.Context.Exec), not only the Node runtime.
func buildExecSDKFixture(t *testing.T) string {
	t.Helper()
	return buildGoSDKFixture(t, "exec-sdk-fixture")
}

// buildGoSDKFixture compiles testdata/<name>, a Go SDK extension. The build is CGO-free, so it drops -race from an inherited GOFLAGS, which the run that builds a race-enabled pig sets.
func buildGoSDKFixture(t *testing.T, name string) string {
	t.Helper()
	binPath := testExecutable(filepath.Join(t.TempDir(), name))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, "go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Join("testdata", name)
	flags := slices.DeleteFunc(strings.Fields(os.Getenv("GOFLAGS")), func(flag string) bool { return flag == "-race" })
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS="+strings.Join(flags, " "))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return binPath
}
