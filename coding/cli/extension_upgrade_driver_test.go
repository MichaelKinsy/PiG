//go:build !windows && !pig_strip_extension_sdk_go

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// fakeGo writes a go command that runs script, and returns its path.
func fakeGo(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// A cancelled analysis ends at once with the context's error, though the go command that runs the driver sits in a long build, and it does not wait for the output pipes the driver holds.
func TestAnalysisDriverCancelEndsTheAnalysis(t *testing.T) {
	command := fakeGo(t, "exec sleep 60")
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := runAnalysisDriver(ctx, t.TempDir(), t.TempDir(), command, os.Environ(), upgrade.LoadOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the cancel took %v", elapsed)
	}
}

// The driver's own error is the error of the analysis, text for text: it is what go list reported.
func TestAnalysisDriverReturnsTheDriversError(t *testing.T) {
	command := fakeGo(t, `read -r request; echo '{"Result":null,"Error":"go list: exit status 1: boom"}'`)
	_, err := runAnalysisDriver(t.Context(), t.TempDir(), t.TempDir(), command, os.Environ(), upgrade.LoadOptions{})
	if err == nil || err.Error() != "go list: exit status 1: boom" {
		t.Fatalf("err = %v", err)
	}
}

// A driver that cannot be built names the go command's output, so the failure reads as the build error it is.
func TestAnalysisDriverBuildFailureNamesTheGoOutput(t *testing.T) {
	command := fakeGo(t, `echo "go: toolchain too old" >&2; exit 1`)
	_, err := runAnalysisDriver(t.Context(), t.TempDir(), t.TempDir(), command, os.Environ(), upgrade.LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "go run ./upgrade/driver") || !strings.Contains(err.Error(), "go: toolchain too old") {
		t.Fatalf("err = %v", err)
	}
}

// A go command that cannot start is an error of the analysis, not a panic or an empty plan.
func TestAnalysisDriverMissingGoCommandFails(t *testing.T) {
	_, err := runAnalysisDriver(t.Context(), t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "no-go"), os.Environ(), upgrade.LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "go run ./upgrade/driver") {
		t.Fatalf("err = %v", err)
	}
}
