package compaction

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// messages.ts:105-136 (createCompactionSummaryMessage, createBranchSummaryMessage, createCustomMessage) read an entry's timestamp with `new Date(timestamp).getTime()`:
// Date.parse's forms, not only RFC 3339 (a space for the T, an RFC 2822 date, a date without a zone, an offset without a colon). A string Date cannot read is NaN,
// null in JSON, and 0 here.
// mutation-checked: RFC 3339 only fails it.
func TestEntryTimestampMsMatchesPi(t *testing.T) {
	inputs := []string{
		"2026-03-05T10:00:00Z", "2026-03-05T10:00:00.123Z", "2026-03-05T10:00:00.123456Z", "2026-03-05T10:00:00+05:30", "2026-03-05T10:00:00-0800", "2026-03-05 10:00:00Z", "2026-03-05 10:00:00",
		"2026-03-05T10:00:00", "2026-03-05", "2026-03", "2026", "Thu, 05 Mar 2026 10:00:00 GMT", "Mar 5, 2026 10:00 PM", "3/5/2026", "1970-01-01T00:00:00.000Z", "0001-01-01T00:00:00Z", "-000001-01-01T00:00:00Z",
		"+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z", "garbage", "", " ", "2026-13-01T00:00:00Z", "2026-02-30T00:00:00Z", "2026-03-05T24:00:00Z", "2026-03-05T25:00:00Z", "1",
	}
	var want []*float64
	pioracle.Run(t, `
const messages = await load("pi-coding-agent/core/messages.js");
emit(input.map((ts) => { const v = messages.createCompactionSummaryMessage("s", 1, ts).timestamp; return Number.isNaN(v) ? null : v; }));`, inputs, &want)
	for i, input := range inputs {
		got := entryTimestampMs(input)
		switch {
		case want[i] == nil && got != 0:
			t.Errorf("entryTimestampMs(%q) = %d, Pi NaN (0 expected)", input, got)
		case want[i] != nil && got != int64(*want[i]):
			t.Errorf("entryTimestampMs(%q) = %d, Pi %v", input, got, *want[i])
		}
	}
}
