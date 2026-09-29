package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func TestFindLimitWarningsPreserveNumbers(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/tools/renderers/find.ts:55-60 preserves the numeric limit and tests its JS truthiness.
	for _, tc := range []struct {
		limit float64
		want  []string
	}{{0, nil}, {1.5, []string{"1.5 results limit"}}, {200, []string{"200 results limit"}}} {
		for _, details := range []any{&tools.FindDetails{ResultLimitReached: new(tc.limit)}, map[string]any{"resultLimitReached": tc.limit}} {
			if got := listToolWarnings("find", listDetailsFrom(details)); !slices.Equal(got, tc.want) {
				t.Fatalf("limit %v via %T: %q, want %q", tc.limit, details, got, tc.want)
			}
		}
	}
}
