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

// Clone (/fork's createBranchedSession) and the JSONL export write the branch's records with each parentId chained. Both rewrite every
// record through rewriteMemberJSONField (a Decoder token per member, marshalJSONLine per key, json.Compact per value) and Canonicalize,
// and Clone then decodes every rewritten record again (DecodeFileEntry) and writes the lines with one Fprintf each; the export builds the
// file in a strings.Builder and copies it to []byte. ForkFromFile, which lw-open moved to Canonicalize plus a buffered write, costs
// 11 B per source byte; a raw splice of parentId into a record that IsCanonical, entries built from the decoded branch and a buffered
// write cost a few: the decode of the branch (logged as the floor) and one line per record.
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
			t.Fatalf("Clone of a %d-byte branch allocated %d B (%.2f per source byte); want at most %.1f: every record is rewritten member by member, canonicalized and decoded again",
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
