package surfaces

import (
	"testing"
	"time"
	_ "time/tzdata" // fallback when a host has no system zoneinfo; time.LoadLocation checks ZONEINFO, then the system database, then the embedded copy
)

// D465, the zone-data difference: Go reads ZONEINFO, then the host's system zoneinfo, then the embedded time/tzdata
// fallback; Node uses ICU's bundled tz data. For a zone that ICU links to another (Europe/Amsterdam -> Europe/Brussels),
// the clock of an instant before 1970 depends on whether the loaded tzdata retains the zone's own pre-1970 history:
// with pre-1937 AMT (+00:19:32, 1172 s) present, Go prints 12:19:32 while Node prints Brussels' 12:00:00; on a host
// whose system zoneinfo links Amsterdam to Brussels without pre-1937 history (for example macOS), Go prints what Node
// prints. The test checks the loaded location at 1930-01-01T12:00Z against live Node under the same `TZ`, with Brussels
// as the control that both sides print alike.
func TestFormatTimeInAmsterdamBefore1937KeepsTzdataHistoryWhereNodePrintsBrusselsClock(t *testing.T) {
	node := driftNode(t)
	platform(t, nil, nil, nil)
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	at := time.Date(1930, time.January, 1, 12, 0, 0, 0, time.UTC)
	locale := "en-GB"
	shape := map[string]any{"hour": "2-digit", "minute": "2-digit", "second": "2-digit"}
	options := &TimeOptions{Hour: FieldTwoDigit, Minute: FieldTwoDigit, Second: FieldTwoDigit}
	clock := func(zone string) (goClock, nodeClock string, loc *time.Location) {
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
		return goClock, results[0].S, location
	}
	brusselsGo, brusselsNode, _ := clock("Europe/Brussels")
	if brusselsGo != "12:00:00" || brusselsNode != "12:00:00" {
		t.Errorf("Europe/Brussels at %s: go %q, node %q, want both 12:00:00", at.Format(time.RFC3339), brusselsGo, brusselsNode)
	}
	amsterdamGo, amsterdamNode, amsterdamLoc := clock("Europe/Amsterdam")
	if amsterdamNode != brusselsNode {
		t.Errorf("Europe/Amsterdam at %s: node prints %q, want Brussels' %q (ICU links Amsterdam to Brussels)", at.Format(time.RFC3339), amsterdamNode, brusselsNode)
	}
	const amsterdamPre1937Offset = 19*60 + 32 // +00:19:32 (1172 s), Amsterdam Mean Time (AMT)
	zoneName, offset := at.In(amsterdamLoc).Zone()
	if offset == amsterdamPre1937Offset {
		t.Logf("Europe/Amsterdam at %s: host tzdata carries pre-1937 history (%s, +%ds); expecting Go 12:19:32 != Node %s", at.Format(time.RFC3339), zoneName, offset, amsterdamNode)
		if amsterdamGo != "12:19:32" {
			t.Errorf("Europe/Amsterdam at %s: go prints %q, want 12:19:32 (Amsterdam's own tzdata history, AMT)", at.Format(time.RFC3339), amsterdamGo)
		}
		if amsterdamGo == amsterdamNode {
			t.Errorf("Europe/Amsterdam at %s: go and node agree on %q, so D465's zone-data difference is gone", at.Format(time.RFC3339), amsterdamGo)
		}
		return
	}
	t.Logf("Europe/Amsterdam at %s: host tzdata lacks pre-1937 history (%s, %+ds); expecting Go == Node (%s)", at.Format(time.RFC3339), zoneName, offset, amsterdamNode)
	if amsterdamGo != amsterdamNode {
		t.Errorf("Europe/Amsterdam at %s without pre-1937 history: go prints %q, want node's %q", at.Format(time.RFC3339), amsterdamGo, amsterdamNode)
	}
}
