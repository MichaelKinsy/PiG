package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// recordingRuntimeHost is the runtime host a test hands to NewInteractiveMode. It records the calls the mode routes to
// it; a call it does not implement panics through the nil embedded interface.
type recordingRuntimeHost struct {
	InteractiveRuntime
	imports [][2]string
	quits   int
}

func (h *recordingRuntimeHost) ImportFromJsonl(_ context.Context, path, cwdOverride string) (extension.CancelledResult, error) {
	h.imports = append(h.imports, [2]string{path, cwdOverride})
	return extension.CancelledResult{Cancelled: true}, nil
}

func (h *recordingRuntimeHost) EmitQuitShutdown() { h.quits++ }

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:608. The constructor keeps runtimeHost, and
// the mode routes Session work through it: /import calls runtimeHost.importFromJsonl (:6582, as in
// interactive-mode-import-command.test.ts:54) and quit emits its shutdown through the host.
func TestNewInteractiveModeRoutesSessionWorkThroughItsRuntimeHost(t *testing.T) {
	host := &recordingRuntimeHost{}
	m := NewInteractiveMode(host, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	cancelled, err := m.buildSlashContext(t.Context()).ImportSession("path/to/session.jsonl", "/fallback")
	if err != nil || !cancelled {
		t.Fatalf("ImportSession = %v, %v; want the host's cancelled result", cancelled, err)
	}
	if want := [][2]string{{"path/to/session.jsonl", "/fallback"}}; !slices.Equal(host.imports, want) {
		t.Fatalf("host imports = %q, want %q", host.imports, want)
	}
	m.emitQuitShutdown()
	if host.quits != 1 {
		t.Fatalf("host quit shutdowns = %d, want 1", host.quits)
	}
}

// Without a runtime host, and without a Session that imports, /import reports that import is unavailable.
func TestNewInteractiveModeWithoutRuntimeHostCannotImport(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if _, err := m.buildSlashContext(t.Context()).ImportSession("session.jsonl", ""); err == nil || err.Error() != "Session import is not available in this context." {
		t.Fatalf("ImportSession err = %v", err)
	}
}
