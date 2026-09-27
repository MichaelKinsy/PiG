package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi 0.87.1 tools/renderers/grep.ts and ls.ts interpolate the requested limit without integer rounding, both live and after session replay.
func TestListToolFractionalLimitWarnings(t *testing.T) {
	for _, tc := range []struct {
		name string
		live any
		wire map[string]any
		want string
	}{
		{"grep", &tools.GrepDetails{MatchLimitReached: 1.5}, map[string]any{"matchLimitReached": 1.5}, "1.5 matches limit"},
		{"ls", &tools.LsDetails{EntryLimitReached: 1.5}, map[string]any{"entryLimitReached": 1.5}, "1.5 entries limit"},
	} {
		for _, value := range []any{tc.live, tc.wire} {
			if got := listToolWarnings(tc.name, listDetailsFrom(value)); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("%s warnings=%v", tc.name, got)
			}
		}
	}
}
