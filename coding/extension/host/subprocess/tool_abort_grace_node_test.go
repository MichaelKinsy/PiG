//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// pig divergence (D111): an aborted extension tool call waits ToolAbortGrace for the tool to settle. A tool that honours its
// AbortSignal settles as in Pi, whose executePreparedToolCall awaits tool.execute and makes its result or thrown message the
// tool result (agent-loop.ts:831-853). A tool that ignores the signal, which Pi awaits indefinitely, fails with Pi's abort text
// "Operation aborted" (agent-loop.ts:623) once the grace period ends.
func TestAbortedToolCallSettlesCooperativelyOrFailsAfterTheGrace(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "abort-grace.mjs")
			if err := os.WriteFile(source, []byte(toolAbortGraceExtension), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, failures := h.LoadAll(t.Context(), []ExtConfig{{Name: "abort-grace", Source: source, Enabled: true, Isolation: isolation}})
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v %v", loaded, failures)
			}
			runner := inproc.NewRunner(loaded, t.TempDir())

			if _, _, err := abortToolAfterItStarts(t, runner, "coop_reject"); err == nil || err.Error() != "coop stopped by signal" {
				t.Fatalf("coop_reject error = %v, want the tool's own rejection %q", err, "coop stopped by signal")
			}
			result, _, err := abortToolAfterItStarts(t, runner, "coop_resolve")
			if err != nil || result.Text() != "coop partial result" {
				t.Fatalf("coop_resolve = %q, %v; want the tool's own result %q", result.Text(), err, "coop partial result")
			}
			_, elapsed, err := abortToolAfterItStarts(t, runner, "hang_tool")
			if err == nil || err.Error() != "Operation aborted" {
				t.Fatalf("hang_tool error = %v, want exactly %q", err, "Operation aborted")
			}
			if elapsed < ToolAbortGrace {
				t.Fatalf("hang_tool failed %v after its abort, before the %v grace period", elapsed, ToolAbortGrace)
			}
		})
	}
}
