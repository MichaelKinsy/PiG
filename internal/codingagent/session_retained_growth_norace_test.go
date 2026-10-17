//go:build !race

package codingagent

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// soakRun appends exchanges of a tool-calling turn (the user's prompt, an assistant tool call, its result, the answer), with a compaction
// every compactEvery exchanges that keeps the last two, as a long RPC or print session with automatic compaction does. The session has no file.
type soakRun struct {
	s *Session
	// kept is the id of the first message of every exchange so far.
	kept []string
}

func (r *soakRun) append(t testing.TB, from, to int) {
	t.Helper()
	s := r.s
	const compactEvery = 6
	for i := from; i < to; i++ {
		var firstUser string
		messages := append(appendTurnMessages(i), agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1", StopReason: ai.StopReasonStop, Timestamp: int64(i),
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("Summary of file %d.", i)}}}})
		for k, message := range messages {
			id, err := s.AppendMessage(message)
			if err != nil {
				t.Fatal(err)
			}
			if k == 0 {
				firstUser = id
			}
		}
		r.kept = append(r.kept, firstUser)
		if (i+1)%compactEvery == 0 {
			if _, err := s.AppendCompaction("Summary: the earlier turns read files.", r.kept[len(r.kept)-2], 20000, nil, false, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func liveHeap() float64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return float64(stats.HeapAlloc)
}

// A session that is never searched keeps no search text, and each exchange it has compacted away leaves only its index slots, its compressed
// records and the compaction entries behind (what Pi's in-memory SessionManager also keeps, as whole entries).
func TestSessionKeepsNoSearchTextForEntriesNobodySearched(t *testing.T) {
	s := NewSession("soak", "/project")
	(&soakRun{s: s}).append(t, 0, 600)
	if s.searchTexts.size != 0 {
		t.Fatalf("a session nobody searched holds %d bytes of search text for %d slots", s.searchTexts.size, len(s.slots))
	}
	// The first search reads the compacted-away entries' records once and finds them as before.
	_, lt := s.lazyTree()
	lt.searchTexts(treeSearchableText)
	if s.searchTexts.size == 0 {
		t.Fatal("the first search indexed no search text")
	}
	found := 0
	for _, ln := range lt.nodes {
		if strings.Contains(ln.search, "read the file and summarize it") {
			found++
		}
	}
	if found != 600 {
		t.Fatalf("a search for the first prompt matches %d nodes, want 600", found)
	}
}

// stored is the bytes the session's own index holds for its entries: the slots, the cold parts and the compressed record blocks it holds in memory (none once they are spilled), which is
// everything a session that has compacted its entries away and was never searched keeps for them besides its compaction entries and the id
// table. Search text is not counted: an eviction must leave none.
func (s *Session) stored() float64 {
	n := uintptr(len(s.slots))*unsafe.Sizeof(entrySlot{}) + uintptr(s.coldCount)*unsafe.Sizeof(slotCold{})
	for _, block := range s.blockList {
		n += uintptr(len(block.data))
	}
	return float64(n)
}

// Between 2,000 and 8,000 exchanges the live heap grows by what the index of the entries costs and a few hundred bytes more for each
// compaction entry and the id table: an eviction keeps no search text or other derived copy of an entry beyond that.
func TestSessionLiveHeapPerExchange(t *testing.T) {
	run := &soakRun{s: NewSession("soak", "/project")}
	run.append(t, 0, 2000)
	heapBefore, storedBefore := liveHeap(), run.s.stored()
	run.append(t, 2000, 8000)
	heapAfter, storedAfter := liveHeap(), run.s.stored()
	runtime.KeepAlive(run)
	const exchanges = 6000
	grown, indexed := (heapAfter-heapBefore)/exchanges, (storedAfter-storedBefore)/exchanges
	t.Logf("live heap grows %.0f B per exchange (4 entries); the index of the entries is %.0f B of it", grown, indexed)
	const compactionAndIDTable = 500
	if grown > indexed+compactionAndIDTable {
		t.Fatalf("live heap grows %.0f B per exchange, of which the index of the entries explains %.0f; want at most %d more", grown, indexed, compactionAndIDTable)
	}
}

// A session without a file writes the records it compacted away to its spill file and holds none of their bytes, and every entry reads
// back as it was appended.
func TestSessionWithoutAFileSpillsItsCompactedRecords(t *testing.T) {
	s := NewSession("soak", "/project")
	run := &soakRun{s: s}
	run.append(t, 0, 6)
	appended := map[string]string{}
	for _, entry := range s.allEntriesLocked() {
		appended[entry.Base().ID] = string(entry.Raw())
	}
	run.append(t, 6, 600)
	if s.spill == nil || len(s.blockList) == 0 {
		t.Fatalf("spill file %v, %d blocks: the compactions spilled nothing", s.spill, len(s.blockList))
	}
	for i, block := range s.blockList {
		if block.data != nil || block.spill == nil {
			t.Fatalf("block %d holds %d bytes in memory", i, len(block.data))
		}
	}
	read := 0
	for _, entry := range s.allEntriesLocked() {
		if want, ok := appended[entry.Base().ID]; ok {
			read++
			if got := string(entry.Raw()); got != want {
				t.Fatalf("entry %s reads back %s, want %s", entry.Base().ID, got, want)
			}
		}
	}
	if read != len(appended) {
		t.Fatalf("read back %d of the first %d entries", read, len(appended))
	}
}
