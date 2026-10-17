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

// A turn appends a user and an assistant message and builds the session context. Its allocation must not grow with the entries already in the session; the bytes each existing entry adds to a turn are a ratcheted metric whose target is zero.
func TestSessionTurnAllocationGate(t *testing.T) {
	const turns = 20
	text := strings.Repeat("lorem ipsum dolor sit amet ", 15)
	perTurn := func(entries int) perfgate.Work {
		session, err := loadSessionFile(perfSessionFile(t, entries))
		if err != nil {
			t.Fatal(err)
		}
		work := perfgate.Measure(func() {
			for i := range turns {
				if _, err := session.AppendMessage(mkUserMsg("question " + text)); err != nil {
					t.Fatal(err)
				}
				_ = session.BuildSessionContext()
				if _, err := session.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d %s", i, text))); err != nil {
					t.Fatal(err)
				}
			}
		})
		return perfgate.Work{Allocs: work.Allocs / turns, Bytes: work.Bytes / turns}
	}
	small, large := perTurn(500), perTurn(2000)
	t.Logf("perfgate: session.turn per turn: 500 entries: %.0f allocs %.0f B; 2000 entries: %.0f allocs %.0f B", small.Allocs, small.Bytes, large.Allocs, large.Bytes)
	perfgate.Check(t, "session.turn.allocs", small.Allocs)
	perfgate.Check(t, "session.turn.bytes", small.Bytes)
	perfgate.Check(t, "session.turn.allocs_per_existing_entry", (large.Allocs-small.Allocs)/1500)
	perfgate.Check(t, "session.turn.bytes_per_existing_entry", (large.Bytes-small.Bytes)/1500)
}
