package surfaces

import (
	"testing"
	"time"
	_ "time/tzdata" // the zones below are loaded from Go's own data wherever the test runs
)

// D465, the zone-data difference: for a zone that ICU links to another, the clock of an instant before 1970 differs between Go and Node.
// Go keeps the zone's own tzdata history (Europe/Amsterdam had its own offset, +0:19:32 "AMT", until 1937); Node prints the target
// zone's clock, so Amsterdam reads like Brussels. The test pins the difference at 1930-01-01T12:00Z against the live Node run under
// the same `TZ`, with Brussels as the control that both sides print alike. If either side's data changes so that the two agree, or
// the control stops agreeing, the test fails and the record's text has to follow.
func TestFormatTimeInAmsterdamBefore1937KeepsTzdataHistoryWhereNodePrintsBrusselsClock(t *testing.T) {
	node := driftNode(t)
	platform(t, nil, nil, nil)
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	at := time.Date(1930, time.January, 1, 12, 0, 0, 0, time.UTC)
	locale := "en-GB"
	shape := map[string]any{"hour": "2-digit", "minute": "2-digit", "second": "2-digit"}
	options := &TimeOptions{Hour: FieldTwoDigit, Minute: FieldTwoDigit, Second: FieldTwoDigit}
	clock := func(zone string) (goClock, nodeClock string) {
		t.Helper()
		location, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		time.Local = location
		t.Setenv("TZ", zone)
		goClock, err = FormatTime(&Language{Locale: locale}, at, options)
		if err != nil {
			t.Fatal(err)
		}
		results := runNode(t, node, []driftCase{{Kind: "time", Locale: &locale, MS: at.UnixMilli(), Options: shape}})
		if results[0].Err != "" {
			t.Fatalf("node: %s", results[0].Err)
		}
		return goClock, results[0].S
	}
	brusselsGo, brusselsNode := clock("Europe/Brussels")
	if brusselsGo != "12:00:00" || brusselsNode != "12:00:00" {
		t.Errorf("Europe/Brussels at %s: go %q, node %q, want both 12:00:00", at.Format(time.RFC3339), brusselsGo, brusselsNode)
	}
	amsterdamGo, amsterdamNode := clock("Europe/Amsterdam")
	if amsterdamGo != "12:19:32" {
		t.Errorf("Europe/Amsterdam at %s: go prints %q, want 12:19:32 (Amsterdam's own tzdata history, AMT)", at.Format(time.RFC3339), amsterdamGo)
	}
	if amsterdamNode != brusselsNode {
		t.Errorf("Europe/Amsterdam at %s: node prints %q, want Brussels' %q (ICU links Amsterdam to Brussels)", at.Format(time.RFC3339), amsterdamNode, brusselsNode)
	}
	if amsterdamGo == amsterdamNode {
		t.Errorf("Europe/Amsterdam at %s: go and node agree on %q, so D465's zone-data difference is gone", at.Format(time.RFC3339), amsterdamGo)
	}
}
