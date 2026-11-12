//go:build !race && linux

package codingagent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// perfSessionFile writes a persisted session of entries messages (alternating user and assistant, about 400 bytes of text each) and returns its path.
func perfSessionFile(t *testing.T, entries int) string {
	t.Helper()
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("lorem ipsum dolor sit amet ", 15)
	for i := range entries {
		message := mkUserMsg(fmt.Sprintf("question %d: %s", i, text))
		if i%2 == 1 {
			message = mkAssistantMsg(fmt.Sprintf("answer %d: %s", i, text))
		}
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	return session.Path()
}

// Loading a session costs a constant allocation per JSONL line, and the bytes each line retains after the load are a ratcheted metric: a load that keeps every line resident holds the whole file.
func TestSessionLoadPerLineAllocationGate(t *testing.T) {
	const n = 1000
	files := map[int]string{}
	for _, entries := range []int{n, 2 * n, 4 * n} {
		files[entries] = perfSessionFile(t, entries)
	}
	load := func(entries int) {
		if _, err := loadSessionFile(files[entries]); err != nil {
			t.Fatal(err)
		}
	}
	marginal := perfgate.CheckLinear(t, "session.load_line", n, 1.5, load)
	perfgate.Check(t, "session.load_line.allocs", marginal.Allocs)
	perfgate.Check(t, "session.load_line.bytes", marginal.Bytes)

	retained := func(entries int) float64 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		session, err := loadSessionFile(files[entries])
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(session)
		return float64(after.HeapAlloc) - float64(before.HeapAlloc)
	}
	perLine := (retained(4*n) - retained(2*n)) / float64(2*n)
	perfgate.Check(t, "session.load_line.retained_bytes", perLine)
}

// sessionTurn is one turn of a persisted Session as coding.Session drives it: the user's message and the assistant's are appended, and the log is read the
// way a turn reads it, the branch for the compaction decision and the projection for the request, before the model answers and after it.
func sessionTurn(tb testing.TB, session *Session, answer string) {
	tb.Helper()
	text := strings.Repeat("lorem ipsum dolor sit amet ", 15)
	if _, err := session.AppendMessage(mkUserMsg("question " + text)); err != nil {
		tb.Fatal(err)
	}
	_ = session.GetContextBranch()
	_ = session.BuildSessionProjectionView()
	if _, err := session.AppendMessage(mkAssistantMsg(answer + text)); err != nil {
		tb.Fatal(err)
	}
	_ = session.GetContextBranch()
	_ = session.BuildSessionProjectionView()
}

// A turn costs the log the same whatever the session's length: a session of 50 turns (200 entries) and one of 3,500 turns (14,000) allocate alike. The
// bytes each existing entry adds to a turn are a ratcheted metric whose target is zero; what remains is the list of entries GetContextBranch hands its
// caller, one interface value for each.
func TestSessionTurnAllocationGate(t *testing.T) {
	const turns = 20
	perTurn := func(entries int) perfgate.Work {
		session, err := loadSessionFile(perfSessionFile(t, entries))
		if err != nil {
			t.Fatal(err)
		}
		work := perfgate.Measure(func() {
			for i := range turns {
				sessionTurn(t, session, fmt.Sprintf("answer %d ", i))
			}
		})
		return perfgate.Work{Allocs: work.Allocs / turns, Bytes: work.Bytes / turns}
	}
	const small, large = 200, 14000
	atSmall, atLarge := perTurn(small), perTurn(large)
	t.Logf("perfgate: session.turn per turn: %d entries: %.0f allocs %.0f B; %d entries: %.0f allocs %.0f B", small, atSmall.Allocs, atSmall.Bytes, large, atLarge.Allocs, atLarge.Bytes)
	perfgate.Check(t, "session.turn.allocs", atSmall.Allocs)
	perfgate.Check(t, "session.turn.bytes", atSmall.Bytes)
	perfgate.Check(t, "session.turn.allocs_per_existing_entry", (atLarge.Allocs-atSmall.Allocs)/(large-small))
	perfgate.Check(t, "session.turn.bytes_per_existing_entry", (atLarge.Bytes-atSmall.Bytes)/(large-small))
}

// BuildSessionContext hands its caller messages it owns, so building it copies every message of the context: its cost per existing entry is a ratcheted
// metric that no cache can remove. A turn does not build it (sessionTurn reads the projection the Session keeps).
func TestSessionContextCopyAllocationGate(t *testing.T) {
	cost := func(entries int) perfgate.Work {
		session, err := loadSessionFile(perfSessionFile(t, entries))
		if err != nil {
			t.Fatal(err)
		}
		return perfgate.Measure(func() { _ = session.BuildSessionContext() })
	}
	small, large := cost(500), cost(2000)
	perfgate.Check(t, "session.context_copy.allocs_per_entry", (large.Allocs-small.Allocs)/1500)
	perfgate.Check(t, "session.context_copy.bytes_per_entry", (large.Bytes-small.Bytes)/1500)
}
