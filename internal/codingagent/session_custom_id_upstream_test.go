// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCustomSessionIDUpstream(t *testing.T) {
	create := func(t *testing.T, id *string, parent string) *Session {
		t.Helper()
		s, err := newSessionWithOptions("/project", id, parent)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:10
	t.Run("uses the provided id instead of generating one", func(t *testing.T) {
		s := create(t, new("my-custom-id"), "")
		if s.ID() != "my-custom-id" {
			t.Fatal(s.ID())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:16
	t.Run("uses the provided id when creating an in-memory session", func(t *testing.T) {
		s := create(t, new("memory-session-id"), "")
		if s.ID() != "memory-session-id" || s.Header().ID != s.ID() || s.Path() != "" {
			t.Fatal(s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:23
	t.Run("allows alphanumeric session ids with interior punctuation", func(t *testing.T) {
		if s := create(t, new("abc-123_def.456"), ""); s.ID() != "abc-123_def.456" {
			t.Fatal(s.ID())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:29
	t.Run("rejects invalid custom session ids", func(t *testing.T) {
		for _, id := range []string{"", "-abc", "abc-", "_abc", "abc_", ".abc", "abc.", "abc/def", `abc\def`, "abc def"} {
			t.Run(id, func(t *testing.T) {
				_, err := newSessionWithOptions("/project", &id, "")
				if err == nil || !strings.Contains(err.Error(), "Session id must be non-empty, contain only alphanumeric characters") {
					t.Fatalf("id=%q error=%v", id, err)
				}
				if id != "" {
					dir := filepath.Join(t.TempDir(), "sessions")
					if _, err := NewSessionManagerWithDir("/project", dir).Create(id, ""); err == nil {
						t.Fatal("persisted constructor accepted invalid ID")
					}
					if _, err := os.Stat(dir); !os.IsNotExist(err) {
						t.Fatal("invalid ID created a session directory")
					}
				}
			})
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:40
	t.Run("generates a UUIDv7 id when no id is provided", func(t *testing.T) {
		s := create(t, nil, "")
		if !uuidPattern.MatchString(s.ID()) {
			t.Fatal(s.ID())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:49
	t.Run("generates a UUIDv7 id when options is provided without id", func(t *testing.T) {
		s := create(t, nil, "parent.jsonl")
		if !uuidPattern.MatchString(s.ID()) || s.ParentSession() != "parent.jsonl" {
			t.Fatal(s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:58
	t.Run("includes the custom id in the session header", func(t *testing.T) {
		s := create(t, new("header-test-id"), "")
		if s.Header().ID != "header-test-id" {
			t.Fatal(s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:67
	t.Run("generates a UUIDv7 id when constructed without an explicit id", func(t *testing.T) {
		s := create(t, nil, "")
		if !uuidPattern.MatchString(s.ID()) || s.Header().ID != s.ID() {
			t.Fatal(s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:73
	t.Run("uses the provided id when creating a persisted session", func(t *testing.T) {
		sm := tempSessionMgr(t)
		s, err := sm.Create("created-session-id", "")
		if err != nil {
			t.Fatal(err)
		}
		if s.ID() != "created-session-id" || s.Header().ID != s.ID() || !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z_created-session-id\.jsonl$`).MatchString(filepath.Base(s.Path())) {
			t.Fatal(s.Path())
		}
		if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
			t.Fatal("eager session write")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:85
	t.Run("generates a UUIDv7 id when creating a branched session", func(t *testing.T) {
		s := create(t, nil, "")
		id := upstreamSessionUser(t, s, "hello")
		s = upstreamClone(t, s, id)
		if !uuidPattern.MatchString(s.ID()) || s.Header().ID != s.ID() {
			t.Fatal(s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:99
	t.Run("generates a UUIDv7 id when forking from another session file", func(t *testing.T) {
		sm := tempSessionMgr(t)
		s, err := sm.Create("legacy-session-id", "")
		if err != nil {
			t.Fatal(err)
		}
		upstreamSessionAssistant(t, s, "hello")
		fork, err := sm.ForkFromFile(s.Path())
		if err != nil {
			t.Fatal(err)
		}
		if !uuidPattern.MatchString(fork.Header().ID) || fork.ParentSession() != s.Path() {
			t.Fatal(fork.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/custom-session-id.test.ts:146
	t.Run("uses the provided id when forking from another session file", func(t *testing.T) {
		sm := tempSessionMgr(t)
		source := filepath.Join(sm.SessionDir(), "source.jsonl")
		if err := os.WriteFile(source, []byte(`{"type":"session","version":3,"id":"source-session-id","timestamp":"2026-01-01T00:00:00Z","cwd":"/project"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fork, err := sm.ForkFromFile(source, "forked-session-id")
		if err != nil {
			t.Fatal(err)
		}
		if fork.ID() != "forked-session-id" || fork.Header().ID != fork.ID() || fork.ParentSession() != source || !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z_forked-session-id\.jsonl$`).MatchString(filepath.Base(fork.Path())) {
			t.Fatal(fork.Header(), fork.Path())
		}
		fmt.Println("SESSION_ID custom=forked-session-id parent=true filename=true")
	})
}
