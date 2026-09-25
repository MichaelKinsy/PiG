package inproc

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream runner.ts reports every handler failure with extensionPath:
// ext.path, the path the extension was loaded from, not its resolved form.
func TestHandlerErrorsReportTheExtensionPath(t *testing.T) {
	ext := extension.Extension{
		Path:         "exts/failing.ts",
		ResolvedPath: "/work/exts/failing.ts",
		Handlers: map[string][]extension.HandlerFn{
			"agent_start": {func(...any) (any, error) { return nil, errors.New("boom") }},
		},
	}
	runner := NewRunner([]extension.Extension{ext}, t.TempDir())
	var got []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { got = append(got, err.ExtensionPath) })
	_, _ = runner.Emit(context.Background(), extension.AgentStartEvent{Type: "agent_start"})
	if len(got) != 1 || got[0] != "exts/failing.ts" {
		t.Fatalf("reported extension paths = %q, want the extension's path", got)
	}
}

// EmitProjectTrust reports handler failures with ext.path as well
// (runner.ts emitProjectTrust).
func TestProjectTrustErrorsReportTheExtensionPath(t *testing.T) {
	ext := extension.Extension{
		Path:         "exts/trust.ts",
		ResolvedPath: "/work/exts/trust.ts",
		Handlers: map[string][]extension.HandlerFn{
			"project_trust": {func(...any) (any, error) { return nil, errors.New("boom") }},
		},
	}
	runner := NewRunner([]extension.Extension{ext}, t.TempDir())
	_, reported, err := EmitProjectTrust(runner, context.Background(), extension.ProjectTrustEvent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reported) != 1 || reported[0].ExtensionPath != "exts/trust.ts" {
		t.Fatalf("project_trust errors = %+v, want the extension's path", reported)
	}
}
