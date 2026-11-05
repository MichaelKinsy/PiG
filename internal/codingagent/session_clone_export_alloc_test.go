//go:build !race

package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// branchSource writes a session of turns exchanges whose questions hold about 16 KiB of escaped text each, and returns it opened as resume
// opens it, with the size of its file.
func branchSource(t *testing.T, turns int) (*SessionManager, *Session, int64) {
	t.Helper()
	dir := t.TempDir()
	live, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range turns {
		text := strings.Repeat(fmt.Sprintf("\t\"line\" %d of turn %d\n", i%97, i), 16<<10/24)
		if _, err := live.AppendMessage(mkUserMsg(text)); err != nil {
			t.Fatal(err)
		}
		if _, err := live.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManagerWithDir(dir, filepath.Join(dir, "clones"))
	s, err := manager.Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	return manager, s, st.Size()
}

// Clone (/fork's createBranchedSession) and the JSONL export write the branch's records with each parentId chained. ForkFromFile, which
// lw-open moved to Canonicalize plus a buffered write, costs 11 B per source byte; a raw splice of parentId into a record that IsCanonical and
// a buffered write cost a few. Clone streams each record from the source to the clone's file and opens that file as a resume does, so it
// costs that open (about the decode of the branch, logged as the floor) and no copy of a record; the export builds the file in a
// strings.Builder and copies it to []byte.
func TestCloneAndExportCostAFewBytesPerSourceByte(t *testing.T) {
	const turns = 300
	const ceiling = 6.0
	// floor logs what reading the branch alone (GetBranch of a second opening of the file) allocates per source byte.
	floor := func(t *testing.T, manager *SessionManager, s *Session, size int64) {
		again, err := manager.Open(s.Path())
		if err != nil {
			t.Fatal(err)
		}
		allocated := lwAllocated(func() { again.GetBranch() })
		t.Logf("floor: GetBranch of the %d-byte branch allocated %d B (%.2f per source byte)", size, allocated, float64(allocated)/float64(size))
	}
	t.Run("clone", func(t *testing.T) {
		manager, s, size := branchSource(t, turns)
		floor(t, manager, s, size)
		leaf := *s.GetLeafID()
		var clone *Session
		var err error
		allocated := lwAllocated(func() { clone, err = manager.Clone(s, leaf) })
		if err != nil || len(clone.GetEntries()) != 2*turns {
			t.Fatalf("Clone: %v", err)
		}
		ratio := float64(allocated) / float64(size)
		t.Logf("Clone of a %d-byte branch allocated %d B (%.2f per source byte)", size, allocated, ratio)
		if ratio > ceiling {
			t.Fatalf("Clone of a %d-byte branch allocated %d B (%.2f per source byte); want at most %.1f: a record is copied or decoded more than the clone's open needs",
				size, allocated, ratio, ceiling)
		}
	})
	t.Run("export", func(t *testing.T) {
		manager, s, size := branchSource(t, turns)
		floor(t, manager, s, size)
		out := filepath.Join(t.TempDir(), "export.jsonl")
		var err error
		allocated := lwAllocated(func() { _, err = ExportSessionToJsonl(s, out, nil) })
		if err != nil {
			t.Fatal(err)
		}
		ratio := float64(allocated) / float64(size)
		t.Logf("export of a %d-byte branch allocated %d B (%.2f per source byte)", size, allocated, ratio)
		if ratio > ceiling {
			t.Fatalf("export of a %d-byte branch allocated %d B (%.2f per source byte); want at most %.1f: every record is rewritten member by member and canonicalized, and the file is built in a strings.Builder and copied",
				size, allocated, ratio, ceiling)
		}
	})
}

// A batch of lines forEachPreparedLine reads ahead is a copy of the lines it holds: it is allocated once, for the batchBytes it ends at and the
// line that crosses them, not grown by doubling. Doubling allocated about 3.5 times the bytes of a batch, and the read starts a new batch for each
// one the index has not returned yet, so a load on more than one worker (every CI runner but a single-CPU one) cost the clone's open that much more.
func TestLineBatchAllocatesOnceForItsLines(t *testing.T) {
	line := []byte(strings.Repeat("x", 16<<10))
	var batch lineBatch
	allocated := lwAllocated(func() {
		for off := int64(0); len(batch.buf) < indexBatchBytes; off++ {
			batch.add(line, off, indexBatchBytes)
		}
	})
	held := len(batch.buf)
	// The lines index (24 B a line) is the only other allocation.
	if limit := uint64(held) * 5 / 4; allocated > limit {
		t.Fatalf("filling a batch of %d bytes allocated %d B (%.2f per byte held); want at most %d", held, allocated, float64(allocated)/float64(held), limit)
	}
}
