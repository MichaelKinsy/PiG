package parity

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/internal/upstreammirror"

	"github.com/MichaelKinsy/PiG/coding"
)

// TestUpstreamMirror_MatchesPin fails loudly when the local `.upstream/current`
// mirror does not match the pinned coding.UpstreamVersion and coding.UpstreamCommit.
// Every other parity test reads types.ts from that mirror and trusts it; a
// stale mirror silently validates pig against the wrong upstream version and
// (observed: a 0.79.4 mirror hid that session_info_changed graduated to
// upstream in 0.80.3).
// This guard turns that silent drift into an explicit failure with the fix.
// The ledger generators (autobind -update) run the same check before they write.
func TestUpstreamMirror_MatchesPin(t *testing.T) {
	if err := upstreammirror.Check(testenv.ModuleRoot(t), coding.UpstreamVersion); err != nil {
		t.Fatal(err)
	}
}
