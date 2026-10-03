package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Thirteen extensions that fail to build for one reason print one line naming
// the cause and the log, not thirteen compiler dumps.
func TestExtensionLoadDiagnosticsGroupBuildFailures(t *testing.T) {
	const cause = "Go toolchain mismatch: the compiler is go1.26.7 but the go command is go1.26.1 (/x/go); reinstall that Go release"
	var errs []error
	for i := range 13 {
		name := fmt.Sprintf("ext%02d", i)
		errs = append(errs, &subprocess.ExtensionLoadError{Name: name, Path: "/pkg/" + name, Err: fmt.Errorf("build packed cell k: %w", &runtimecell.BuildFailure{Summary: cause, Cause: cause, Log: "/cache/logs/build-1.log"})})
	}
	diagnostics := extensionLoadDiagnostics(errs)
	if len(diagnostics) != 1 {
		t.Fatalf("%d diagnostics, want 1: %v", len(diagnostics), diagnostics)
	}
	message := diagnostics[0].Message
	if strings.Contains(message, "\n") || !strings.HasPrefix(message, "13 extensions failed to build: Go toolchain mismatch") || !strings.Contains(message, "/cache/logs/build-1.log") {
		t.Fatalf("message = %q", message)
	}
}

func TestExtensionLoadDiagnosticForOneBuildFailureIsOneLine(t *testing.T) {
	failure := &runtimecell.BuildFailure{Summary: "go build: extension.go:6:43: too many arguments", Cause: "go build: too many arguments", Log: "/l.log"}
	diagnostics := extensionLoadDiagnostics([]error{&subprocess.ExtensionLoadError{Name: "a", Path: "/pkg/a", Err: fmt.Errorf("build packed cell go-1: %w", failure)}})
	// Pi's main.ts wraps the loader error, which loader.ts words as
	// `Failed to load extension: ${message}`; the build summary is the message.
	want := `Failed to load extension "/pkg/a": Failed to load extension: go build: extension.go:6:43: too many arguments (details: /l.log)`
	if len(diagnostics) != 1 || diagnostics[0].Message != want {
		t.Fatalf("diagnostics = %v, want %q", diagnostics, want)
	}
}

// Pi's main.ts prints the -ne hint after its startup errors when one of them is an extension load failure
// (main.ts:912-914), and every extension that fails to load is one: `Failed to load extension "<path>": ...`. PiG folds
// extensions that fail to build for one cause into one line (D20) that does not carry that text, so a start whose only
// failures were such a group exited 1 without telling the user how to start without extensions.
func TestReportExtensionLoadFailuresHintsAfterGroupedBuildFailure(t *testing.T) {
	failure := &runtimecell.BuildFailure{Summary: "go build: assignment mismatch", Cause: "go build: assignment mismatch", Log: "/l.log"}
	var errs []error
	for _, name := range []string{"context-info", "context-info:2"} {
		errs = append(errs, &subprocess.ExtensionLoadError{Name: name, Path: "/pkg/" + name, Err: fmt.Errorf("build packed cell k: %w", failure)})
	}
	diagnostics := extensionLoadDiagnostics(errs)
	if len(diagnostics) != 1 || strings.Contains(diagnostics[0].Message, "Failed to load extension") {
		t.Fatalf("diagnostics = %v, want one grouped line", diagnostics)
	}
	stderr := captureStderr(t, func() { reportExtensionLoadFailures(diagnostics) })
	want := "Error: " + diagnostics[0].Message + "\n" + extensionLoadFailureHint + "\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

// A failed virtual-model registration is an error but not a load failure: Pi prints no hint for it alone.
func TestReportExtensionLoadFailuresOmitsHintWithoutLoadFailure(t *testing.T) {
	diagnostics := []codingagent.AgentSessionRuntimeDiagnostic{{Type: "error", Message: `Extension "/pkg/a" error: 3 extensions failed to build`}}
	stderr := captureStderr(t, func() { reportExtensionLoadFailures(diagnostics) })
	if strings.Contains(stderr, extensionLoadFailureHint) {
		t.Fatalf("stderr = %q, want no -ne hint", stderr)
	}
}
