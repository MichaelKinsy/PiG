package codingagent

// pi: packages/coding-agent/src/modes/interactive/session-share.ts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Pi session-share.ts:57 shareSession removes its temp directory in `finally`, whatever the share did. Here each share owns a temp directory that is gone when
// shareSession returns after a success, a gateway failure and a cancellation alike.
func TestShareSessionRemovesItsTempDirectoryOnEveryOutcome(t *testing.T) {
	// Build the session before redirecting the temp directory: on Windows a session snapshot is a delete-on-close file in os.TempDir() that stays listed while the Session lives, and would otherwise land in scratch.
	session := shareTestSession(t, "cleanup")
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	status := http.StatusCreated
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(status)
		if status == http.StatusCreated {
			_, _ = w.Write([]byte(`{"artifact":{"canonical_url":"` + serverOrigin(request) + `/session/p_test"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer server.Close()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		status  int
		wantErr bool
	}{
		{"success", t.Context(), http.StatusCreated, false},
		{"gateway failure", t.Context(), http.StatusBadGateway, true},
		{"cancelled", cancelled, http.StatusCreated, true},
	} {
		status = tc.status
		_, err := shareSession(tc.ctx, session, ShareState{}, server.URL, server.Client(), func(string) {})
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: err = %v, want error %v", tc.name, err, tc.wantErr)
		}
		if entries, readErr := os.ReadDir(scratch); readErr != nil || len(entries) != 0 {
			t.Fatalf("%s: temp directory holds %d entries (%v) after shareSession returned", tc.name, len(entries), readErr)
		}
	}
}
