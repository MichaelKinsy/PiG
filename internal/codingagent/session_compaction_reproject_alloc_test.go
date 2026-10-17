//go:build !race

package codingagent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// lwAllocated returns the bytes the heap allocated while f ran.
func lwAllocated(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// appendCompactionAllocated writes a session of turns exchanges and resumes it, so only the context's tail is resident (as after any
// compaction or leaf move; a live session evicts on its first compaction). It projects the context as the agent does before every
// request (BuildSessionProjectionView) and returns the bytes the AppendCompaction that follows allocates.
func appendCompactionAllocated(t *testing.T, turns int) uint64 {
	t.Helper()
	dir := t.TempDir()
	live, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	var lastUser string
	for i := range turns {
		if lastUser, err = live.AppendMessage(mkUserMsg(strings.Repeat(fmt.Sprintf("line %d of turn %d\n", i%97, i), 64))); err != nil {
			t.Fatal(err)
		}
		if _, err = live.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewSessionManagerWithDir(dir, filepath.Dir(live.Path())).Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s.BuildSessionProjectionView().Messages); got != 2*turns {
		t.Fatalf("context holds %d messages, want %d", got, 2*turns)
	}
	var id string
	allocated := lwAllocated(func() {
		id, err = s.AppendCompaction("summary of the conversation", lastUser, 100000, nil, false, nil)
	})
	if err != nil || id == "" {
		t.Fatalf("AppendCompaction = %q, %v", id, err)
	}
	return allocated
}

// AppendCompaction reads the current system message from the context it is about to compact. It projects the whole context again
// (BuildSessionProjection: a copy of every message, and a read and decode of every message no longer resident) only to find the system
// messages in it, though the agent has just projected that context and keeps the projection (BuildSessionProjectionView). The
// compaction's own work (the entry, and the eviction of the resident tail) does not grow with the context; the re-projection does.
func TestAppendCompactionDoesNotReprojectTheContext(t *testing.T) {
	const small, large = 1000, 4000
	allocSmall := appendCompactionAllocated(t, small)
	allocLarge := appendCompactionAllocated(t, large)
	slope := (float64(allocLarge) - float64(allocSmall)) / float64(2*(large-small))
	const ceiling = 64.0
	t.Logf("AppendCompaction allocated %d B over %d context messages and %d B over %d: %.1f B per context message", allocSmall, 2*small,
		allocLarge, 2*large, slope)
	if slope > ceiling {
		t.Fatalf("AppendCompaction allocates %.1f B per context message (%d B over %d messages, %d B over %d); want at most %.0f: it projects the context again (BuildSessionProjection) to find its system message",
			slope, allocSmall, 2*small, allocLarge, 2*large, ceiling)
	}
}
