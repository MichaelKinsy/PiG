package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
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
