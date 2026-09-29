// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

func restoredEntry(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(raw)) {
		t.Fatal(raw)
	}
	return json.RawMessage(raw)
}
func migrationFixture(version int) []json.RawMessage {
	header := fmt.Sprintf(`{"type":"session","id":"sess-1","version":%d,"timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`, version)
	if version == 1 {
		header = `{"type":"session","id":"sess-1","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`
	}
	return []json.RawMessage{[]byte(header), []byte(`{"type":"message","timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}`), []byte(`{"type":"message","timestamp":"2025-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"api":"test","provider":"test","model":"test","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0},"stopReason":"stop","timestamp":2}}`)}
}

func TestSessionMigrationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/migration.test.ts:5
	t.Run("should add id parentId to v1 entries", func(t *testing.T) {
		raw := migrationFixture(1)
		s, err := newSessionFromEntries("/tmp", "", raw)
		if err != nil {
			t.Fatal(err)
		}
		entries := s.Entries()
		if s.Header().Version != 3 || len(entries) != 2 {
			t.Fatalf("header=%+v entries=%v", s.Header(), entries)
		}
		if len(entries[0].Base.ID) != 8 || len(entries[1].Base.ID) != 8 || entries[0].Base.ParentID != nil || entries[1].Base.ParentID == nil || *entries[1].Base.ParentID != entries[0].Base.ID {
			t.Fatalf("migration chain=%+v", entries)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/migration.test.ts:43
	t.Run("should be idempotent skip already migrated", func(t *testing.T) {
		raw := migrationFixture(2)
		for i, id := range []string{"abc12345", "def67890"} {
			var err error
			raw[i+1], err = replaceJSONField(raw[i+1], "id", id)
			if err != nil {
				t.Fatal(err)
			}
			var parent any
			if i == 1 {
				parent = "abc12345"
			}
			raw[i+1], err = replaceJSONField(raw[i+1], "parentId", parent)
			if err != nil {
				t.Fatal(err)
			}
		}
		s, err := newSessionFromEntries("/tmp", "", raw)
		if err != nil {
			t.Fatal(err)
		}
		entries := s.Entries()
		if entries[0].Base.ID != "abc12345" || entries[1].Base.ID != "def67890" || entries[1].Base.ParentID == nil || *entries[1].Base.ParentID != "abc12345" {
			t.Fatalf("entries=%+v", entries)
		}
	})
}

func TestSessionPreloadedEntriesUpstream(t *testing.T) {
	makeEntries := func(build func(*Session)) []json.RawMessage {
		s := NewSession("source", "/project")
		build(s)
		var out []json.RawMessage
		for _, e := range s.Entries() {
			out = append(out, e.Raw())
		}
		return out
	}
	message := func(s *Session, text string) string {
		id, err := s.AppendMessage(mkUserMsg(text))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	load := func(t *testing.T, id string, entries []json.RawMessage) *Session {
		t.Helper()
		s, err := newSessionFromEntries("/project", id, entries)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:18
	t.Run("adopts entries verbatim", func(t *testing.T) {
		raw := makeEntries(func(s *Session) {
			message(s, "hello")
			if err := s.AppendModelSwitch("anthropic", "claude-opus-4-5", ""); err != nil {
				t.Fatal(err)
			}
			message(s, "again")
		})
		s := load(t, "", raw)
		for i, e := range s.Entries() {
			if string(e.Raw()) != string(raw[i]) {
				t.Fatal("entry changed")
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:30
	t.Run("keeps the loaded leaf so appends continue the conversation", func(t *testing.T) {
		raw := makeEntries(func(s *Session) { message(s, "hello"); message(s, "again") })
		s := load(t, "", raw)
		previous := *s.LeafID()
		id := message(s, "continued")
		e, _ := s.EntryByID(id)
		if *s.LeafID() != id || e.Base.ParentID == nil || *e.Base.ParentID != previous {
			t.Fatal("lost loaded leaf")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:44
	t.Run("never mints an id that collides with a loaded entry", func(t *testing.T) {
		raw := makeEntries(func(s *Session) {
			for i := range 50 {
				message(s, fmt.Sprintf("message %d", i))
			}
		})
		s := load(t, "", raw)
		old := s.Entries()
		id := message(s, "continued")
		for _, e := range old {
			if e.Base.ID == id {
				t.Fatal("ID collision")
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:55
	t.Run("rebuilds the branch structure rather than a flat chain", func(t *testing.T) {
		raw := makeEntries(func(s *Session) {
			id := message(s, "hello")
			message(s, "abandoned")
			if err := s.Fork(id); err != nil {
				t.Fatal(err)
			}
			message(s, "kept")
		})
		roots := load(t, "", raw).Tree().Children
		if len(roots) != 1 || len(roots[0].Children) != 2 {
			t.Fatalf("tree=%+v", roots)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:70
	t.Run("rebuilds labels", func(t *testing.T) {
		id := ""
		raw := makeEntries(func(s *Session) {
			id = message(s, "hello")
			label := "checkpoint"
			if err := s.AppendLabelChange(id, &label); err != nil {
				t.Fatal(err)
			}
		})
		roots := load(t, "", raw).Tree().Children
		if len(roots) != 1 || roots[0].Entry.Base.ID != id || roots[0].Label != "checkpoint" {
			t.Fatalf("labels=%+v", roots)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:82
	t.Run("resolves a compaction against the entry it was written against", func(t *testing.T) {
		id := ""
		raw := makeEntries(func(s *Session) {
			message(s, "dropped")
			id = message(s, "kept")
			if _, err := s.AppendCompaction("summary so far", id, 1000, nil, false, nil); err != nil {
				t.Fatal(err)
			}
		})
		s := load(t, "", raw)
		found := false
		for _, e := range s.BuildSessionProjection().Entries {
			if e.SourceEntry.Base.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatal("kept entry missing")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:96
	t.Run("creates a header from the options when the entries carry none", func(t *testing.T) {
		s := load(t, "restored-session", makeEntries(func(s *Session) { message(s, "hello") }))
		if s.ID() != "restored-session" || s.Header().ID != s.ID() || s.Header().CWD != "/project" {
			t.Fatalf("header=%+v", s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:106
	t.Run("generates a session id when the options carry none", func(t *testing.T) {
		s := load(t, "", makeEntries(func(s *Session) { message(s, "hello") }))
		if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(s.ID()) || s.Header().ID != s.ID() {
			t.Fatal(s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:115
	t.Run("stays off the filesystem", func(t *testing.T) {
		s := load(t, "", makeEntries(func(s *Session) { message(s, "hello") }))
		message(s, "continued")
		if s.Path() != "" || s.flushed {
			t.Fatal("persisted preloaded memory session")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:125
	t.Run("starts an empty session when the entries are empty", func(t *testing.T) {
		s := load(t, "empty-session", nil)
		if s.ID() != "empty-session" || len(s.Entries()) != 0 || s.LeafID() != nil {
			t.Fatal("nonempty state")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:133
	t.Run("takes the session identity from a header among the entries", func(t *testing.T) {
		raw := makeEntries(func(s *Session) { message(s, "hello") })
		raw = append([]json.RawMessage{[]byte(`{"type":"session","version":3,"id":"stored-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/stored"}`)}, raw...)
		s := load(t, "ignored", raw)
		if s.ID() != "stored-session" || s.Header().CWD != "/stored" {
			t.Fatal(s.Header())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:146
	t.Run("migrates entries restored with an older header", func(t *testing.T) {
		s := load(t, "", []json.RawMessage{[]byte(`{"type":"session","version":2,"id":"v2-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/project"}`), restoredEntry(t, `{"type":"message","id":"abc12345","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"hookMessage","content":"from a hook","timestamp":1}}`)})
		var e map[string]any
		if err := json.Unmarshal(s.Entries()[0].Raw(), &e); err != nil {
			t.Fatal(err)
		}
		if s.Header().Version != 3 || e["id"] != "abc12345" || e["message"].(map[string]any)["role"] != "custom" {
			t.Fatal(e)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/load-entries.test.ts:166
	t.Run("adopts headerless entries as current-version without migrating them", func(t *testing.T) {
		raw := restoredEntry(t, `{"type":"message","id":"abc12345","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"hookMessage","content":"from a hook","timestamp":1}}`)
		s := load(t, "", []json.RawMessage{raw})
		if !reflect.DeepEqual(s.Entries()[0].Raw(), raw) {
			t.Fatal("headerless entry migrated")
		}
	})
}

func TestSessionFileMigrationRewritesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.jsonl")
	raw := migrationFixture(1)
	var data []byte
	for _, r := range raw {
		data = append(append(data, r...), '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Header().Version != 3 || len(s.BuildContext(nil)) != 2 {
		t.Fatalf("not migrated: %+v", s.Header())
	}
	reread, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Entries(), reread.Entries()) {
		t.Fatal("migration not persisted")
	}
	fmt.Printf("SESSION_MIGRATION version=%d entries=%d chain=%t\n", s.Header().Version, len(s.Entries()), *s.Entries()[1].Base.ParentID == s.Entries()[0].Base.ID)
}

func TestSessionMigrationCompactionIndex(t *testing.T) {
	for _, index := range []int{1, 3} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			raw := migrationFixture(1)
			raw = append(raw, json.RawMessage(fmt.Sprintf(`{"type":"compaction","timestamp":"2025-01-01T00:00:03Z","summary":"saved","tokensBefore":1000,"firstKeptEntryIndex":%d}`, index)))
			s, err := newSessionFromEntries("/tmp", "", raw)
			if err != nil {
				t.Fatal(err)
			}
			var comp map[string]any
			if err := json.Unmarshal(s.Entries()[2].Raw(), &comp); err != nil {
				t.Fatal(err)
			}
			if comp["firstKeptEntryId"] != s.Entries()[index-1].Base.ID {
				t.Fatalf("compaction boundary = %v", comp)
			}
			if _, found := comp["firstKeptEntryIndex"]; found {
				t.Fatal("legacy index retained")
			}
		})
	}
}
