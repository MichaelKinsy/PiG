package codingagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pi: packages/coding-agent/src/core/session-cwd.ts

// Ports packages/coding-agent/test/session-cwd.test.ts (the persisted-session cases) and the message formats of session-cwd.ts:36-55.
func writePiSessionFile(t *testing.T, path, cwd string) {
	t.Helper()
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "session-id", "timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(header, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCwdDetectsAMissingCwdFromPersistedSessions(t *testing.T) {
	fallback := t.TempDir()
	missing := filepath.Join(fallback, "does-not-exist")
	file := filepath.Join(t.TempDir(), "session.jsonl")
	writePiSessionFile(t, file, missing)

	session, err := NewSessionManager(fallback).Open(file)
	if err != nil {
		t.Fatal(err)
	}
	issue := GetMissingSessionCwdIssue(session, fallback)
	if issue == nil || *issue != (SessionCwdIssue{SessionFile: session.Path(), SessionCwd: missing, FallbackCwd: fallback}) {
		t.Fatalf("issue = %+v", issue)
	}
	var missingErr *MissingSessionCwdError
	if err := AssertSessionCwdExists(session, fallback); !errors.As(err, &missingErr) || missingErr.Issue != *issue {
		t.Fatalf("AssertSessionCwdExists = %v, want a *MissingSessionCwdError carrying the issue", err)
	}
}

func TestSessionCwdOverrideWhenOpeningASession(t *testing.T) {
	fallback := t.TempDir()
	file := filepath.Join(t.TempDir(), "session.jsonl")
	writePiSessionFile(t, file, filepath.Join(fallback, "does-not-exist"))

	session, err := NewSessionManager(fallback).Open(file, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if session.CWD() != fallback {
		t.Fatalf("cwd = %q, want the override %q", session.CWD(), fallback)
	}
	if issue := GetMissingSessionCwdIssue(session, fallback); issue != nil {
		t.Fatalf("issue = %+v, want none for an existing cwd", issue)
	}
	if err := AssertSessionCwdExists(session, fallback); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCwdWithoutASessionFileHasNoIssue(t *testing.T) {
	if issue := GetMissingSessionCwdIssue(nil, t.TempDir()); issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
}

func TestMissingSessionCwdMessagesMatchPi(t *testing.T) {
	issue := SessionCwdIssue{SessionFile: "/s/session.jsonl", SessionCwd: "/gone", FallbackCwd: "/here"}
	if got, want := FormatMissingSessionCwdError(issue), "Stored session working directory does not exist: /gone\nSession file: /s/session.jsonl\nCurrent working directory: /here"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	issue.SessionFile = ""
	if got, want := FormatMissingSessionCwdError(issue), "Stored session working directory does not exist: /gone\nCurrent working directory: /here"; got != want {
		t.Errorf("error without a session file = %q, want %q", got, want)
	}
	if got, want := FormatMissingSessionCwdPrompt(issue), "cwd from session file does not exist\n/gone\n\ncontinue in current cwd\n/here"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if got := (&MissingSessionCwdError{Issue: issue}).Error(); got != FormatMissingSessionCwdError(issue) {
		t.Errorf("Error() = %q, want the formatted error", got)
	}
}
