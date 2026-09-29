package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 session-selector.ts:375-397 filters only by name/query, not active path; :494-504 styles the active row with accent rather than adding a label.
func TestSessionSelectorIncludesCurrentSession(t *testing.T) {
	for _, name := range []string{"Current", ""} {
		for _, scope := range []sessionScope{sessionScopeCurrent, sessionScopeAll} {
			for _, sortMode := range []sessionSortMode{sessionSortThreaded, sessionSortRecent, sessionSortRelevance} {
				t.Run(string(scope)+"/"+string(sortMode)+"/"+name, func(t *testing.T) {
					current := SessionInfo{Path: filepath.Join(t.TempDir(), "current.jsonl"), Name: name, FirstMessage: "Current preview", AllMessagesText: "Current preview", Modified: time.Now().Add(time.Hour)}
					other := SessionInfo{Path: filepath.Join(t.TempDir(), "other.jsonl"), Name: "Other", Modified: current.Modified.Add(-time.Hour)}
					sessions := []SessionInfo{current, other}
					loader := func() ([]SessionInfo, error) { return sessions, nil }
					sel := newLoadedSessionSelector(loader, loader, nil, nil, current.Path, sessionSelectorInputBindings(t))
					sel.loadScope(scope)
					sel.drainLoadUpdates()
					sel.sortMode = sortMode
					sel.refilter()
					if len(sel.filtered) != len(sessions) || sel.filtered[0].Session.Path != current.Path {
						t.Fatalf("active session missing or reordered: %+v", sel.filtered)
					}
					display := current.Name
					if display == "" {
						display = current.FirstMessage
					}
					for _, selected := range []bool{false, true} {
						row := sel.renderNode(sel.filtered[0], selected, 100)
						if !strings.Contains(row, tui.ActiveTheme().Accent+display+tui.SGRFgReset) {
							t.Fatalf("active row lacks accent: %q", row)
						}
						cursor := "  "
						if selected {
							cursor = "› "
						}
						left, right := cursor+display, "0 now"
						want := left + strings.Repeat(" ", 100-len([]rune(left))-len(right)) + right
						if got := stripANSI(row); got != want {
							t.Fatalf("active row = %q, want %q (no extra current-session marker)", got, want)
						}
					}
					sel.HandleInput("Current")
					if len(sel.filtered) != 1 || sel.filtered[0].Session.Path != current.Path {
						t.Fatalf("search removed active session: %+v", sel.filtered)
					}
					sel.HandleInput("\r")
					if !sel.Done() || sel.Cancelled() || sel.SelectedPath() != current.Path {
						t.Fatalf("Enter: done=%v cancelled=%v path=%q", sel.Done(), sel.Cancelled(), sel.SelectedPath())
					}
				})
			}
		}
	}
}

// Ports the exact active-session case in packages/coding-agent/test/session-selector-path-delete.test.ts:328-357, including two aliases of the same session directory and Ctrl+D.
func TestUpstreamSessionSelectorCurrentSessionAcrossSymlinkAliases(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "real", "sessions")
	aliasA, aliasB := filepath.Join(base, "alias-a", "sessions"), filepath.Join(base, "alias-b", "sessions")
	for _, dir := range []string{shared, filepath.Dir(aliasA), filepath.Dir(aliasB)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	testenv.RequireDirectoryLink(t, shared, aliasA)
	testenv.RequireDirectoryLink(t, shared, aliasB)
	for _, name := range []string{"parent", "child"} {
		if err := os.WriteFile(filepath.Join(shared, name+".jsonl"), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sessions := []SessionInfo{{ID: "parent", Path: filepath.Join(aliasB, "parent.jsonl"), Name: "Parent", Created: time.UnixMilli(0), Modified: time.UnixMilli(0), MessageCount: 1, FirstMessage: "hello", AllMessagesText: "hello"}}
	sel := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return nil, nil }, nil, nil, filepath.Join(aliasA, "parent.jsonl"), sessionSelectorInputBindings(t))
	sel.HandleInput("\x04")
	if sel.confirmDelete != "" || sel.statusState.message != "Cannot delete the currently active session" || !sel.statusState.error {
		t.Fatalf("Ctrl+D: confirmation=%q status=%q error=%v", sel.confirmDelete, sel.statusState.message, sel.statusState.error)
	}
}

// Pi interactive-mode.ts:5563-5566,5592-5608 resumes even the active path; Escape only closes the picker (:5567-5570).
func TestResumeHandlerCurrentSessionSelection(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "select", true: "cancel"}[cancel], func(t *testing.T) {
			current := SessionInfo{Path: filepath.Join(t.TempDir(), "current.jsonl"), Name: "Current"}
			loader := func() ([]SessionInfo, error) { return []SessionInfo{current}, nil }
			sel := newLoadedSessionSelector(loader, loader, nil, nil, current.Path, sessionSelectorInputBindings(t))
			var events []string
			sc := &SlashContext{
				PickSession: func() (string, bool) {
					key := "\r"
					if cancel {
						key = "\x1b"
					}
					sel.HandleInput(key)
					return sel.SelectedPath(), sel.Done() && !sel.Cancelled()
				},
				LoadSessionPath: func(path string) error { events = append(events, path); return nil },
				ShowStatus:      func(status string) { events = append(events, status) },
			}
			if err := resumeHandler(sc); err != nil {
				t.Fatal(err)
			}
			want := []string{current.Path, "Resumed session"}
			if cancel {
				want = nil
			}
			if !slices.Equal(events, want) {
				t.Fatalf("resume effects = %q, want %q", events, want)
			}
		})
	}
}

func BenchmarkSessionSelectorCurrentSession(b *testing.B) {
	for _, size := range []int{10, 1000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			base := sessionSearchCases(b)[10].Sessions
			sessions := make([]SessionInfo, size)
			dir := b.TempDir()
			for i := range sessions {
				sessions[i] = base[i%len(base)]
				sessions[i].Path = filepath.Join(dir, fmt.Sprintf("%d.jsonl", i))
			}
			loader := func() ([]SessionInfo, error) { return sessions, nil }
			sel := newLoadedSessionSelector(loader, loader, nil, nil, sessions[0].Path, sessionSelectorInputBindings(b))
			sel.searchInput.SetText("delta")
			b.ReportAllocs()
			for b.Loop() {
				sel.refilter()
			}
		})
	}
}

// The current path is metadata, not an extra result: Pi lists only loader results, subject to the same name filter as every other session.
func TestSessionSelectorDoesNotInventCurrentSession(t *testing.T) {
	for _, sessions := range [][]SessionInfo{nil, {{Path: "/current.jsonl", FirstMessage: "unnamed"}}} {
		loader := func() ([]SessionInfo, error) { return sessions, nil }
		sel := newLoadedSessionSelector(loader, loader, nil, nil, "/current.jsonl", sessionSelectorInputBindings(t))
		sel.toggleNameFilter()
		if len(sel.filtered) != 0 {
			t.Fatalf("invented current result: %+v", sel.filtered)
		}
	}
}
