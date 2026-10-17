//go:build !race

// The race detector instruments allocations, so these allocation ratchets run without it, as the repository's other
// allocation ratchets do.

package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// QUALITY-AUDIT-050 item 12: BuildContext spent three allocations per entry in GetSessionContextSettings alone, on entries
// whose settings it never decoded. The whole build was 8 allocations per message (24,038 for 3,000); it is 5 after the fix.
func TestBuildContextAllocatesPerMessageWithinBudget(t *testing.T) {
	const entries = 600
	s, leaf := buildLinearSession(t, entries)
	allocs := testing.AllocsPerRun(5, func() {
		if got := s.BuildContext(&leaf); len(got) != entries {
			t.Fatalf("BuildContext len = %d, want %d", len(got), entries)
		}
	})
	if perMessage := allocs / entries; perMessage > 6.5 {
		t.Fatalf("BuildContext allocates %.2f times per message (%.0f for %d), budget 6.5", perMessage, allocs, entries)
	}
}

// A thinking level is never found in a session that never changed it, so the scan walks the whole path (Pi's loop does the
// same). Entries it skips must cost nothing: the allocations of a scan may not grow with the path.
func TestGetSessionContextSettingsSkipsEntriesWithoutAllocating(t *testing.T) {
	decode := func(raw string) SessionEntry { return sessionentry.DecodeSessionEntry(json.RawMessage(raw)) }
	path := func(skipped int) []SessionEntry {
		entries := make([]SessionEntry, 0, skipped+1)
		for range skipped {
			entries = append(entries, decode(`{"type":"message","message":{"role":"user","content":"hello"}}`), decode(`{"type":"label","targetId":"x"}`))
		}
		return append(entries, decode(`{"type":"message","message":{"role":"assistant","provider":"p","model":"m","content":[]}}`))
	}
	measure := func(skipped int) float64 {
		entries := path(skipped)
		return testing.AllocsPerRun(20, func() {
			if _, model := GetSessionContextSettings(entries); model == nil || model.ModelID != "m" {
				t.Fatalf("model = %+v", model)
			}
		})
	}
	short, long := measure(2), measure(1000)
	if long > short {
		t.Fatalf("a scan over 2,001 entries allocates %v times, over 5 entries %v: skipped entries must cost nothing", long, short)
	}
}
