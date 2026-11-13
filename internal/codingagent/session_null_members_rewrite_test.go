package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// nullMemberLines are entries as the pinned Pi package writes them when an extension sets an optional member to null (coding/testdata/entry-null-members-pi.jsonl,
// with identities and times).
var nullMemberLines = []string{
	`{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.001Z","message":{"role":"user","content":"u","timestamp":1}}`,
	`{"type":"message","id":"00000002","parentId":"00000001","timestamp":"2026-01-01T00:00:00.002Z","message":{"role":"assistant","content":[{"type":"text","text":"a"}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`,
	`{"type":"custom","customType":"t","data":null,"id":"00000003","parentId":"00000002","timestamp":"2026-01-01T00:00:00.003Z"}`,
	`{"type":"custom_message","customType":"t","content":"x","display":true,"details":null,"id":"00000004","parentId":"00000003","timestamp":"2026-01-01T00:00:00.004Z"}`,
	`{"type":"branch_summary","id":"00000005","parentId":"00000004","timestamp":"2026-01-01T00:00:00.005Z","fromId":"00000004","summary":"s","details":null,"usage":null,"fromHook":true}`,
	`{"type":"label","id":"00000006","parentId":"00000005","timestamp":"2026-01-01T00:00:00.006Z","targetId":"00000001","label":null}`,
	`{"type":"compaction","id":"00000007","parentId":"00000006","timestamp":"2026-01-01T00:00:00.007Z","summary":"s","firstKeptEntryId":"00000003","tokensBefore":10,"details":null,"usage":null,"fromHook":true}`,
	`{"type":"message","id":"00000008","parentId":"00000007","timestamp":"2026-01-01T00:00:00.008Z","message":{"role":"user","content":"after","timestamp":3}}`,
}

func writeNullMemberSession(t *testing.T, dir string, version int) string {
	t.Helper()
	return writeSessionFileLines(t, dir, version, nullMemberLines)
}

// writeSessionFileLines writes a session file of the given version that holds lines after its header.
func writeSessionFileLines(t *testing.T, dir string, version int, lines []string) string {
	t.Helper()
	header := `{"type":"session","version":` + string(rune('0'+version)) + `,"id":"01900000-0000-7000-8000-000000000000","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + string(nullMemberJSON(t, dir)) + `}`
	path := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(path, []byte(header+"\n"+strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func nullMemberJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// A file Pi wrote with a member that is null reads back as null and keeps the member through everything that writes entries again: Pi holds the parsed object
// and writes it with JSON.stringify (session-manager.ts _rewriteFile, createBranchedSession, forkFrom), which writes a null. A label entry does not travel with
// a branch copy or a clone (createBranchedSession writes the labels the path's entries carry, and a null label clears one), so those keep the other four.
func TestRewritersKeepNullMembers(t *testing.T) {
	entryLines := nullMemberLines[2:7]
	// A branch copy leaves the label entry out and the entry after it follows the one before (createBranchedSession rebuilds the parent chain); its other
	// members stay as written.
	withoutLabel := append(append([]string{}, entryLines[:3]...), strings.Replace(entryLines[4], `"parentId":"00000006"`, `"parentId":"00000005"`, 1))
	requireLines := func(t *testing.T, path string, lines []string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range lines {
			if !strings.Contains(string(data), line+"\n") {
				t.Errorf("%s does not hold the line %s\n%s", filepath.Base(path), line, data)
			}
		}
	}
	open := func(t *testing.T, version int) (*SessionManager, *Session, string) {
		t.Helper()
		dir := t.TempDir()
		path := writeNullMemberSession(t, dir, version)
		manager := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions"))
		session, err := manager.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return manager, session, path
	}
	t.Run("reload", func(t *testing.T) {
		_, session, _ := open(t, 3)
		states := map[string]string{}
		for _, entry := range session.GetEntries() {
			switch e := entry.(type) {
			case CustomEntry:
				states["custom"] = nullRewriteAny(e.Data)
			case CustomMessageEntry:
				states["custom_message"] = nullRewriteAny(e.Details)
			case BranchSummaryEntry:
				states["branch_summary"] = nullRewriteAny(e.Details) + "," + nullRewritePointer(e.Usage != nil, e.UsageNull)
			case LabelEntry:
				states["label"] = nullRewritePointer(e.Label != nil, e.LabelNull)
			case CompactionEntry:
				states["compaction"] = nullRewriteAny(e.Details) + "," + nullRewritePointer(e.Usage != nil, e.UsageNull)
			}
		}
		want := map[string]string{"custom": "null", "custom_message": "null", "branch_summary": "null,null", "label": "null", "compaction": "null,null"}
		for kind, state := range want {
			if states[kind] != state {
				t.Errorf("%s reads back as %q, want %q", kind, states[kind], state)
			}
		}
	})
	t.Run("migration", func(t *testing.T) {
		_, _, path := open(t, 2)
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), `"version":3`) {
			t.Fatalf("the file was not migrated: %v\n%s", err, data)
		}
		requireLines(t, path, entryLines)
	})
	t.Run("fork", func(t *testing.T) {
		manager, _, path := open(t, 3)
		forked, err := manager.ForkFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, forked.Path(), entryLines)
	})
	t.Run("clone", func(t *testing.T) {
		manager, session, _ := open(t, 3)
		clone, err := manager.Clone(session, *session.GetLeafID())
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, clone.Path(), withoutLabel)
	})
	t.Run("branch copy", func(t *testing.T) {
		_, session, _ := open(t, 3)
		path, err := session.CreateBranchedSession(*session.GetLeafID())
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, path, withoutLabel)
	})
	t.Run("export", func(t *testing.T) {
		_, session, _ := open(t, 3)
		out, err := ExportSessionToJsonl(session, filepath.Join(t.TempDir(), "export.jsonl"), nil)
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, out, entryLines[:3])
	})
	t.Run("first flush", func(t *testing.T) {
		dir := t.TempDir()
		session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		// Entries appended before the first user or assistant message are written when that message creates the file.
		if _, err := session.AppendCustomEntry("buffered", json.RawMessage("null")); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendCustomMessageEntry("buffered", "x", true, json.RawMessage("null")); err != nil {
			t.Fatal(err)
		}
		for _, line := range nullMemberLines[:2] {
			if err := session.AppendEntry(json.RawMessage(line)); err != nil {
				t.Fatal(err)
			}
		}
		data, err := os.ReadFile(session.Path())
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range []string{`"customType":"buffered","data":null,`, `"content":"x","display":true,"details":null,`} {
			if !strings.Contains(string(data), member) {
				t.Errorf("the flushed file lacks %s\n%s", member, data)
			}
		}
	})
}

// nullRewriteAny and nullRewritePointer are what a Go reader of a decoded member is told: an `any` member holds orderedjson.Null for a null, a pointer member
// has its marker.
func nullRewriteAny(value any) string {
	switch {
	case value == nil:
		return "absent"
	case orderedjson.IsNull(value):
		return "null"
	}
	return "value"
}

func nullRewritePointer(set, null bool) string {
	switch {
	case set:
		return "value"
	case null:
		return "null"
	}
	return "absent"
}

// A tool_result handler that returns details: null does not replace the tool's own details (agent-loop.ts `afterResult.details ?? result.details`); the decode
// keeps the null for the next handler (runner.ts `handlerResult.details !== undefined`) and the override drops it.
func TestToolResultHandlerNullDetailsKeepTheToolsOwn(t *testing.T) {
	var result extension.ToolResultEventResult
	if err := json.Unmarshal([]byte(`{"details":null}`), &result); err != nil {
		t.Fatal(err)
	}
	if !orderedjson.IsNull(result.Details) {
		t.Fatalf("details: null decoded as %#v", result.Details)
	}
	if override := ToolResultEventOverride(&result); override.Details != nil {
		t.Errorf("the override replaces the tool's details with %#v", override.Details)
	}
}

// authoredUsageLines are entries as the pinned Pi package writes them when an extension supplies a usage in its own member order and with a member Pi's Usage
// does not declare (coding/testdata/entry-null-members-pi.jsonl, rows "... usage as its author wrote it", with identities and times).
var authoredUsageLines = []string{
	nullMemberLines[0],
	nullMemberLines[1],
	`{"type":"branch_summary","id":"00000003","parentId":"00000002","timestamp":"2026-01-01T00:00:00.003Z","fromId":"00000002","summary":"s","usage":{"cost":{"total":0.5,"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"totalTokens":3,"output":2,"input":1,"cacheRead":0,"cacheWrite":0,"extra":1},"fromHook":true}`,
	`{"type":"compaction","id":"00000004","parentId":"00000003","timestamp":"2026-01-01T00:00:00.004Z","summary":"s","firstKeptEntryId":"00000001","tokensBefore":10,"usage":{"cost":{"total":0.25,"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"totalTokens":30,"output":20,"input":10,"cacheRead":0,"cacheWrite":0,"extra":1},"fromHook":true}`,
	`{"type":"message","id":"00000005","parentId":"00000004","timestamp":"2026-01-01T00:00:00.005Z","message":{"role":"user","content":"after","timestamp":3}}`,
}

// A file Pi wrote with a usage as an extension authored it keeps the object, member for member, through everything that writes entries again (Pi holds the
// parsed object and writes it with JSON.stringify), and the typed readers get the members Pi's readers use: input, output, cacheRead, cacheWrite and
// cost.total (usage-totals.ts addUsageToTotals).
func TestRewritersKeepAnAuthoredUsage(t *testing.T) {
	entryLines := authoredUsageLines[2:4]
	requireLines := func(t *testing.T, path string, lines []string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range lines {
			if !strings.Contains(string(data), line+"\n") {
				t.Errorf("%s does not hold the line %s\n%s", filepath.Base(path), line, data)
			}
		}
	}
	open := func(t *testing.T, version int) (*SessionManager, *Session, string) {
		t.Helper()
		dir := t.TempDir()
		path := writeSessionFileLines(t, dir, version, authoredUsageLines)
		manager := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions"))
		session, err := manager.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return manager, session, path
	}
	t.Run("reload", func(t *testing.T) {
		_, session, _ := open(t, 3)
		got := map[string]ai.Usage{}
		for _, entry := range session.GetEntries() {
			switch e := entry.(type) {
			case BranchSummaryEntry:
				if e.Usage != nil {
					got["branch_summary"] = *e.Usage
				}
			case CompactionEntry:
				if e.Usage != nil {
					got["compaction"] = *e.Usage
				}
			}
		}
		for kind, want := range map[string]ai.Usage{
			"branch_summary": {Input: 1, Output: 2, TotalTokens: 3, Cost: ai.UsageCost{Total: 0.5}},
			"compaction":     {Input: 10, Output: 20, TotalTokens: 30, Cost: ai.UsageCost{Total: 0.25}},
		} {
			if !reflect.DeepEqual(got[kind], want) {
				t.Errorf("%s usage reads back as %+v, want %+v", kind, got[kind], want)
			}
		}
	})
	t.Run("migration", func(t *testing.T) {
		_, _, path := open(t, 2)
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), `"version":3`) {
			t.Fatalf("the file was not migrated: %v\n%s", err, data)
		}
		requireLines(t, path, entryLines)
	})
	t.Run("fork", func(t *testing.T) {
		manager, _, path := open(t, 3)
		forked, err := manager.ForkFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, forked.Path(), entryLines)
	})
	t.Run("clone", func(t *testing.T) {
		manager, session, _ := open(t, 3)
		clone, err := manager.Clone(session, *session.GetLeafID())
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, clone.Path(), entryLines)
	})
	t.Run("branch copy", func(t *testing.T) {
		_, session, _ := open(t, 3)
		path, err := session.CreateBranchedSession(*session.GetLeafID())
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, path, entryLines)
	})
	t.Run("export", func(t *testing.T) {
		_, session, _ := open(t, 3)
		out, err := ExportSessionToJsonl(session, filepath.Join(t.TempDir(), "export.jsonl"), nil)
		if err != nil {
			t.Fatal(err)
		}
		requireLines(t, out, entryLines[:1])
	})
}
