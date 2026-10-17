// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func sessionCwdFixture(t *testing.T) (string, string, string) {
	t.Helper()
	fallback := t.TempDir()
	missing := filepath.Join(fallback, "does-not-exist")
	file := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(file, []byte(fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":\"session-id\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"cwd\":%q}\n", missing)), 0o600); err != nil {
		t.Fatal(err)
	}
	return fallback, missing, file
}

func TestSessionCwdHandlingUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/session-cwd.test.ts:37
	t.Run("detects missing session cwd from persisted sessions", func(t *testing.T) {
		fallback, missing, file := sessionCwdFixture(t)
		session, err := icodingagent.NewSessionManagerWithDir(fallback, filepath.Dir(file)).Open(file)
		if err != nil {
			t.Fatal(err)
		}
		got := icodingagent.GetMissingSessionCwdIssue(session, fallback)
		want := &icodingagent.SessionCwdIssue{SessionFile: file, SessionCwd: missing, FallbackCwd: fallback}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("issue=%+v want=%+v", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-cwd.test.ts:54
	t.Run("supports overriding the effective cwd when opening a session", func(t *testing.T) {
		fallback, missing, file := sessionCwdFixture(t)
		session, err := icodingagent.NewSessionManagerWithDir(fallback, filepath.Dir(file)).Open(file, fallback)
		if err != nil {
			t.Fatal(err)
		}
		if session.CWD() != fallback || icodingagent.GetMissingSessionCwdIssue(session, fallback) != nil {
			t.Fatal("override not used")
		}
		if session.GetHeader().CWD != missing {
			t.Fatal("override rewrote stored cwd")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-cwd.test.ts:67
	t.Run("throws controlled error before runtime creation when stored cwd is missing", func(t *testing.T) {
		fallback, missing, file := sessionCwdFixture(t)
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: fallback, AgentDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		runtime := &Runtime{services: services}
		called := false
		_, err = runtime.startSessionWithFactory(SessionStartOptions{ResumePath: file}, func(*AgentSessionServices, SessionOptions) (*Session, error) {
			called = true
			return nil, errors.New("should not be called")
		})
		var missingErr *icodingagent.MissingSessionCwdError
		if !errors.As(err, &missingErr) || called {
			t.Fatalf("error=%v factoryCalled=%v", err, called)
		}
		if missingErr.Issue.SessionCwd != missing {
			t.Fatal(missingErr.Issue)
		}
		fmt.Printf("SESSION_CWD blocked=%t factory=%t\n", missingErr != nil, called)
	})
}
