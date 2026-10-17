//go:build parity

package journeys

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func TestSelectPicksJourneysByTouchedPrefix(t *testing.T) {
	js := []*harness.Journey{
		{Name: "a", Touches: []string{"coding/compaction", "tui/"}},
		{Name: "b", Touches: []string{"ai/"}},
		{Name: "c", Touches: []string{"internal/codingagent/"}},
	}
	for _, tc := range []struct {
		name    string
		changed []string
		want    []string
	}{
		{"nothing", nil, nil},
		{"one prefix", []string{"coding/compaction/x.go"}, []string{"a"}},
		{"two journeys share a prefix and one does not", []string{"tui/editor.go", "coding/compaction/x.go"}, []string{"a"}},
		{"provider requests are compared by every journey", []string{"ai/openai.go"}, []string{"a", "b", "c"}},
		{"session files are compared by every journey", []string{"internal/sessionentry/entry.go"}, []string{"a", "b", "c"}},
		{"documentation selects nothing", []string{"docs/a.md", "README.md", "changelog.d/x.md", "tui/README.md"}, nil},
		{"program entry selects all", []string{"cmd/pig/main.go"}, []string{"a", "b", "c"}},
		{"journey infrastructure selects all", []string{"test/journeys/harness/run.go"}, []string{"a", "b", "c"}},
		{"outside every prefix", []string{"extensions/sdk/x.go"}, nil},
	} {
		if got := harness.Select(js, tc.changed); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
