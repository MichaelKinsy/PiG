// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fileOperationWrite(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
func fileOperationHeader(cwd, id string) string {
	return fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"timestamp\":\"2025-01-01T00:00:00Z\",\"cwd\":%q}\n", id, cwd)
}

const fileOperationLegacyHeader = "{\"type\":\"session\",\"id\":\"abc\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"cwd\":\"/tmp\"}\n"

const fileOperationUser = `{"type":"message","id":"1","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}`

func TestLoadEntriesFromFileUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		missing       bool
		want          int
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:47
		{"returns empty array for non-existent file", "", true, 0},
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:52
		{"returns empty array for empty file", "", false, 0},
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:58
		{"returns empty array for file without valid session header", "{\"type\":\"message\",\"id\":\"1\"}\n", false, 0},
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:64
		{"returns empty array for malformed JSON", "not json\n", false, 0},
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:70
		{"loads valid session file", fileOperationLegacyHeader + fileOperationUser + "\n", false, 2},
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:83
		{"skips malformed lines but keeps valid ones", fileOperationLegacyHeader + "not valid json\n" + fileOperationUser + "\n", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "file.jsonl")
			if !tc.missing {
				fileOperationWrite(t, dir, "file.jsonl", tc.content)
			}
			entries, err := LoadEntriesFromFile(path)
			if err != nil || entries == nil || len(entries) != tc.want {
				t.Fatalf("entries=%v err=%v", entries, err)
			}
			if tc.want == 2 {
				for i, want := range []string{"session", "message"} {
					var e SessionEntryBase
					if err := json.Unmarshal(entries[i], &e); err != nil {
						t.Fatal(err)
					}
					if e.Type != want {
						t.Fatal(e)
					}
				}
			}
		})
	}
	for _, tc := range []struct {
		name, content string
		want          int
		newline       bool
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:95
		{"adds a newline after an unterminated valid record", fileOperationLegacyHeader + fileOperationUser, 2, true},
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:106
		{"adds a newline after an unterminated malformed final fragment", fileOperationLegacyHeader + `{"type":"message"`, 1, true},
		// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:116
		{"does not modify an unterminated non-session file", `{"type":"message","id":"1"}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fileOperationWrite(t, t.TempDir(), "file.jsonl", tc.content)
			entries, err := LoadEntriesFromFile(path)
			if err != nil || len(entries) != tc.want {
				t.Fatalf("entries=%d err=%v", len(entries), err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.content
			if tc.newline {
				want += "\n"
			}
			if string(data) != want {
				t.Fatalf("file=%q want=%q", data, want)
			}
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:125 (all three table rows)
	for _, tc := range []struct{ name, prefix, id string }{{"leading blank lines", "\n  \n", "leading-blank"}, {"leading malformed lines", "not json\n{broken json\n", "leading-malformed"}, {"a multi-buffer header", "", strings.Repeat("a", 8192)}} {
		t.Run("reads cwd from a session with "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cwd := filepath.Join(dir, "stored-project")
			path := fileOperationWrite(t, dir, "header.jsonl", tc.prefix+fileOperationHeader(cwd, tc.id))
			s, err := NewSessionManagerWithDir(dir, dir).Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.ID() != tc.id || s.CWD() != cwd {
				t.Fatalf("id=%q cwd=%q", s.ID(), s.CWD())
			}
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:139 (both header/prefix cases and both override states)
	t.Run("opens compatible sessions beyond the discovery scan limit", func(t *testing.T) {
		for _, tc := range []struct{ name, id, prefix string }{{"large-header", strings.Repeat("a", 1024*1024+1), ""}, {"large-prefix", "large-prefix", strings.Repeat("x", 1024*1024+1) + "\n"}} {
			dir := t.TempDir()
			stored, override := filepath.Join(dir, "stored-project"), filepath.Join(dir, "override-project")
			path := fileOperationWrite(t, dir, tc.name+".jsonl", tc.prefix+fileOperationHeader(stored, tc.id))
			for _, useOverride := range []bool{false, true} {
				var overrides []string
				want := stored
				if useOverride {
					overrides = []string{override}
					want = override
				}
				s, err := NewSessionManagerWithDir(dir, dir).Open(path, overrides...)
				if err != nil {
					t.Fatal(err)
				}
				if s.ID() != tc.id || s.CWD() != want {
					t.Fatalf("id/cwd mismatch for %s override=%v", tc.name, useOverride)
				}
			}
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:162
	t.Run("opens session files larger than Node's max string length", func(t *testing.T) {
		output, err := exec.CommandContext(t.Context(), "node", "-e", "process.stdout.write(String(require('node:buffer').constants.MAX_STRING_LENGTH))").Output()
		if err != nil {
			t.Fatal(err)
		}
		limit, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		path := fileOperationWrite(t, dir, "large.jsonl", fileOperationHeader("/tmp", "abc"))
		file, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		const stride int64 = 16 * 1024 * 1024
		for offset := stride; offset <= limit+stride; offset += stride {
			if _, err := file.WriteAt([]byte{'\n'}, offset); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		appendFile, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := appendFile.WriteString(fileOperationUser + "\n"); err != nil {
			_ = appendFile.Close()
			t.Fatal(err)
		}
		if err := appendFile.Close(); err != nil {
			t.Fatal(err)
		}
		s, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.ID() != "abc" || len(s.Entries()) != 1 {
			t.Fatalf("id=%s entries=%d", s.ID(), len(s.Entries()))
		}
		messages := s.BuildContext(nil)
		if len(messages) != 1 || messages[0].User == nil || extractUserText(messages[0]) != "hi" || messages[0].User.Timestamp != 1 {
			t.Fatal(messages)
		}
	})
}

func TestFindMostRecentSessionUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:204
	t.Run("returns null for empty directory", func(t *testing.T) {
		dir := t.TempDir()
		if got := NewSessionManagerWithDir(dir, dir).FindMostRecent(); got != "" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:208
	t.Run("returns null for non-existent directory", func(t *testing.T) {
		dir := t.TempDir()
		if got := NewSessionManagerWithDir(dir, filepath.Join(dir, "nonexistent")).FindMostRecent(); got != "" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:212
	t.Run("ignores non-jsonl files", func(t *testing.T) {
		dir := t.TempDir()
		fileOperationWrite(t, dir, "file.txt", "hello")
		fileOperationWrite(t, dir, "file.json", "{}")
		if got := NewSessionManagerWithDir(dir, dir).FindMostRecent(); got != "" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:218
	t.Run("ignores jsonl files without valid session header", func(t *testing.T) {
		dir := t.TempDir()
		fileOperationWrite(t, dir, "invalid.jsonl", "{\"type\":\"message\"}\n")
		if got := NewSessionManagerWithDir(dir, dir).FindMostRecent(); got != "" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:223
	t.Run("returns single valid session file", func(t *testing.T) {
		dir := t.TempDir()
		path := fileOperationWrite(t, dir, "session.jsonl", fileOperationHeader("/tmp", "abc"))
		if got := NewSessionManagerWithDir(dir, dir).FindMostRecent(); got != path {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:229
	t.Run("returns most recently modified session", func(t *testing.T) {
		dir := t.TempDir()
		old := fileOperationWrite(t, dir, "older.jsonl", fileOperationHeader("/tmp", "old"))
		recent := fileOperationWrite(t, dir, "newer.jsonl", fileOperationHeader("/tmp", "new"))
		if err := os.Chtimes(old, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(recent, time.Unix(2, 0), time.Unix(2, 0)); err != nil {
			t.Fatal(err)
		}
		if got := NewSessionManagerWithDir(dir, dir).FindMostRecent(); got != recent {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:241
	t.Run("skips invalid files and returns valid one", func(t *testing.T) {
		dir := t.TempDir()
		fileOperationWrite(t, dir, "invalid.jsonl", "{\"type\":\"not-session\"}\n")
		path := fileOperationWrite(t, dir, "valid.jsonl", fileOperationHeader("/tmp", "abc"))
		if got := NewSessionManagerWithDir(dir, dir).FindMostRecent(); got != path {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:252
	t.Run("skips oversized corrupt files and returns valid session", func(t *testing.T) {
		dir := t.TempDir()
		fileOperationWrite(t, dir, "oversized.jsonl", strings.Repeat("x", 1024*1024+1))
		path := fileOperationWrite(t, dir, "valid.jsonl", fileOperationHeader("/tmp", "abc"))
		if got := NewSessionManagerWithDir(dir, dir).FindMostRecent(); got != path {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:261
	t.Run("filters most recent session by cwd", func(t *testing.T) {
		dir := t.TempDir()
		a, b := filepath.Join(dir, "project-a"), filepath.Join(dir, "project-b")
		pa := fileOperationWrite(t, dir, "a.jsonl", fileOperationHeader(a, "a"))
		pb := fileOperationWrite(t, dir, "b.jsonl", fileOperationHeader(b, "b"))
		if err := os.Chtimes(pa, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(pb, time.Unix(2, 0), time.Unix(2, 0)); err != nil {
			t.Fatal(err)
		}
		if got := NewSessionManagerWithDir(a, dir).FindMostRecentForContinue(); got != pa {
			t.Fatal(got)
		}
		if got := NewSessionManagerWithDir(b, dir).FindMostRecentForContinue(); got != pb {
			t.Fatal(got)
		}
	})
}

func TestSessionFileOperationsUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:326
	t.Run("scopes current-folder APIs by cwd while listing all flat sessions", func(t *testing.T) {
		dir := t.TempDir()
		a, b := t.TempDir(), t.TempDir()
		pa := listingPersistedSession(t, dir, a, "from A")
		pb := listingPersistedSession(t, dir, b, "from B")
		m := NewSessionManagerWithDir(a, dir)
		current, err := m.ListCurrentSessions()
		if err != nil || len(current) != 1 || current[0].Path != pa {
			t.Fatalf("current=%v err=%v", current, err)
		}
		all, err := m.ListAllSessions()
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, info := range all {
			paths = append(paths, info.Path)
		}
		slices.Sort(paths)
		want := []string{pa, pb}
		slices.Sort(want)
		if !slices.Equal(paths, want) {
			t.Fatal(paths)
		}
		if got := m.FindMostRecentForContinue(); got != pa {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:370
	t.Run("truncates and rewrites empty file with valid header", func(t *testing.T) {
		dir := t.TempDir()
		path := fileOperationWrite(t, dir, "empty.jsonl", "")
		s, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.ID() == "" || s.Header().Type != "session" {
			t.Fatal(s.Header())
		}
		records := readJSONLLines(t, path)
		if len(records) != 1 || records[0]["type"] != "session" || records[0]["id"] != s.ID() {
			t.Fatal(records)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:390
	t.Run("throws and preserves non-empty file without valid header", func(t *testing.T) {
		dir := t.TempDir()
		original := `{"type":"message","id":"abc","parentId":"orphaned","timestamp":"2025-01-01T00:00:00Z","message":{"role":"assistant","content":"test"}}` + "\n"
		path := fileOperationWrite(t, dir, "no-header.jsonl", original)
		_, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err == nil || err.Error() != "Session file is not a valid pi session: "+path {
			t.Fatalf("error=%v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != original {
			t.Fatal("file changed", err)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:402
	t.Run("throws and preserves non-session JSONL files", func(t *testing.T) {
		dir := t.TempDir()
		original := "{\"type\":\"event\",\"data\":\"not a session\"}\n"
		path := fileOperationWrite(t, dir, "not-a-session.log", original)
		_, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err == nil || err.Error() != "Session file is not a valid pi session: "+path {
			t.Fatalf("error=%v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != original {
			t.Fatal("file changed", err)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:413
	t.Run("preserves explicit session file path when recovering from corrupted file", func(t *testing.T) {
		dir := t.TempDir()
		path := fileOperationWrite(t, dir, "my-session.jsonl", "")
		s, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.Path() != path {
			t.Fatal(s.Path())
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:423
	t.Run("subsequent loads of initialized empty file work correctly", func(t *testing.T) {
		dir := t.TempDir()
		path := fileOperationWrite(t, dir, "empty.jsonl", "")
		m := NewSessionManagerWithDir(dir, dir)
		one, err := m.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		two, err := m.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if one.ID() != two.ID() || two.Header().Type != "session" {
			t.Fatal("identity changed")
		}
	})
}

// readSessionFileRoles mirrors upstream utilities.ts readSessionFileRoles (.upstream/v0.99.1/packages/coding-agent/test/utilities.ts:159): the role of each message entry, otherwise the entry type.
func readSessionFileRoles(t *testing.T, path string) []string {
	t.Helper()
	var roles []string
	for _, record := range readJSONLLines(t, path) {
		if message, ok := record["message"].(map[string]any); ok {
			if role, ok := message["role"].(string); ok {
				roles = append(roles, role)
				continue
			}
		}
		roles = append(roles, record["type"].(string))
	}
	return roles
}

func TestSessionFileCreationUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:447
	t.Run("does not create a file for a session with only setup entries", func(t *testing.T) {
		s, err := tempSessionMgr(t).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AppendModelSwitch("anthropic", "claude-sonnet-4-5", ""); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendThinkingLevelChange("off"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
			t.Fatalf("setup entries alone created %s: %v", s.Path(), err)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:456 (#10000: the first prompt must survive a first turn that never produces an assistant message)
	t.Run("creates the file when the first user message is appended", func(t *testing.T) {
		dir := t.TempDir()
		manager := NewSessionManagerWithDir(dir, dir)
		s, err := manager.Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AppendModelSwitch("anthropic", "claude-sonnet-4-5", ""); err != nil {
			t.Fatal(err)
		}
		upstreamSessionUser(t, s, "first question")

		if got := readSessionFileRoles(t, s.Path()); !slices.Equal(got, []string{"session", "model_change", "user"}) {
			t.Fatalf("roles = %v", got)
		}
		opened, err := NewSessionManagerWithDir(dir, dir).Open(s.Path())
		if err != nil {
			t.Fatal(err)
		}
		if got := len(opened.BuildContext(nil)); got != 1 {
			t.Fatalf("resumed context has %d messages, want 1", got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:466
	t.Run("appends later entries to the file without rewriting earlier ones", func(t *testing.T) {
		s, err := tempSessionMgr(t).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		upstreamSessionUser(t, s, "first question")
		upstreamCustom(t, s, "preset-state", map[string]any{"name": "plan"})
		upstreamSessionAssistant(t, s, "first answer")

		if got := readSessionFileRoles(t, s.Path()); !slices.Equal(got, []string{"session", "user", "custom", "assistant"}) {
			t.Fatalf("roles = %v", got)
		}
	})
}
