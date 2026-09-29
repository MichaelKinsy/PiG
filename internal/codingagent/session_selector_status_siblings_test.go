package codingagent

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestSessionSelectorMutationStatusesUsePiDeadlines(t *testing.T) {
	for _, tc := range []struct {
		name, want             string
		duration               time.Duration
		failure, active, trash bool
	}{
		{"delete success", "Session deleted", 2 * time.Second, false, false, false},
		// session-selector.ts:857: a trash-method success names the trash.
		{"trash success", "Session moved to trash", 2 * time.Second, false, false, true},
		{"delete failure", "Failed to delete: denied", 3 * time.Second, true, false, false},
		{"active refusal", "Cannot delete the currently active session", 3 * time.Second, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sessions := []SessionInfo{{Path: "status-session.jsonl", ID: "session"}}
				selector := newStatusTestSelector(func() ([]SessionInfo, error) { return sessions, nil })
				defer selector.clearStatusMessage()
				selector.deleteSession = func(string) sessionDeleteResult {
					if tc.failure {
						return sessionDeleteResult{method: sessionDeleteUnlink, error: "denied"}
					}
					sessions = nil
					if tc.trash {
						return sessionDeleteResult{ok: true, method: sessionDeleteTrash}
					}
					return sessionDeleteResult{ok: true, method: sessionDeleteUnlink}
				}
				if tc.active {
					selector.currentPath = canonicalSessionPath(sessions[0].Path)
				}
				selector.startDeleteConfirmation()
				if !tc.active {
					selector.HandleInput("\r")
				}
				if selector.statusState.message != tc.want {
					t.Errorf("status=%q want=%q", selector.statusState.message, tc.want)
				}
				time.Sleep(tc.duration - time.Millisecond)
				selector.Render(120)
				if selector.statusState.message == "" {
					t.Error("status expired early")
				}
				time.Sleep(time.Millisecond)
				selector.Render(120)
				if selector.statusState.message != "" || selector.statusTimeout() != nil {
					t.Errorf("status remains at %v: %+v", tc.duration, selector.statusState)
				}
			})
		})
	}
}
