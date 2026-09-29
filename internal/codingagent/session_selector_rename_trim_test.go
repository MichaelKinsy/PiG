package codingagent

import (
	"testing"
)

func TestPairReviewRenameUsesECMAScriptTrim(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		calls       int
		renameMode  bool
	}{{"BOM", "\ufeff", 0, true}, {"NEL", "\u0085", 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := []SessionInfo{{Path: "rename-trim.jsonl", ID: "rename-trim", Name: "Old"}}
			loader := func() ([]SessionInfo, error) { return sessions, nil }
			var calls []string
			selector := newLoadedSessionSelector(loader, loader, func(_ string, value string) error { calls = append(calls, value); return nil }, nil, "", nil)
			defer selector.close()
			selector.enterRenameMode()
			if err := selector.confirmRename(tc.value); err != nil {
				t.Fatal(err)
			}
			// Pi awaits the refresh promise before checking the resulting mode.
			selector.drainLoadUpdates()
			if len(calls) != tc.calls || selector.renameMode != tc.renameMode {
				t.Fatalf("calls=%q renameMode=%v, want calls=%d renameMode=%v", calls, selector.renameMode, tc.calls, tc.renameMode)
			}
			if len(calls) > 0 && calls[0] != tc.value {
				t.Fatalf("renamed value=%q, want %q", calls[0], tc.value)
			}
		})
	}
}
