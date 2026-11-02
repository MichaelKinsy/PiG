package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cloneLines is the records of a clone's file after its header, with the ids of the label entries Clone makes (random) renamed L1, L2, ... in file order.
func cloneLines(t *testing.T, clone *Session) string {
	t.Helper()
	data, err := os.ReadFile(clone.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")[1:]
	var labelIDs []string
	for _, line := range lines {
		if after, ok := strings.CutPrefix(line, `{"type":"label","id":"`); ok {
			labelIDs = append(labelIDs, strings.SplitN(after, `"`, 2)[0])
		}
	}
	joined := strings.Join(lines, "\n")
	for n, id := range labelIDs {
		joined = strings.ReplaceAll(joined, `"`+id+`"`, fmt.Sprintf(`"L%d"`, n+1))
	}
	return joined
}

// A session with labels (one cleared, one a compaction's firstKeptEntryId), a compaction, a model change and custom entries is cloned from
// the live Session, whose entries are resident, and from the Session a resume opens, whose older entries are read back from their lines.
// The clone's file is the same bytes from both, and the clone's entries are the file's.
func TestCloneWritesTheSameFileFromResidentAndFileBackedSources(t *testing.T) {
	dir := t.TempDir()
	manager := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions"))
	live, err := manager.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	must := func(id string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := must(live.AppendMessage(mkUserMsg("first \"quoted\" <&>")))
	must(live.AppendMessage(mkAssistantMsg("answer")))
	must(live.AppendLabelChange(first, new("start")))
	must(live.AppendModelChange("faux", "faux-2"))
	second := must(live.AppendMessage(mkUserMsg("second")))
	cleared := must(live.AppendMessage(mkAssistantMsg("to be unlabeled")))
	must(live.AppendLabelChange(cleared, new("temporary")))
	must(live.AppendLabelChange(cleared, nil))
	kept := must(live.AppendLabelChange(second, new("kept")))
	must(live.AppendCustomMessageEntry("note", "custom text", true, map[string]any{"b": 1.0, "a": 2.0}))
	must(live.AppendCompaction("summary", kept, 100, nil, false, nil))
	must(live.AppendMessage(mkUserMsg("after the compaction")))
	leaf := must(live.AppendMessage(mkAssistantMsg("last")))

	fromLive, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "live-clones")).Clone(live, leaf)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := manager.Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "file-clones")).Clone(opened, leaf)
	if err != nil {
		t.Fatal(err)
	}
	want, have := cloneLines(t, fromLive), cloneLines(t, fromFile)
	if want != have {
		t.Fatalf("clone from the file-backed source:\n%s\nfrom the live source:\n%s", have, want)
	}
	for _, clone := range []*Session{fromLive, fromFile} {
		if !clone.flushed || clone.Path() == "" || clone.GetHeader().ParentSession != live.Path() {
			t.Fatalf("clone flushed=%v path=%q parentSession=%q, want a written file naming its source", clone.flushed, clone.Path(), clone.GetHeader().ParentSession)
		}
		if got := len(clone.GetEntries()); got != strings.Count(want, "\n")+1 {
			t.Fatalf("clone has %d entries, its file %d records", got, strings.Count(want, "\n")+1)
		}
	}
	// "start" labels the first message, "kept" the compaction's first kept entry: both labels move to the end as new entries; the cleared one is gone.
	if got := strings.Count(want, `"type":"label"`); got != 2 {
		t.Fatalf("clone holds %d label entries, want 2:\n%s", got, want)
	}
}

// Clone streams the records of the branch from the source's file to the clone's, so a source entry that is not resident is never decoded: a
// branch of any length costs the source no entry read.
func TestCloneReadsNoSourceEntryBackToDecodeIt(t *testing.T) {
	dir := t.TempDir()
	manager := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions"))
	live, err := manager.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	var keep, leaf string
	for i := range 300 {
		id, err := live.AppendMessage(mkUserMsg(fmt.Sprintf("question %d %s", i, strings.Repeat("x", 512))))
		if err != nil {
			t.Fatal(err)
		}
		if i == 290 {
			keep = id
		}
		if leaf, err = live.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := live.AppendCompaction("summary", keep, 100, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	source, err := manager.Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	resident := 0
	for i := range source.slots {
		if source.slots[i].entry != nil {
			resident++
		}
	}
	if resident > len(source.slots)/4 {
		t.Fatalf("%d of %d source entries are resident after the open, want the older ones read from their lines", resident, len(source.slots))
	}
	before := source.EntryReads()
	clone, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "clones")).Clone(source, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if reads := source.EntryReads() - before; reads != 0 {
		t.Fatalf("Clone read back and decoded %d source entries, want none", reads)
	}
	if got := len(clone.GetEntries()); got != 600 {
		t.Fatalf("clone has %d entries, want 600", got)
	}
}
