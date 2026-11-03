package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts shouldShowStartupHeader and shouldShowStartupDetails (1501-1531), which init()
// (974-1152) reads: verbose forces both, otherwise quietStartup true hides the header and quietStartup true or "header" hides the details.

import "testing"

func TestStartupHeaderAndDetailsVisibilityFollowVerboseAndQuietStartup(t *testing.T) {
	for _, tc := range []struct {
		quiet          QuietStartup
		verbose        bool
		header, detail bool
	}{
		{QuietStartupFalse, false, true, true},
		{QuietStartupFalse, true, true, true},
		{QuietStartupHeader, false, true, false},
		{QuietStartupHeader, true, true, true},
		{QuietStartupTrue, false, false, false},
		{QuietStartupTrue, true, true, true},
	} {
		m := &InteractiveMode{opts: InteractiveModeOptions{Verbose: tc.verbose, Settings: Settings{QuietStartup: tc.quiet}}}
		if got := m.shouldShowStartupHeader(); got != tc.header {
			t.Errorf("quiet=%v verbose=%v: header=%v, want %v", tc.quiet, tc.verbose, got, tc.header)
		}
		if got := m.shouldShowStartupDetails(); got != tc.detail {
			t.Errorf("quiet=%v verbose=%v: details=%v, want %v", tc.quiet, tc.verbose, got, tc.detail)
		}
	}
}
