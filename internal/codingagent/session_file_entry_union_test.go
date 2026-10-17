package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/ai"
)

// session-manager.ts:183-197: a record of a session file is a SessionHeader or one of the eleven members of SessionEntry. LoadEntriesFromFile
// returns the header first and each entry as the member its type names, and a record no member describes stays in place as the RawEntry that
// holds it (loadEntriesFromFile casts what JSON.parse returns).
func TestLoadEntriesFromFileReturnsTheMembersOfFileEntry(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w","future":1}`,
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1}}`,
		`{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-01-01T00:00:02.000Z","thinkingLevel":"high"}`,
		`{"type":"model_change","id":"mc","parentId":"t1","timestamp":"2026-01-01T00:00:03.000Z","provider":"p","modelId":"m"}`,
		`{"type":"usage","id":"us","parentId":"mc","timestamp":"2026-01-01T00:00:04.000Z","kind":"cache_warm","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`,
		`{"type":"custom","id":"cu","parentId":"us","timestamp":"2026-01-01T00:00:05.000Z","customType":"x","data":{"b":1,"a":2}}`,
		`{"type":"custom_message","id":"cm","parentId":"cu","timestamp":"2026-01-01T00:00:06.000Z","customType":"x","content":"c","display":true}`,
		`{"type":"label","id":"lb","parentId":"cm","timestamp":"2026-01-01T00:00:07.000Z","targetId":"m1","label":"mark"}`,
		`{"type":"session_info","id":"si","parentId":"lb","timestamp":"2026-01-01T00:00:08.000Z","name":"n"}`,
		`{"type":"branch_summary","id":"bs","parentId":"si","timestamp":"2026-01-01T00:00:09.000Z","fromId":"m1","summary":"s"}`,
		`{"type":"compaction","id":"co","parentId":"bs","timestamp":"2026-01-01T00:00:10.000Z","summary":"s","firstKeptEntryId":"m1","tokensBefore":1}`,
		`{"type":"context_edit","id":"ce","parentId":"co","timestamp":"2026-01-01T00:00:11.000Z","targetId":"m1","replacement":null}`,
		`{"type":"from_the_future","id":"ff","parentId":"ce","timestamp":"2026-01-01T00:00:12.000Z","new":{"z":1}}`,
	}
	path := fileOperationWrite(t, t.TempDir(), "s.jsonl", strings.Join(lines, "\n")+"\n")
	records, err := LoadEntriesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != len(lines) {
		t.Fatalf("%d records, want %d", len(records), len(lines))
	}
	want := []any{SessionHeader{}, MessageEntry{}, ThinkingLevelEntry{}, ModelChangeEntry{}, UsageEntry{}, CustomEntry{}, CustomMessageEntry{}, LabelEntry{}, SessionInfoEntry{}, BranchSummaryEntry{}, CompactionEntry{}, ContextEditEntry{}, RawEntry{}}
	for i, record := range records {
		if got, wantType := typeName(record), typeName(want[i]); got != wantType {
			t.Errorf("record %d is %s, want %s", i, got, wantType)
		}
		if string(record.Raw()) != lines[i] {
			t.Errorf("record %d writes %s, want the line it was read from %s", i, record.Raw(), lines[i])
		}
	}
	if header := records[0].(SessionHeader); header.ID != "u1" || header.CWD != "/w" {
		t.Errorf("header = %+v", header)
	}
	if custom := records[5].(CustomEntry); custom.CustomType != "x" {
		t.Errorf("custom entry = %+v", custom)
	}
}

func typeName(v any) string { return reflect.TypeOf(v).Name() }

// loadEntries and the session manager's rewrite of a file at an older version (session-manager.ts _loadEntries, migrateToCurrentVersion,
// _rewriteFile) keep what Pi's parsed objects keep: members the entry types do not name, and the order of every member. The entries of the loaded
// session are the typed members, and an unfamiliar record keeps its place in the rewritten file.
func TestRewriteOfAMigratedFileKeepsUnnamedMembersAndOrder(t *testing.T) {
	dir := t.TempDir()
	header := `{"type":"session","version":2,"id":"old","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(dir) + `,"provider":"p","modelId":"m"}`
	user := `{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","extra":{"z":1,"a":2},"message":{"role":"user","content":"hi","timestamp":1}}`
	hook := `{"type":"message","id":"bbbbbbbb","parentId":"aaaaaaaa","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"hookMessage","customType":"x","content":"c","display":true,"timestamp":2}}`
	unknown := `{"type":"from_the_future","id":"cccccccc","parentId":"bbbbbbbb","timestamp":"2026-01-01T00:00:03.000Z","new":{"z":1,"a":2}}`
	path := fileOperationWrite(t, dir, "old.jsonl", strings.Join([]string{header, user, hook, unknown}, "\n")+"\n")

	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := session.GetEntries()
	if len(entries) != 3 {
		t.Fatalf("%d entries, want 3", len(entries))
	}
	if _, ok := entries[0].(MessageEntry); !ok {
		t.Errorf("entry 0 is %T, want MessageEntry", entries[0])
	}
	if message, ok := entries[1].(MessageEntry); !ok || message.Message.Role() != "custom" {
		t.Errorf("entry 1 = %T, want a MessageEntry with role custom (migrateV2ToV3)", entries[1])
	}
	if _, ok := entries[2].(RawEntry); !ok {
		t.Errorf("entry 2 is %T, want the RawEntry that holds a record of a type this build does not know", entries[2])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	wantLines := []string{
		strings.Replace(header, `"version":2`, `"version":3`, 1),
		user,
		strings.Replace(hook, `"role":"hookMessage"`, `"role":"custom"`, 1),
		unknown,
	}
	if strings.Join(got, "\n") != strings.Join(wantLines, "\n") {
		t.Errorf("rewritten file:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantLines, "\n"))
	}
	if filepath.Base(path) != "old.jsonl" {
		t.Fatal("the file moved")
	}
}

// The entries a session appends are members of the union from the moment they are appended: Session.GetEntries returns the member each Append
// method wrote, whose fields are what the method was given and whose record is the line written to the file.
func TestAppendedEntriesAreMembersOfSessionEntry(t *testing.T) {
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, dir).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	user := upstreamSessionUser(t, session, "hi")
	upstreamSessionAssistant(t, session, "ok")
	label := "mark"
	appenders := []struct {
		member SessionEntry
		append func() error
	}{
		{ThinkingLevelEntry{}, func() error { _, err := session.AppendThinkingLevelChange("high"); return err }},
		{ModelChangeEntry{}, func() error { _, err := session.AppendModelChange("p", "m"); return err }},
		{UsageEntry{}, func() error {
			_, err := session.AppendUsage("cache_warm", "p", "m", ai.Usage{Input: 1}, "n")
			return err
		}},
		{CustomEntry{}, func() error { _, err := session.AppendCustomEntry("x", map[string]any{"b": 1, "a": 2}); return err }},
		{CustomMessageEntry{}, func() error { _, err := session.AppendCustomMessageEntry("x", "c", true, nil); return err }},
		{LabelEntry{}, func() error { _, err := session.AppendLabelChange(user, &label); return err }},
		{SessionInfoEntry{}, func() error { _, err := session.AppendSessionInfo("n"); return err }},
		{CompactionEntry{}, func() error { _, err := session.AppendCompaction("s", user, 5, nil, false, nil); return err }},
		{ContextEditEntry{}, func() error { _, err := session.AppendContextEdit(user, nil); return err }},
		{BranchSummaryEntry{}, func() error { _, err := session.BranchWithSummary(&user, "s", nil, false, nil); return err }},
	}
	data := func() []string {
		raw, err := os.ReadFile(session.Path())
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	for _, row := range appenders {
		before := len(session.GetEntries())
		if err := row.append(); err != nil {
			t.Fatal(err)
		}
		entries := session.GetEntries()
		if len(entries) != before+1 {
			t.Fatalf("%T: %d entries, want %d", row.member, len(entries), before+1)
		}
		appended := entries[before]
		if typeName(appended) != typeName(row.member) {
			t.Errorf("appended entry is %s, want %s", typeName(appended), typeName(row.member))
		}
		if lines := data(); string(appended.Raw()) != lines[len(lines)-1] {
			t.Errorf("%s: record %s differs from the line written %s", typeName(appended), appended.Raw(), lines[len(lines)-1])
		}
	}
	if custom := session.GetEntries()[5].(CustomEntry); custom.CustomType != "x" || custom.Data == nil {
		t.Errorf("custom entry = %+v", custom)
	}
}

// migrateToCurrentVersion edits the header object (session-manager.ts migrateV1ToV2), so the header of a session loaded from an older file reports
// the current version wherever it is read: GetHeader (the extension API's getHeader) writes version 3, not the version-2 record.
func TestMigratedHeaderCarriesTheCurrentVersion(t *testing.T) {
	header := `{"type":"session","version":2,"id":"old","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	user := `{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1}}`
	session, err := newSessionFromEntries("/w", "", fileEntriesOf([]json.RawMessage{json.RawMessage(header), json.RawMessage(user)}))
	if err != nil {
		t.Fatal(err)
	}
	got := session.GetHeader()
	if got.Version != CurrentSessionVersion {
		t.Errorf("header version = %d, want %d", got.Version, CurrentSessionVersion)
	}
	out, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(out), `"version":3`) || strings.Contains(string(out), `"version":2`) {
		t.Errorf("header writes %s, %v, want version 3", out, err)
	}
}

// getLatestCompactionEntry casts the newest entry of that type without validating it (session-manager.ts:372-379): a compaction entry whose
// members are ill-typed is a RawEntry here, and it is still the newest, with its base members.
func TestGetLatestCompactionEntryKeepsAnUndecodableNewestEntry(t *testing.T) {
	good := decodeEntry(`{"type":"compaction","id":"c1","parentId":null,"timestamp":"t1","summary":"s","firstKeptEntryId":"k","tokensBefore":1}`)
	bad := decodeEntry(`{"type":"compaction","id":"c2","parentId":"c1","timestamp":"t2","summary":5}`)
	if _, ok := bad.(RawEntry); !ok {
		t.Fatalf("fixture is %T, want an entry no member describes", bad)
	}
	got := GetLatestCompactionEntry([]SessionEntry{good, bad})
	if got == nil || got.ID != "c2" || got.Timestamp != "t2" || got.Summary != "" {
		t.Fatalf("GetLatestCompactionEntry = %+v, want the undecodable newest entry with its base members only", got)
	}
}

// Pi keeps every truthy record of a session file as an entry and checks none of its members (session-manager.ts parseSessionEntryLine,
// _buildIndex): a record that is no object, or whose id is no string, loads, its id (undefined for a non-object) becomes the leaf, the
// context walks from it, and the next entry's parentId is that value, absent when it is undefined. Each case was probed on Pi 1.1.0 over RPC
// (get_state messageCount, then the file after a prompt), with the record after the assistant ("end") or between the user and the assistant.
func TestSessionKeepsRecordsThatAreNotEntriesAsPiDoes(t *testing.T) {
	header := `{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	user := `{"type":"message","id":"aaaa0001","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"first"}],"timestamp":1767225601000}}`
	assistant := `{"type":"message","id":"aaaa0002","parentId":"aaaa0001","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1767225602000}}`
	const absent = "absent"
	cases := []struct {
		record   string
		end      bool
		messages int
		parentID string
		// branch is the length of getBranch after the next entry: it walks `current.parentId ? byId.get(current.parentId) : undefined`.
		branch int
	}{
		{`[1]`, true, 0, absent, 1},
		{`[1]`, false, 2, `"aaaa0002"`, 3},
		{`true`, true, 0, absent, 1},
		{`true`, false, 2, `"aaaa0002"`, 3},
		{`"x"`, true, 0, absent, 1},
		{`"x"`, false, 2, `"aaaa0002"`, 3},
		{`{"type":"custom","id":5}`, true, 0, `5`, 2},
		{`{"type":"custom","id":5}`, false, 2, `"aaaa0002"`, 3},
		{`{"type":"custom","customType":"x"}`, true, 0, absent, 1},
		{`{"type":"custom","customType":"x","id":null}`, true, 0, `null`, 1},
		{`{"type":"custom","customType":"x","id":7,"parentId":"aaaa0002"}`, true, 2, `7`, 4},
		{`{"type":"custom","customType":"x","id":"c","parentId":0}`, true, 0, `"c"`, 2},
		{`{"type":"custom","customType":"x","id":0}` + "\n" + `{"type":"custom","customType":"x","id":"z","parentId":0}`, true, 0, `"z"`, 2},
	}
	for _, tc := range cases {
		lines := []string{header, user}
		if !tc.end {
			lines = append(lines, tc.record)
		}
		lines = append(lines, assistant)
		if tc.end {
			lines = append(lines, tc.record)
		}
		for _, load := range []string{"open", "restore"} {
			t.Run(fmt.Sprintf("%s end=%v %s", tc.record, tc.end, load), func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "s.jsonl")
				if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				var session *Session
				var err error
				if load == "open" {
					session, err = NewSessionManagerWithDir(dir, dir).Open(path)
				} else {
					var records []json.RawMessage
					for line := range strings.SplitSeq(strings.Join(lines, "\n"), "\n") {
						records = append(records, json.RawMessage(line))
					}
					session, err = restoreSessionFileEntries(path, fileEntriesOf(records))
				}
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if got := len(session.BuildSessionContext().Messages); got != tc.messages {
					t.Errorf("context holds %d messages, want %d", got, tc.messages)
				}
				// Before the next entry the branch is that entry's parent path, one entry shorter, for the session and for an extension's reads.
				if got := len(session.GetBranch()); got != tc.branch-1 {
					t.Errorf("before the next entry the branch holds %d entries, want %d", got, tc.branch-1)
				}
				log, err := newSessionReadLog(session)
				if err != nil {
					t.Fatalf("an extension's read: %v", err)
				}
				if got := len(log.branch(nil)); got != tc.branch-1 {
					t.Errorf("an extension's getBranch holds %d entries, want %d", got, tc.branch-1)
				}
				if _, err := session.AppendModelChange("p", "m2"); err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				written := strings.Split(strings.TrimSpace(string(content)), "\n")
				var members map[string]json.RawMessage
				if err := json.Unmarshal([]byte(written[len(written)-1]), &members); err != nil {
					t.Fatal(err)
				}
				parentID := absent
				if value, present := members["parentId"]; present {
					parentID = string(value)
				}
				if parentID != tc.parentID {
					t.Errorf("the next entry's parentId is %s, want %s", parentID, tc.parentID)
				}
				if got := len(session.GetBranch()); got != tc.branch {
					t.Errorf("the branch holds %d entries, want %d", got, tc.branch)
				}
			})
		}
	}
}

func decodeEntry(raw string) SessionEntry {
	return sessionentry.DecodeSessionEntry(json.RawMessage(raw))
}

// _loadEntries takes the first record of type "session" as the header whether or not its id is a string, and migrateV1ToV2/migrateV2ToV3 set
// that object's version (session-manager.ts:287-331). A header record without an id is no SessionHeader member, so the migrated header is found
// again the way it was found before the migration: by its type, not by its Go member.
func TestMigratedHeaderWithoutAnIDCarriesTheCurrentVersion(t *testing.T) {
	header := `{"type":"session","version":2,"timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	user := `{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1}}`
	session, err := newSessionFromEntries("/w", "", fileEntriesOf([]json.RawMessage{json.RawMessage(header), json.RawMessage(user)}))
	if err != nil {
		t.Fatal(err)
	}
	got := session.GetHeader()
	out, err := json.Marshal(got)
	if got.Version != CurrentSessionVersion || err != nil || !strings.Contains(string(out), `"version":3`) {
		t.Errorf("header = %+v writes %s, %v, want version 3", got, out, err)
	}
}
