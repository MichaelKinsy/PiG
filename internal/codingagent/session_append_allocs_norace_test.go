//go:build !race

package codingagent

import "testing"

// ceilingAppendTurnAllocs is measured at 111 allocations; decoding the tool result's record, whose map details have no proven decoded form,
// costs 141, and parsing each record again for the accounting more.
const ceilingAppendTurnAllocs = 125

// Appending a turn's messages must not parse each record again for the accounting and the gate: one tool-calling turn (user, assistant with a call, 50 KB tool result) is encoded once and decoded once.
func TestAppendingATurnDoesNotParseItsRecordsForAccounting(t *testing.T) {
	session := NewSession("allocs", "/project")
	i := 0
	allocs := testing.AllocsPerRun(50, func() {
		i++
		for _, message := range appendTurnMessages(i) {
			if _, err := session.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Logf("allocations per turn: %.0f", allocs)
	if allocs > ceilingAppendTurnAllocs {
		t.Fatalf("appending one turn allocates %.0f times, want at most %d", allocs, ceilingAppendTurnAllocs)
	}
}
