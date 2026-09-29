package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUpstreamSessionSelectorRename(t *testing.T) {
	for _, tc := range []struct {
		name string
		show bool
		site int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/session-selector-rename.test.ts:41
		{"shows rename hint in interactive /resume picker configuration", true, 41},
		// .upstream/v0.87.1/packages/coding-agent/test/session-selector-rename.test.ts:60
		{"does not show rename hint in --resume picker configuration", false, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := []SessionInfo{{Path: "/tmp/a.jsonl", ID: "a", Created: time.UnixMilli(0), Modified: time.UnixMilli(0), MessageCount: 1, FirstMessage: "hello", AllMessagesText: "hello"}}
			sel := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return nil, nil }, nil, nil, "", sessionSelectorInputBindings(t))
			sel.showRenameHint = tc.show
			output := strings.Join(sel.Render(120), "\n")
			for _, part := range []string{"ctrl+r", "rename"} {
				if strings.Contains(output, part) != tc.show {
					t.Fatalf("show=%v output=%q", tc.show, output)
				}
			}
			renameCaseRecord(t, tc.site, tc.name)
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/session-selector-rename.test.ts:79
	t.Run("enters rename mode on Ctrl+R and submits with Enter", func(t *testing.T) {
		sessions := []SessionInfo{{Path: "/tmp/a.jsonl", ID: "a", Name: "Old", Created: time.UnixMilli(0), Modified: time.UnixMilli(0), MessageCount: 1, FirstMessage: "hello", AllMessagesText: "hello"}}
		var calls [][2]string
		sel := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return nil, nil }, func(path, name string) error { calls = append(calls, [2]string{path, name}); return nil }, nil, "", sessionSelectorInputBindings(t))
		sel.HandleInput("\x1b[114;5u")
		output := strings.Join(sel.Render(120), "\n")
		if !strings.Contains(output, "Rename Session") || strings.Contains(output, "Resume Session") {
			t.Fatalf("rename output=%q", output)
		}
		sel.HandleInput("X")
		sel.HandleInput("\r")
		if len(calls) != 1 || calls[0] != ([2]string{sessions[0].Path, "XOld"}) {
			t.Fatalf("rename calls=%q", calls)
		}
		renameCaseRecord(t, 79, "enters rename mode on Ctrl+R and submits with Enter")
	})
}

func renameCaseRecord(t testing.TB, site int, name string) {
	t.Helper()
	record, err := json.Marshal([]any{site, name})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SESSION_RENAME %s\n", record)
}
