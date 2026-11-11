//go:build !race && linux

package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/sessiongen"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// longSession is a generated session of a given size, resumed, and what resuming it and running turns on it cost.
type longSession struct {
	lines int
	// resumeAllocs is the allocations of resuming it; resumeHeap the live heap the resumed Session holds and firstTurnHeap the live heap once a turn has read its context.
	resumeAllocs, resumeHeap, firstTurnHeap float64
	turn                                    perfgate.Work
}

func liveHeapBytes() float64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return float64(stats.HeapAlloc)
}

func measureLongSession(t *testing.T, tokens int) longSession {
	t.Helper()
	path := filepath.Join(t.TempDir(), "long.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sessiongen.Write(file, sessiongen.Options{Tokens: tokens, Seed: 1, ForkEvery: 40})
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	out := longSession{lines: result.Lines}
	out.resumeAllocs = perfgate.Measure(func() {
		if _, err := loadSessionFile(path); err != nil {
			t.Fatal(err)
		}
	}).Allocs
	before := liveHeapBytes()
	session, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out.resumeHeap = liveHeapBytes() - before
	const turns = 5
	work := perfgate.Measure(func() {
		for range turns {
			sessionTurn(t, session, "answer ")
		}
	})
	out.turn = perfgate.Work{Allocs: work.Allocs / turns, Bytes: work.Bytes / turns}
	out.firstTurnHeap = liveHeapBytes() - before
	runtime.KeepAlive(session)
	return out
}

// A session of 1 and of 3 million tokens resumes into an index, and a turn on it costs the log what a turn on a short session does: the records of the
// history are read and decoded by the first turn alone. The allocation of a resume per line is the marginal one between the two sessions, which leaves out
// the cost of decoding the newest entries the resume keeps.
func TestSessionLongGate(t *testing.T) {
	small, large := measureLongSession(t, 1_000_000), measureLongSession(t, 3_000_000)
	t.Logf("perfgate: session.long at 1M tokens (%d lines): resume %.0f allocs, %.0f B live, %.0f B live after a turn; turn %.0f allocs %.0f B", small.lines, small.resumeAllocs, small.resumeHeap, small.firstTurnHeap, small.turn.Allocs, small.turn.Bytes)
	t.Logf("perfgate: session.long at 3M tokens (%d lines): resume %.0f allocs, %.0f B live, %.0f B live after a turn; turn %.0f allocs %.0f B", large.lines, large.resumeAllocs, large.resumeHeap, large.firstTurnHeap, large.turn.Allocs, large.turn.Bytes)
	extraLines := float64(large.lines - small.lines)
	perfgate.Check(t, "session.long.resume_allocs_per_line", (large.resumeAllocs-small.resumeAllocs)/extraLines)
	perfgate.Check(t, "session.long.resume_heap_bytes", large.resumeHeap)
	perfgate.Check(t, "session.long.first_turn_heap_bytes", large.firstTurnHeap)
	perfgate.Check(t, "session.long.turn_allocs", large.turn.Allocs)
	perfgate.Check(t, "session.long.turn_allocs_growth", large.turn.Allocs-small.turn.Allocs)
	perfgate.Check(t, "session.long.turn_bytes_per_existing_entry", (large.turn.Bytes-small.turn.Bytes)/extraLines)
}
