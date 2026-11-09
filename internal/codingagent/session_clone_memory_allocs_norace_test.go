//go:build !race

package codingagent

import (
	"fmt"
	"testing"
)

// An in-memory session's clone builds its entries from the source's decoded messages with the parent rebased, as before Clone streamed its records (761f5a208): decoding every
// record again doubled the cost, BenchmarkCloneLabeledSession going from 26211 to 57436 allocs/op in the allocation baseline.
func TestCloneOfAnInMemorySessionReusesItsDecodedMessages(t *testing.T) {
	const messages = 1000
	source := NewSession("alloc", "/project")
	var leaf string
	for i := range messages {
		id, err := source.AppendMessage(mkUserMsg(fmt.Sprintf("message %d", i)))
		if err != nil {
			t.Fatal(err)
		}
		leaf = id
	}
	manager := NewSessionManagerWithDir("/project", t.TempDir())
	var clone *Session
	allocs := testing.AllocsPerRun(5, func() {
		var err error
		if clone, err = manager.Clone(source, leaf); err != nil {
			t.Fatal(err)
		}
	})
	if got := len(clone.GetEntries()); got != messages {
		t.Fatalf("clone has %d entries, want %d", got, messages)
	}
	// About 27 allocations per message with the entries reused; 52 when each record is decoded again.
	if limit := float64(messages * 32); allocs > limit {
		t.Fatalf("Clone of %d in-memory messages allocated %.0f times, want at most %.0f: the records are decoded again", messages, allocs, limit)
	}
}
