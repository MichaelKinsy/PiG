//go:build !pig_strip_extension_sdk_go

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// fakeGoEnv, when set, makes this test binary act as the go command that runs the analysis driver, so the tests need no shell script and run on every OS. "hang" waits in a long build, "reply" reads the request and answers with a driver error, and "buildfail" prints a toolchain error and fails.
const fakeGoEnv = "PIG_TEST_FAKE_GO"

func init() {
	switch os.Getenv(fakeGoEnv) {
	case "":
		return
	case "hang":
		time.Sleep(time.Minute)
		os.Exit(3)
	case "reply":
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Println(`{"Result":null,"Error":"go list: exit status 1: boom"}`)
		os.Exit(0)
	case "buildfail":
		fmt.Fprintln(os.Stderr, "go: toolchain too old")
		os.Exit(1)
	}
	os.Exit(4)
}

// fakeGo returns the path of the go command that does mode, and the environment that selects it.
func fakeGo(t *testing.T, mode string) (string, []string) {
	t.Helper()
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return command, append(os.Environ(), fakeGoEnv+"="+mode)
}

// A cancelled analysis ends at once with the context's error, though the go command that runs the driver sits in a long build, and it does not wait for the output pipes the driver holds.
func TestAnalysisDriverCancelEndsTheAnalysis(t *testing.T) {
	command, env := fakeGo(t, "hang")
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := runAnalysisDriver(ctx, t.TempDir(), t.TempDir(), command, env, upgrade.LoadOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the cancel took %v", elapsed)
	}
}

// The driver's own error is the error of the analysis, text for text: it is what go list reported.
func TestAnalysisDriverReturnsTheDriversError(t *testing.T) {
	command, env := fakeGo(t, "reply")
	_, err := runAnalysisDriver(t.Context(), t.TempDir(), t.TempDir(), command, env, upgrade.LoadOptions{})
	if err == nil || err.Error() != "go list: exit status 1: boom" {
		t.Fatalf("err = %v", err)
	}
}

// A driver that cannot be built names the go command's output, so the failure reads as the build error it is.
func TestAnalysisDriverBuildFailureNamesTheGoOutput(t *testing.T) {
	command, env := fakeGo(t, "buildfail")
	_, err := runAnalysisDriver(t.Context(), t.TempDir(), t.TempDir(), command, env, upgrade.LoadOptions{})
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
