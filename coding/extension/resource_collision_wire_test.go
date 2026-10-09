package extension

import (
	"encoding/json"
	"testing"
)

// packages/coding-agent/src/core/diagnostics.ts:6 winnerSource and diagnostics.ts:7 loserSource: ResourceCollision's sources are optional (for example "npm:foo" or "local"), present on the wire only when set.
func TestResourceCollisionSourcesAreOptionalOnTheWire(t *testing.T) {
	for _, tc := range []struct {
		name, wire, winner, loser string
	}{
		{"without sources", `{"resourceType":"skill","name":"n","winnerPath":"/a","loserPath":"/b"}`, "", ""},
		{"with sources", `{"resourceType":"skill","name":"n","winnerPath":"/a","loserPath":"/b","winnerSource":"npm:foo","loserSource":"local"}`, "npm:foo", "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var collision ResourceCollision
			if err := json.Unmarshal([]byte(tc.wire), &collision); err != nil {
				t.Fatal(err)
			}
			if collision.WinnerSource != tc.winner || collision.LoserSource != tc.loser {
				t.Errorf("sources = %q/%q, want %q/%q", collision.WinnerSource, collision.LoserSource, tc.winner, tc.loser)
			}
			got, err := json.Marshal(collision)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.wire {
				t.Errorf("round trip\n got %s\nwant %s", got, tc.wire)
			}
		})
	}
}
