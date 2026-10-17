package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/session-selector.ts

// pi: packages/coding-agent/src/modes/interactive/components/session-selector-search.ts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func pathDeleteSession(id string) SessionInfo {
	return SessionInfo{Path: "/tmp/" + id + ".jsonl", ID: id, Created: time.UnixMilli(0), Modified: time.UnixMilli(0), MessageCount: 1, FirstMessage: "hello", AllMessagesText: "hello"}
}

func pathDeleteSelector(t *testing.T, sessions []SessionInfo, current string) *SessionSelectorComponent {
	t.Helper()
	return newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return []SessionInfo{}, nil }, nil, nil, current, sessionSelectorInputBindings(t))
}

func pathDeleteAliases(t *testing.T) (parentA, parentB, childB string) {
	t.Helper()
	base := t.TempDir()
	for _, dir := range []string{"real/sessions", "alias-a", "alias-b"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Upstream's symlinkSync(sharedDir, alias) at session-selector-path-delete.test.ts:70-71 makes a directory symlink, which Windows refuses without privilege. The junction made there instead is the same symbolic link to Pi's realpath-based canonicalizePath.
	for _, alias := range []string{"alias-a", "alias-b"} {
		testenv.RequireDirectoryLink(t, filepath.Join(base, "real/sessions"), filepath.Join(base, alias, "sessions"))
	}
	for _, id := range []string{"parent", "child"} {
		if err := os.WriteFile(filepath.Join(base, "real/sessions", id+".jsonl"), []byte(id+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(base, "alias-a/sessions/parent.jsonl"), filepath.Join(base, "alias-b/sessions/parent.jsonl"), filepath.Join(base, "alias-b/sessions/child.jsonl")
}

func TestSessionSelectorDeleteHeaderFollowsConfirmation(t *testing.T) {
	s := pathDeleteSelector(t, []SessionInfo{pathDeleteSession("a")}, "")
	s.HandleInput("\x04")
	if got := stripANSI(sessionSelectorHeader(s, 120)[1]); got != "Delete session? enter confirm · escape/ctrl+c cancel" {
		t.Fatalf("confirmation header=%q", got)
	}
	s.HandleInput("\x1b")
	if strings.Contains(strings.Join(s.Render(120), "\n"), "Delete session?") {
		t.Fatal("cancel did not clear the header")
	}
}

func TestUpstreamSessionSelectorPathDelete(t *testing.T) {
	observed := []any{}
	for _, tc := range []struct {
		name, query, key string
		confirm          bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/session-selector-path-delete.test.ts:108
		{"does not treat Ctrl+Backspace as delete when search query is non-empty", "a", "\x1b[127;5u", false},
		// .upstream/v0.87.1/packages/coding-agent/test/session-selector-path-delete.test.ts:132
		{"enters confirmation mode on Ctrl+D even with a non-empty search query", "a", "\x04", true},
		// .upstream/v0.87.1/packages/coding-agent/test/session-selector-path-delete.test.ts:156
		{"enters confirmation mode on Ctrl+Backspace when search query is empty", "", "\x1b[127;5u", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := []SessionInfo{pathDeleteSession("a"), pathDeleteSession("b")}
			s := pathDeleteSelector(t, sessions, "")
			changes := []any{}
			// Go's header reads confirmation state directly rather than subscribing to SessionList's callback. Observe each input boundary and the deletion callback.
			recordConfirmation := func() {
				if s.confirmDelete == "" {
					changes = append(changes, nil)
				} else {
					changes = append(changes, s.confirmDelete)
				}
			}
			if tc.query != "" {
				s.HandleInput(tc.query)
			}
			s.HandleInput(tc.key)
			if s.confirmDelete != "" {
				recordConfirmation()
			}
			want := []any{}
			if tc.confirm {
				want = append(want, sessions[0].Path)
			}
			if !reflect.DeepEqual(changes, want) {
				t.Fatalf("confirmation changes=%v want=%v", changes, want)
			}
			if tc.query == "" {
				deleted := ""
				s.deleteSession = unlinkDeleter(func(path string) error {
					deleted = path
					if s.confirmDelete != "" {
						t.Error("confirmation must clear before deletion starts")
					}
					return nil
				})
				s.HandleInput("\r")
				recordConfirmation()
				want = append(want, nil)
				if !reflect.DeepEqual(changes, want) || deleted != sessions[0].Path {
					t.Fatalf("confirmation=%v deleted=%q want=%v %q", changes, deleted, want, sessions[0].Path)
				}
			}
			observed = append(observed, changes)
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/session-selector-path-delete.test.ts:253
	t.Run("threads sessions when parent and child paths use different symlink aliases", func(t *testing.T) {
		parentA, parentB, childB := pathDeleteAliases(t)
		parent, child := pathDeleteSession("parent"), pathDeleteSession("child")
		parent.Path, parent.Name, parent.Modified = parentB, "Parent", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		child.Path, child.Name, child.ParentSessionPath, child.Modified = childB, "Child", parentA, time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
		s := pathDeleteSelector(t, []SessionInfo{parent, child}, "")
		// ANSI stripping is the exact upstream text assertion, not a normalized frame comparator.
		output := stripANSI(strings.Join(s.Render(120), "\n"))
		if !strings.Contains(output, "Parent") || !strings.Contains(output, "└─ Child") {
			t.Fatalf("missing threaded parent/child: %s", output)
		}
		observed = append(observed, []string{s.filtered[0].Session.ID, s.filtered[1].Session.ID})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-selector-path-delete.test.ts:328
	t.Run("treats the current session as active across symlink aliases", func(t *testing.T) {
		parentA, parentB, _ := pathDeleteAliases(t)
		parent := pathDeleteSession("parent")
		parent.Path, parent.Name = parentB, "Parent"
		s := pathDeleteSelector(t, []SessionInfo{parent}, parentA)
		changes := []any{}

		s.HandleInput("\x04")
		if s.confirmDelete != "" {
			changes = append(changes, s.confirmDelete)
		}
		if len(changes) != 0 || s.statusState.message != "Cannot delete the currently active session" {
			t.Fatalf("confirmations=%v error=%q", changes, s.statusState.message)
		}
		observed = append(observed, []any{changes, s.statusState.message})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-selector-path-delete.test.ts:289
	t.Run("sorts threaded sessions by latest activity in their subtree", func(t *testing.T) {
		p1, p2, c2 := pathDeleteSession("parent-one"), pathDeleteSession("parent-two"), pathDeleteSession("child-two")
		p1.Name, p1.Modified = "Parent one", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		p2.Name, p2.Modified = "Parent two", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		c2.Name, c2.ParentSessionPath, c2.Modified = "Child two", p2.Path, time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
		s := pathDeleteSelector(t, []SessionInfo{p1, p2, c2}, "")
		output := stripANSI(strings.Join(s.Render(120), "\n"))
		parentTwo, childTwo, parentOne := strings.Index(output, "Parent two"), strings.Index(output, "└─ Child two"), strings.Index(output, "Parent one")
		if parentTwo < 0 || childTwo <= parentTwo || parentOne <= childTwo {
			t.Fatalf("thread order=%d,%d,%d\n%s", parentTwo, childTwo, parentOne, output)
		}
		ids := []string{}
		for _, node := range s.filtered {
			ids = append(ids, node.Session.ID)
		}
		observed = append(observed, ids)
	})
	raw, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("SESSION_PATH_DELETE_BASIC " + string(raw))
}

// session-selector-path-delete.test.ts:108-180 through getSessionList(): onDeleteConfirmationChange receives the path when the confirmation
// starts and null (here "") when it ends; onSelect and onCancel run on the confirm and cancel keys; getSelectedSessionPath follows the cursor.
func TestSessionListCallbacksAndSelection(t *testing.T) {
	for _, tc := range []struct {
		name, query, key string
		wantStart        bool
	}{
		{"does not treat Ctrl+Backspace as delete when search query is non-empty", "a", "\x1b[127;5u", false},
		{"enters confirmation mode on Ctrl+D even with a non-empty search query", "a", "\x04", true},
		{"enters confirmation mode on Ctrl+Backspace when search query is empty", "", "\x1b[127;5u", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := []SessionInfo{pathDeleteSession("a"), pathDeleteSession("b")}
			s := pathDeleteSelector(t, sessions, "")
			list := s.GetSessionList()
			var changes []string
			list.OnDeleteConfirmationChange = func(path string) { changes = append(changes, path) }
			if tc.query != "" {
				list.HandleInput(tc.query)
			}
			list.HandleInput(tc.key)
			var want []string
			if tc.wantStart {
				want = []string{sessions[0].Path}
			}
			if !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes = %q, want %q", changes, want)
			}
			if tc.wantStart {
				s.deleteSession = unlinkDeleter(func(string) error { return nil })
				list.HandleInput("\r")
				if want = append(want, ""); !reflect.DeepEqual(changes, want) {
					t.Fatalf("after confirm changes = %q, want %q", changes, want)
				}
			}
		})
	}
	t.Run("onSelect, onCancel and the selected path", func(t *testing.T) {
		sessions := []SessionInfo{pathDeleteSession("a"), pathDeleteSession("b")}
		s := pathDeleteSelector(t, sessions, "")
		list := s.GetSessionList()
		if got := list.GetSelectedSessionPath(); got != sessions[0].Path {
			t.Fatalf("selected path = %q, want %q", got, sessions[0].Path)
		}
		list.HandleInput("\x1b[B")
		if got := list.GetSelectedSessionPath(); got != sessions[1].Path {
			t.Fatalf("selected path after down = %q, want %q", got, sessions[1].Path)
		}
		var selected []string
		cancelled := 0
		list.OnSelect = func(path string) { selected = append(selected, path) }
		list.OnCancel = func() { cancelled++ }
		list.HandleInput("\r")
		if !reflect.DeepEqual(selected, []string{sessions[1].Path}) || cancelled != 0 {
			t.Fatalf("after confirm selected=%q cancelled=%d", selected, cancelled)
		}
		s2 := pathDeleteSelector(t, sessions, "")
		l2 := s2.GetSessionList()
		l2.OnCancel = func() { cancelled++ }
		l2.HandleInput("\x1b")
		if cancelled != 1 || s2.GetSessionList() != l2 {
			t.Fatalf("cancelled=%d", cancelled)
		}
	})
}
