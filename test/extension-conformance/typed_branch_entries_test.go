package extensionconformance

import (
	"context"
	"strings"
	"testing"
)

// TestConformance_TypedBranchEntriesAgreeBetweenGoAndRust: the Go and Rust SDKs offer a typed view of the session branch (Python and Node hand out Pi's raw entries). The host sends each entry in Pi's session-entry
// shape, {"type":"message","message":{...}}, whose assistant content is a block array with thinking, text and ToolCall blocks whose arguments are an object (packages/ai/src/types.ts:423-431).
// The Rust view returned an empty role, text, provider, model and usage for every message entry, and the Go view dropped the text, thinking and tool calls of an assistant message with a tool call. Both
// must now report the same flattened entries through the host-driven session mirror.
func TestConformance_TypedBranchEntriesAgreeBetweenGoAndRust(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const want = `e1~user~hello~~~~~- | e2~assistant~let me look~hm ~p~m~read=c1={"path":"a.txt"}~3/4/5/6/18 | e3~user~thanks~~~~~-`
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if !strings.Contains(tc.name, "go") && !strings.Contains(tc.name, "rust") {
			continue
		}
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
			before := len(*h.notify)
			if !h.runner.ExecuteCommand(context.Background(), "branch-typed-probe", "") {
				t.Fatal("fixture has no branch-typed-probe command")
			}
			var got string
			pollUntilConformance(t, "branch-typed-probe: no report", func() bool {
				for _, n := range (*h.notify)[before:] {
					if rest, ok := strings.CutPrefix(n, "branchtyped:"); ok {
						got = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			if got != want {
				t.Fatalf("typed branch = %s\nwant         %s", got, want)
			}
		})
	}
}
