package codingagent

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Pi session-selector.ts:917-939 awaits refreshSessionsAfterMutation before its finally block leaves the rename panel.
func TestSessionRenameWaitsForRefreshedSessions(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			name := "current/success"
			if all {
				name = "all/success"
			}
			if fail {
				name = strings.ReplaceAll(name, "success", "failure")
			}
			t.Run(name, func(t *testing.T) {
				entries := []SessionInfo{{Path: filepath.Join(t.TempDir(), "session.jsonl"), ID: "session", Name: "Old"}}
				refresh := make(chan sessionLoadResult, 1)
				loads := 0
				initial := 1
				if all {
					initial = 2
				}
				var progress SessionListProgress
				loader := func(_ *sessionLoad, p SessionListProgress) <-chan sessionLoadResult {
					loads++
					if loads <= initial {
						ready := make(chan sessionLoadResult, 1)
						ready <- sessionLoadResult{sessions: entries}
						return ready
					}
					progress = p
					return refresh
				}
				s := newSessionSelectorWithLoaders(loader, loader, func(_ string, name string) error { entries[0].Name = name; return nil }, nil, "", sessionSelectorInputBindings(t))
				t.Cleanup(s.close)
				s.drainLoadUpdates()
				if all {
					s.toggleScope()
					s.drainLoadUpdates()
				}
				s.HandleInput("\x1b[114;5u")
				if !s.renameMode {
					t.Fatal("rename did not open")
				}
				s.renameInput.SetText("Renamed")
				s.HandleInput("\r")
				if loads != initial+1 || s.scopeLoad(s.scope) == nil {
					t.Fatal("refresh was not pending")
				}
				if !s.renameMode || !strings.Contains(strings.Join(s.Render(100), "\n"), "Rename Session") {
					t.Error("rename panel closed while refresh was pending")
				}
				progress(1, 2, entries)
				if !s.renameMode {
					t.Error("progress was mistaken for refresh completion")
				}
				if fail {
					refresh <- sessionLoadResult{err: errors.New("refresh failure")}
				} else {
					refresh <- sessionLoadResult{sessions: entries}
				}
				s.drainLoadUpdates()
				if s.renameMode || s.renamePath != "" || s.loading {
					t.Fatalf("completed refresh retained rename/loading state: rename=%v path=%q loading=%v", s.renameMode, s.renamePath, s.loading)
				}
				if fail {
					if !s.statusState.error || !strings.Contains(s.statusState.message, "refresh failure") {
						t.Fatalf("refresh error disappeared: %q", s.statusState.message)
					}
				} else if len(s.filtered) != 1 || s.filtered[0].Session.Name != "Renamed" {
					t.Fatalf("updated sessions not installed: %+v", s.filtered)
				}
			})
		}
	}
}

func TestSessionRenameUsesJavaScriptTrim(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		calls       int
	}{{"BOM", "\ufeff", 0}, {"NEXT LINE", "\u0085", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			loader := func() ([]SessionInfo, error) {
				return []SessionInfo{{Path: "session.jsonl", ID: "session", Name: "Old"}}, nil
			}
			var names []string
			s := newLoadedSessionSelector(loader, loader, func(_ string, name string) error { names = append(names, name); return nil }, nil, "", sessionSelectorInputBindings(t))
			t.Cleanup(s.close)
			s.enterRenameMode()
			if err := s.confirmRename(tc.input); err != nil {
				t.Fatal(err)
			}
			s.drainLoadUpdates()
			if len(names) != tc.calls {
				t.Fatalf("rename calls=%q want count %d", names, tc.calls)
			}
			if tc.calls == 0 && !s.renameMode {
				t.Error("empty JS-trimmed input closed rename")
			}
			if tc.calls == 1 && (names[0] != tc.input || s.renameMode) {
				t.Fatalf("rename input=%q mode=%v", names[0], s.renameMode)
			}
		})
	}
}

func TestSessionRenameFailureLeavesPanelWithoutRefresh(t *testing.T) {
	loads := 0
	loader := func(_ *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
		loads++
		ready := make(chan sessionLoadResult, 1)
		ready <- sessionLoadResult{sessions: []SessionInfo{{Path: "session.jsonl", ID: "session", Name: "Old"}}}
		return ready
	}
	failure := errors.New("rename failure")
	s := newSessionSelectorWithLoaders(loader, loader, func(string, string) error { return failure }, nil, "", sessionSelectorInputBindings(t))
	t.Cleanup(s.close)
	s.drainLoadUpdates()
	s.enterRenameMode()
	if err := s.confirmRename("New"); !errors.Is(err, failure) {
		t.Fatalf("rename error=%v, want original failure", err)
	}
	if s.renameMode || loads != 1 {
		t.Fatalf("failed rename: mode=%v loads=%d", s.renameMode, loads)
	}
}
