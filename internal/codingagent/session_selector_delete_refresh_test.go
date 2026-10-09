package codingagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Pi session-selector.ts:845-859 removes the successful deletion from both caches and publishes the new visible list before awaiting refreshSessionsAfterMutation.
func TestSessionSelectorDeleteRefreshImmediatelyRemovesSession(t *testing.T) {
	var observed []any
	for _, tc := range []struct {
		name                                          string
		scope                                         sessionScope
		only, filtered, moveDuringDelete, child, fail bool
	}{
		{name: "current", scope: sessionScopeCurrent},
		{name: "all", scope: sessionScopeAll},
		{name: "current singleton", scope: sessionScopeCurrent, only: true},
		{name: "all singleton", scope: sessionScopeAll, only: true},
		{name: "filtered singleton", scope: sessionScopeCurrent, filtered: true},
		{name: "touched selection", scope: sessionScopeCurrent, moveDuringDelete: true},
		{name: "deleted parent", scope: sessionScopeCurrent, child: true},
		{name: "failed deletion", scope: sessionScopeCurrent, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			victim := scopeSession("deleted")
			victim.Name = "delete-target"
			currentKeep, allKeep := scopeSession("current-keep"), scopeSession("all-keep")
			currentKeep.Name, allKeep.Name = "current-survivor", "all-survivor"
			if tc.child {
				currentKeep.ParentSessionPath = victim.Path
			}
			current, all := []SessionInfo{victim}, []SessionInfo{victim}
			if !tc.only {
				current = append(current, currentKeep)
				all = append(all, allKeep)
			}
			if tc.moveDuringDelete {
				other := scopeSession("other")
				other.Name = "other-survivor"
				current = append(current, other)
			}
			pending := make(chan sessionLoadResult, 1)
			mutated, refreshes := false, 0
			loader := func(rows []SessionInfo) sessionsLoader {
				return func(_ *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
					if mutated {
						refreshes++
						return pending
					}
					result := make(chan sessionLoadResult, 1)
					result <- sessionLoadResult{sessions: rows}
					return result
				}
			}
			var s *SessionSelectorComponent
			s = newSessionSelectorWithLoaders(loader(current), loader(all), nil, unlinkDeleter(func(path string) error {
				if path != victim.Path {
					t.Fatalf("deleted path=%q", path)
				}
				if tc.fail {
					return errors.New("delete rejected")
				}
				if tc.moveDuringDelete {
					s.HandleInput("\x1b[B")
				}
				mutated = true
				return nil
			}), "", sessionSelectorInputBindings(t))
			t.Cleanup(s.close)
			s.drainLoadUpdates()
			s.HandleInput("\t")
			s.drainLoadUpdates()
			if tc.scope == sessionScopeCurrent {
				s.HandleInput("\t")
			}
			if tc.filtered {
				s.HandleInput("delete")
			}
			s.HandleInput("\x04")
			s.HandleInput("\r")
			if tc.fail {
				if refreshes != 0 || !s.statusState.error || len(s.filtered) != len(current) || s.filtered[0].Session.Path != victim.Path {
					t.Fatalf("failed deletion changed list: refreshes=%d rows=%+v status=%q", refreshes, s.filtered, s.statusState.message)
				}
				return
			}
			if refreshes != 1 || !s.loading || s.confirmDelete != "" {
				t.Fatalf("refresh state: calls=%d loading=%v confirmation=%q", refreshes, s.loading, s.confirmDelete)
			}
			rows := make([]string, 0, len(s.filtered))
			for _, node := range s.filtered {
				rows = append(rows, node.Session.ID)
			}
			want := []string{}
			if !tc.only && !tc.filtered {
				if tc.scope == sessionScopeAll {
					want = []string{allKeep.ID}
				} else {
					want = []string{currentKeep.ID}
				}
				if tc.moveDuringDelete {
					want = append(want, "other")
				}
			}
			if !reflect.DeepEqual(rows, want) {
				t.Errorf("pending refresh rows=%q, want %q", rows, want)
			}
			if strings.Contains(strings.Join(s.Render(120), "\n"), victim.Name) {
				t.Error("deleted Session remains rendered during refresh")
			}
			if current[0].ID != victim.ID || all[0].ID != victim.ID {
				t.Error("deletion mutated a loader-owned snapshot")
			}
			if tc.child && len(s.filtered) > 0 && s.filtered[0].Depth != 0 {
				t.Error("surviving child still has deleted parent indentation")
			}
			if tc.filtered && s.searchInput.Text() != "delete" {
				t.Error("deletion changed the search query")
			}
			selected := ""
			s.HandleInput("\r")
			if len(want) == 0 {
				if s.Done() || s.SelectedPath() != "" {
					t.Errorf("Enter selected deleted singleton: %q", s.SelectedPath())
				}
			} else {
				keep := currentKeep
				if tc.scope == sessionScopeAll {
					keep = allKeep
				}
				if !s.Done() || s.SelectedPath() != keep.Path {
					t.Errorf("Enter selected %q, want surviving %q", s.SelectedPath(), keep.Path)
				}
				selected = strings.TrimSuffix(filepath.Base(s.SelectedPath()), ".jsonl")
			}
			observed = append(observed, []any{tc.name, rows, selected})
		})
	}
	if !t.Failed() {
		data, err := json.Marshal(observed)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("SESSION_DELETE_REFRESH %s\n", data)
	}
}
