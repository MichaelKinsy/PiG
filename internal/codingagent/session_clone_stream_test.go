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
		if source.resident.get(i) != nil {
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

// blockBackedSource is a session of 150 exchanges with a compaction every sixth, as a long session without a file holds it: the entries
// before the compaction's kept range are held as compressed records in blocks (packLocked), and no entry of them is resident. appended is
// each entry's record as it was appended, by id.
func blockBackedSource(t *testing.T) (source *Session, leaf string, appended map[string]string) {
	t.Helper()
	source = NewSession("block-backed", "/work/p")
	appended = map[string]string{}
	var users []string
	record := func(id string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		entry, ok := source.GetEntry(id)
		if !ok {
			t.Fatalf("entry %s was not appended", id)
		}
		appended[id] = string(entry.Raw())
		return id
	}
	for i := range 150 {
		users = append(users, record(source.AppendMessage(mkUserMsg(fmt.Sprintf("question %d %s", i, strings.Repeat("y", 200))))))
		leaf = record(source.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))))
		if (i+1)%6 == 0 {
			leaf = record(source.AppendCompaction("summary", users[len(users)-2], 1000, nil, false, nil))
		}
	}
	return source, leaf, appended
}

// A clone of a branch whose older entries are only in compressed blocks reads each record from its block and writes the records the source
// appended: the chain is linear, so the rewrite of each parentId changes no byte. Both a source without a file (the clone stays in memory)
// and one that names a file (the clone is written and opened) are cloned.
func TestCloneReadsRecordsFromCompressedBlocks(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted=%v", persisted), func(t *testing.T) {
			source, leaf, appended := blockBackedSource(t)
			inBlocks := 0
			for i := range source.slots {
				if source.resident.get(i) == nil && source.coldPart(&source.slots[i]).blockOf() != 0 {
					inBlocks++
				}
			}
			if inBlocks < 200 || len(source.blockList) == 0 {
				t.Fatalf("%d entries are in %d compressed blocks, want the older entries packed", inBlocks, len(source.blockList))
			}
			dir := t.TempDir()
			if persisted {
				source.path = filepath.Join(dir, "source.jsonl")
			}
			clone, err := NewSessionManagerWithDir("/work/p", filepath.Join(dir, "clones")).Clone(source, leaf)
			if err != nil {
				t.Fatal(err)
			}
			branch := clone.GetBranch()
			if len(branch) != len(appended) {
				t.Fatalf("clone has %d entries, want %d", len(branch), len(appended))
			}
			for _, entry := range branch {
				if got, want := string(entry.Raw()), appended[entry.Base().ID]; got != want {
					t.Fatalf("clone entry %s is\n%s\nthe source appended\n%s", entry.Base().ID, got, want)
				}
			}
			if persisted != (clone.Path() != "") {
				t.Fatalf("clone path %q for a source with persisted=%v", clone.Path(), persisted)
			}
		})
	}
}

// A clone that fails after it has written records leaves its file as written so far, as Pi's does: Pi opens the file and writes entry by entry
// (session-manager.ts _rewriteFile), and nothing there removes it. A label entry whose targetId is not a string, which Clone decodes after the
// path, fails it.
func TestCloneKeepsTheFileItWroteWhenItFails(t *testing.T) {
	dir := t.TempDir()
	message := func(id, parent, role string) string {
		return fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"%s","content":"text of %s","timestamp":1}}`, id, parent, role, id)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"source","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work/p"}`,
		message("u1", "null", "user"), message("a1", `"u1"`, "assistant"),
		`{"type":"label","id":"lb","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","targetId":5,"label":"bad"}`,
		message("u2", `"lb"`, "user"), message("a2", `"u2"`, "assistant"),
	}
	sourcePath := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clones := filepath.Join(dir, "clones")
	manager := NewSessionManagerWithDir("/work/p", clones)
	source, err := manager.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Clone(source, "a2"); err == nil {
		t.Fatal("Clone of a branch with a label whose targetId is a number succeeded, want the decode error")
	}
	files, err := filepath.Glob(filepath.Join(clones, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("clone directory holds %v (%v), want the one file the failed clone wrote", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	written := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(written) != 5 || !strings.HasPrefix(written[0], `{"type":"session"`) || !strings.Contains(written[4], `"id":"a2"`) {
		t.Fatalf("the failed clone's file holds %d records, want its header and the four entries of the path:\n%s", len(written), data)
	}
}
