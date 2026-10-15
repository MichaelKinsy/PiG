package compaction

import "testing"

// compaction.ts findTurnStartIndex(entries, entryIndex, startIndex) walks back from entryIndex to startIndex for the closest context-visible user-role entry, and returns -1 when none lies in that span.
func TestFindTurnStartIndexWalksBackToTheClosestUserEntry(t *testing.T) {
	b := upstreamCompactionBuilder{t: t}
	b.user("turn 1")                          // 0
	b.assistant("a1", piUsage(0, 10, 100, 0)) // 1
	b.user("turn 2")                          // 2
	b.assistant("a2", piUsage(0, 10, 200, 0)) // 3
	b.assistant("a3", piUsage(0, 10, 300, 0)) // 4
	for _, tc := range []struct {
		name                   string
		entryIndex, startIndex int
		want                   int
	}{
		{"inside the second turn", 4, 0, 2},
		{"on the user entry itself", 2, 0, 2},
		{"first turn", 1, 0, 0},
		{"start index excludes the turn start", 4, 3, -1},
		{"start index includes the turn start", 4, 2, 2},
		{"empty span", 0, 1, -1},
	} {
		if got := FindTurnStartIndex(b.entries, tc.entryIndex, tc.startIndex); got != tc.want {
			t.Errorf("%s: FindTurnStartIndex(%d, %d) = %d, want %d", tc.name, tc.entryIndex, tc.startIndex, got, tc.want)
		}
	}
}
