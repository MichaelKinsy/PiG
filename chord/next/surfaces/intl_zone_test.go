package surfaces

import (
	"testing"
	"time"
	_ "time/tzdata" // fallback when a host has no system zoneinfo; time.LoadLocation checks ZONEINFO, then the system database, then the embedded copy
)

// D465 claim 2: the zone of `FormatTime` is the process's (`time.Local`), as a `Date`'s is in Node, across a daylight-saving change.
// For zones with a change the clock on each side of it is compared with the live Node run under the same `TZ` (the process zone is what
// `TZ` names, in Node and, once `time.Local` is loaded from it, in Go). The instants are a minute either side of each transition (a
// spring-forward and a fall-back, and a half-hour change in Lord Howe), and the test checks that the offset really changes between
// the two of a pair, so a corpus that stopped crossing a transition would fail instead of passing.
func TestFormatTimeInAZoneWithADaylightSavingChangeIsTheClockNodePrintsUnderTheSameTZ(t *testing.T) {
	node := driftNode(t)
	platform(t, nil, nil, nil)
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	zones := []struct {
		name   string
		before []time.Time // a minute before a transition; the transition is the minute after
	}{
		{"America/New_York", []time.Time{time.Date(2026, time.March, 8, 6, 59, 0, 0, time.UTC), time.Date(2026, time.November, 1, 5, 59, 0, 0, time.UTC)}},
		{"Europe/Berlin", []time.Time{time.Date(2026, time.March, 29, 0, 59, 0, 0, time.UTC), time.Date(2026, time.October, 25, 0, 59, 0, 0, time.UTC)}},
		{"Australia/Lord_Howe", []time.Time{time.Date(2026, time.April, 4, 14, 59, 0, 0, time.UTC), time.Date(2026, time.October, 3, 15, 29, 0, 0, time.UTC)}},
	}
	shapes := []struct {
		options *TimeOptions
		node    map[string]any
	}{
		{nil, map[string]any{"timeStyle": "short"}},
		{&TimeOptions{Hour: FieldTwoDigit, Minute: FieldTwoDigit, Second: FieldTwoDigit}, map[string]any{"hour": "2-digit", "minute": "2-digit", "second": "2-digit"}},
	}
	for _, zone := range zones {
		t.Run(zone.name, func(t *testing.T) {
			location, err := time.LoadLocation(zone.name)
			if err != nil {
				t.Fatal(err)
			}
			time.Local = location
			t.Setenv("TZ", zone.name)
			var cases []driftCase
			var instants []time.Time
			for _, before := range zone.before {
				_, offsetBefore := before.In(location).Zone()
				_, offsetAfter := before.Add(time.Minute).In(location).Zone()
				if offsetBefore == offsetAfter {
					t.Fatalf("%s has no change of offset at %v", zone.name, before.Add(time.Minute))
				}
				for _, at := range []time.Time{before, before.Add(time.Minute), before.Add(61 * time.Minute)} {
					for _, locale := range []string{"en-US", "de", "ja"} {
						for _, shape := range shapes {
							cases = append(cases, driftCase{Kind: "time", Locale: &locale, MS: at.UnixMilli(), Options: shape.node, timeOptions: shape.options})
							instants = append(instants, at)
						}
					}
				}
			}
			for i, want := range runNode(t, node, cases) {
				c := cases[i]
				got, err := FormatTime(&Language{Locale: *c.Locale}, instants[i], c.timeOptions)
				if want.Err != "" || err != nil || got != want.S {
					t.Errorf("%s %s at %s with %v: go %q (%v), node %+v", zone.name, *c.Locale, instants[i].Format(time.RFC3339), c.Options, got, err, want)
				}
			}
		})
	}
}
