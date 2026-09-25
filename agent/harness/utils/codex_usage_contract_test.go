package utils

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// addUsage in harness/utils/usage.ts preserves absent fields and allocates a
// fresh result, including when only one operand has an optional counter.
func TestUsageOptionalPresenceAndOwnership(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name              string
		left, right, want *int
	}{
		{"absent", nil, nil, nil},
		{"explicit zero", nil, new(0), new(0)},
		{"left", new(7), nil, new(7)},
		{"right", nil, new(11), new(11)},
		{"both", new(7), new(11), new(18)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum := AddUsage(ai.Usage{CacheWrite1h: tc.left, Reasoning: tc.left}, ai.Usage{CacheWrite1h: tc.right, Reasoning: tc.right})
			if !reflect.DeepEqual(sum.CacheWrite1h, tc.want) || !reflect.DeepEqual(sum.Reasoning, tc.want) {
				t.Fatalf("optional counters = %v, %v; want %v", sum.CacheWrite1h, sum.Reasoning, tc.want)
			}
			if sum.CacheWrite1h != nil {
				*sum.CacheWrite1h = 99
				if !reflect.DeepEqual(sum.Reasoning, tc.want) || sum.CacheWrite1h == tc.left || sum.CacheWrite1h == tc.right {
					t.Fatal("result counters alias one another or an operand")
				}
			}
		})
	}
}
