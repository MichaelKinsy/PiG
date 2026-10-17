//go:build !race

package codingagent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// compactionHistory writes a session of the given number of compactions, each after a few exchanges and with a summary of summaryBytes
// markdown, and returns its path and the summary bytes it holds.
func compactionHistory(t *testing.T, compactions, summaryBytes int) (string, int) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for c := range compactions {
		var kept string
		for i := range 4 {
			if kept, err = s.AppendMessage(mkUserMsg(fmt.Sprintf("question %d.%d", c, i))); err != nil {
				t.Fatal(err)
			}
			if _, err = s.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d.%d", c, i))); err != nil {
				t.Fatal(err)
			}
		}
		line := fmt.Sprintf("- compaction %d: read `f%d.go`, \"fixed\" the parser\n", c, c)
		summary := "## Goal\n" + strings.Repeat(line, summaryBytes/len(line))
		total += len(summary)
		if _, err = s.AppendCompaction(summary, kept, 150000, nil, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	return s.Path(), total
}

func compactionHeapLive() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// retainedAfterOpen opens path as resume does and returns the heap the session retains with its context.
func retainedAfterOpen(t *testing.T, path string) uint64 {
	t.Helper()
	before := compactionHeapLive()
	s, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(path)).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	context := s.BuildSessionContext()
	if len(context.Messages) == 0 {
		t.Fatal("empty context")
	}
	after := compactionHeapLive()
	runtime.KeepAlive(context)
	runtime.KeepAlive(s)
	return after - before
}

// An opened session keeps every compaction entry resident (they are not evictable, and contextPathIn reads every non-message entry before
// the kept range), and each keeps its record beside its decode (DecodeSessionEntry of a bytes.Clone of the line, session_file.go
// prepareLine): the summary twice, once JSON-escaped. On the P1 fixture that is 27 MB of the 41.6 MB an open retains. Pi holds each entry
// decoded once; a resident compaction whose line is in the file can drop its record (read back with the hash check) or keep the record and
// decode on use.
func TestOpenedSessionHoldsEachCompactionSummaryOnce(t *testing.T) {
	const compactions = 40
	smallPath, small := compactionHistory(t, compactions, 64<<10)
	largePath, large := compactionHistory(t, compactions, 128<<10)
	retainedSmall := retainedAfterOpen(t, smallPath)
	retainedLarge := retainedAfterOpen(t, largePath)
	slope := (float64(retainedLarge) - float64(retainedSmall)) / float64(large-small)
	const ceiling = 1.3
	t.Logf("%d compactions: %d summary bytes retain %d B, %d retain %d B: %.2f B per summary byte", compactions, small, retainedSmall, large,
		retainedLarge, slope)
	if slope > ceiling {
		t.Fatalf("an opened session retains %.2f B per compaction summary byte (%d B for %d, %d B for %d); want at most %.2f: each resident compaction entry keeps its record beside its decoded summary",
			slope, retainedSmall, small, retainedLarge, large, ceiling)
	}
}

// Projecting the context reads the latest compaction's kept entry and summary from the decoded entry: a compaction held without its record
// would otherwise be encoded, and its summary decoded again, for every projection.
func TestProjectingAResumedContextDoesNotEncodeItsCompaction(t *testing.T) {
	const summaryBytes = 128 << 10
	path, _ := compactionHistory(t, 4, summaryBytes)
	s, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(path)).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.BuildSessionContext()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	view := freshProjectionView(s)
	runtime.ReadMemStats(&after)
	if len(view.Messages) == 0 {
		t.Fatal("empty projection")
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("a projection of a context after a %d B summary allocates %d B", summaryBytes, allocated)
	if allocated > summaryBytes/2 {
		t.Fatalf("a projection allocates %d B for a context whose compaction summary is %d B: it encodes the compaction to read it", allocated, summaryBytes)
	}
}
