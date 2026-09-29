package codingagent

import (
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

// Pi session-selector.ts:930-938 refreshes after rename and restores the list without replacing the existing header status or hints.
func TestPairReviewRenameRestoresHeader(t *testing.T) {
	sessions := []SessionInfo{{Path: "rename-header.jsonl", ID: "rename-header", Name: "Old"}}
	loader := func() ([]SessionInfo, error) { return sessions, nil }
	selector := newLoadedSessionSelector(loader, loader, func(_ string, name string) error { sessions[0].Name = name; return nil }, nil, "", nil)
	defer selector.clearStatusMessage()
	before := sessionSelectorHeader(selector, 120)
	selector.enterRenameMode()
	if err := selector.confirmRename("New"); err != nil {
		t.Fatal(err)
	}
	selector.drainLoadUpdates() // Await the resolved refresh fixture before inspecting the restored header.
	if selector.renameMode {
		t.Fatal("rename mode did not end")
	}
	if got := sessionSelectorHeader(selector, 120); !reflect.DeepEqual(got, before) {
		t.Fatalf("rename replaced header hints: got %q want %q", got, before)
	}
}

// Pi confirmRename (session-selector.ts:917-939) never writes the header status. A status set before the rename, here the active-session refusal with its 3000 ms timeout (session-selector.ts:836-838), survives a resolved or rejected rename. After a resolved rename it still expires at its original deadline (session-selector.ts:116-126), not at a restarted one.
func TestSessionSelectorRenameKeepsPriorTimedStatus(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "resolved"
		if reject {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				denied := errors.New("rename denied")
				active := SessionInfo{Path: "rename-active.jsonl", ID: "rename-active", Name: "Old"}
				loader := func() ([]SessionInfo, error) { return []SessionInfo{active}, nil }
				rename := func(string, string) error {
					if reject {
						return denied
					}
					return nil
				}
				bindings := sessionSelectorInputBindings(t)
				selector := newLoadedSessionSelector(loader, loader, rename, nil, active.Path, bindings)
				defer selector.close()
				hints := newLoadedSessionSelector(loader, loader, rename, nil, active.Path, bindings)
				defer hints.close()
				clean := sessionSelectorHeader(hints, 120)

				selector.HandleInput("\x04")
				refused := sessionSelectorHeader(selector, 120)
				if got := stripANSI(refused[1]); got != "Cannot delete the currently active session" {
					t.Fatalf("status before rename=%q", got)
				}
				time.Sleep(time.Second)
				selector.HandleInput("\x1b[114;5u")
				if !selector.renameMode {
					t.Fatal("rename did not open")
				}
				selector.renameInput.SetText("New")
				selector.HandleInput("\r")
				if reject {
					if !errors.Is(selector.operationError, denied) {
						t.Fatalf("rename error=%v, want original rejection", selector.operationError)
					}
				} else {
					if selector.operationError != nil {
						t.Fatal(selector.operationError)
					}
					selector.drainLoadUpdates() // Await the resolved post-rename refresh.
				}
				if selector.renameMode {
					t.Fatal("rename mode did not end")
				}
				if got := sessionSelectorHeader(selector, 120); !reflect.DeepEqual(got, refused) {
					t.Fatalf("rename changed the prior status:\n got %q\nwant %q", got, refused)
				}
				if reject {
					return // Pi's rejected rename reaches the process owner; TestSessionSelectorRenameFailureReachesOwners covers that exit.
				}
				time.Sleep(2*time.Second - time.Millisecond)
				if got := sessionSelectorHeader(selector, 120); !reflect.DeepEqual(got, refused) {
					t.Fatalf("prior status expired before its 3000 ms deadline: %q", got)
				}
				time.Sleep(time.Millisecond)
				if got := sessionSelectorHeader(selector, 120); !reflect.DeepEqual(got, clean) {
					t.Fatalf("prior status did not expire at its original 3000 ms deadline:\n got %q\nwant %q", got, clean)
				}
			})
		})
	}
}
