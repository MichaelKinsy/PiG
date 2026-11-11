package radius

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/radius/contract"
)

// Ports the status-lines case of test/p2/radius.test.ts: Pi Mobile's lines, retries with their error.
func TestRadiusStatusLinesAsPiMobileLogsThem(t *testing.T) {
	for _, each := range []struct {
		status contract.Status
		want   string
	}{
		// Pi's test at 5c37d3be1 expects `connected as brain "Studio" (brain-1)` for this input, but Pi's own
		// radiusStatusText returns `connected as brain Studio` (the two disagree upstream): the port follows the code.
		{contract.Status{Kind: contract.StatusConnected, ServerID: "brain-1", OrganizationID: "org", Name: "Studio"}, "connected as brain Studio"},
		{contract.Status{Kind: contract.StatusConnected, ServerID: "brain-1", OrganizationID: "org"}, "connected as brain brain-1"},
		{contract.Status{Kind: contract.StatusRetrying, Error: "offline"}, "not connected (offline)"},
		{contract.Status{Kind: contract.StatusConnecting}, ""},
		{contract.Status{Kind: contract.StatusSignedOut}, ""},
	} {
		if got := StatusText(each.status); got != each.want {
			t.Errorf("StatusText(%v) = %q, want %q", each.status, got, each.want)
		}
	}
}
