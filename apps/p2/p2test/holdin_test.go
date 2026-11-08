package p2test_test

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// A reference that stays loading (a worker whose start failed leaves its address failed, and the reference waits through that)
// fails HoldIn once the bound passes, naming the reference's state, where it waited for the test binary's timeout.
func TestHoldInOfAReferenceThatStaysLoadingFailsAtItsBound(t *testing.T) {
	unserved, err := contracts.Local("p2.test.unserved", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("x", contracts.NewMethod())}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{})
	was := p2test.HoldLiveTimeout
	p2test.HoldLiveTimeout = 300 * time.Millisecond
	defer func() { p2test.HoldLiveTimeout = was }()
	start := time.Now()
	_, err = p2test.HoldIn(fixture.Host, unserved, delta.NewJsonObject(0), "x")
	if err == nil || !strings.Contains(err.Error(), "was not live within") {
		t.Fatalf("HoldIn = %v, want the bound's error", err)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatalf("HoldIn took %v", time.Since(start))
	}
}
