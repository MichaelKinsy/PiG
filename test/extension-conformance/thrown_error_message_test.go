package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestConformance_ThrownToolErrorMessagesAgreeAcrossSDKs: Pi reports a tool that throws as `error instanceof Error ? error.message : String(error)`
// (agent-loop.ts:773, 850, 899), so an Error whose message is empty reports an empty message. The Node runtime reported `error?.message || String(error)`,
// which turns that message into "Error"; the Go, Rust and Python SDKs return what the handler gave. Each SDK's handler fails with a non-ASCII message
// with a newline, with an empty message and with a 100000-character message, and then serves a normal call. The in-process Go reference runs the same cases.
func TestConformance_ThrownToolErrorMessagesAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	want := map[string]string{"unicode": "é😀 \n x", "empty": "", "long": strings.Repeat("x", 100000)}
	for _, tc := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			tool, ok := findTool(h.runner, "throw_probe")
			if !ok {
				t.Fatal("throw_probe not registered")
			}
			call := func(kind string) (string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				result, err := tool.Definition.Execute(ctx, "tc-throw", json.RawMessage(fmt.Sprintf(`{"kind":%q}`, kind)), nil)
				if err != nil {
					return err.Error(), err
				}
				return fmt.Sprint(result.Content), nil
			}
			for _, kind := range []string{"unicode", "empty", "long"} {
				got, err := call(kind)
				if err == nil {
					t.Fatalf("%s: the call succeeded with %q", kind, got)
				}
				if got != want[kind] {
					t.Errorf("%s: error message %.60q (%d bytes), want %.60q (%d bytes)", kind, got, len(got), want[kind], len(want[kind]))
				}
			}
			if got, err := call("ok"); err != nil || !strings.Contains(got, "ok") {
				t.Fatalf("the call after the errors = %q, %v; the extension stopped serving", got, err)
			}
		})
	}
}
