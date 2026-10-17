package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A compaction read from a file or appended holds no record when it encodes to its line again, and keeps it otherwise; every compaction
// serves its line's bytes, and the context built from it holds its summary.
func TestCompactionEntriesServeTheirLines(t *testing.T) {
	summary := `## Goal\n- read \"main.go\" <&> done\n`
	lines := []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-13T08:00:00.000Z","cwd":"/project"}`,
		`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-10-13T08:00:01.000Z","message":{"role":"user","content":"one","timestamp":1}}`,
		`{"type":"message","id":"a2","parentId":"a1","timestamp":"2026-10-13T08:00:02.000Z","message":{"role":"user","content":"two","timestamp":2}}`,
		`{"type":"compaction","id":"c1","parentId":"a2","timestamp":"2026-10-13T08:00:03.000Z","summary":"` + summary + `","firstKeptEntryId":"a2","tokensBefore":1000,"fromHook":false}`,
		`{"type":"compaction","id":"c2","parentId":"c1","timestamp":"2026-10-13T08:00:04.000Z","summary":"` + summary + `","firstKeptEntryId":"a2","tokensBefore":1000,"details":{"z":1,"a":[2,{"y":1,"b":2}]},"fromHook":true}`,
		`{"type":"compaction","id":"c3","parentId":"c2","timestamp":"2026-10-13T08:00:05.000Z","summary":"` + summary + `read\/written","firstKeptEntryId":"a2","tokensBefore":1000,"fromHook":false,"extra":1}`,
		`{"type":"message","id":"a3","parentId":"c3","timestamp":"2026-10-13T08:00:06.000Z","message":{"role":"user","content":"three","timestamp":3}}`,
	}
	// c3 is spelled as JSON.stringify does not write it (an escape it does not write, a member the entry does not name).
	bare := map[string]bool{"c1": true, "c2": true}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSessionManagerWithDir(filepath.Dir(path), filepath.Dir(path)).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	context := session.BuildSessionContext()
	if len(context.Messages) == 0 || context.Messages[0].Custom == nil || !strings.HasSuffix(context.Messages[0].Custom["summary"].(string), "read/written") {
		t.Fatalf("context starts with %+v, not c3's summary", context.Messages)
	}
	entries := session.GetEntries()
	for i, entry := range entries {
		if got := entry.Raw(); !bytes.Equal(got, []byte(lines[i+1])) {
			t.Fatalf("entry %s serves\n%s\nits line is\n%s", entry.Base().ID, got, lines[i+1])
		}
		if entry.Base().Type == "compaction" {
			if kept := holdsItsRecord(entry); kept == bare[entry.Base().ID] {
				t.Fatalf("compaction %s holds its record = %v; it encodes to its line again = %v", entry.Base().ID, kept, bare[entry.Base().ID])
			}
		}
	}
	id, err := session.AppendCompaction("## Goal\n- appended \"<&>\"\n", "a3", 2000, map[string]any{"readFiles": []any{"a.go"}}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	written := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	appended, _ := session.GetEntry(id)
	if last := written[len(written)-1]; !bytes.Equal(appended.Raw(), last) || holdsItsRecord(appended) {
		t.Fatalf("appended compaction serves\n%s\nholds its record %v; the file holds\n%s", appended.Raw(), holdsItsRecord(appended), last)
	}
}
