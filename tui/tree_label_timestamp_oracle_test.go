package tui

import (
	"math/rand"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// tree-selector.ts:862-885 formatLabelTimestamp against the pinned Pi TreeList for ISO timestamps across the whole Date range (same day, same year, other
// years with one-, two- and five-digit and negative years, as Date.toISOString writes them) and for strings Date.parse reads in other forms or rejects (an invalid Date
// prints "aN/NaN/NaN NaN:NaN"), in three zones with a fixed "now".
// mutation-checked: dropping the invalid-date text, zero-padding the year, taking the year modulo 100 and reading only RFC 3339 each fail it.
func TestFormatLabelTimestampMatchesPi(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var instants []int64
	for range 1200 {
		switch random.Intn(4) {
		case 0:
			instants = append(instants, now.UnixMilli()+(random.Int63n(40*86400)-20*86400)*1000)
		case 1:
			instants = append(instants, random.Int63n(4102444800000))
		case 2:
			instants = append(instants, -random.Int63n(62135596800000*2))
		default:
			instants = append(instants, random.Int63n(8640000000000000*2)-8640000000000000)
		}
	}
	// Years 1 to 9 and 0, midnight at the day boundary of "now".
	for year := int64(-12); year <= 12; year++ {
		instants = append(instants, time.Date(int(year), 3, 5, 14, 7, 0, 0, time.UTC).UnixMilli())
	}
	instants = append(instants, now.UnixMilli(), now.UnixMilli()-12*3600*1000, now.UnixMilli()-13*3600*1000, now.UnixMilli()+12*3600*1000)
	extra := []string{"garbage", "x", "2026-13-45", "Mar 5, 2026 10:00", "2026-03-05", "2026-10-11", "2026-10-11T12:00", "1", "Thu, 01 Jan 1970 00:00:00 GMT", "2026-10-11T12:00:00+05:00", "12/25/2025", "2026-10-11T12:00:00 PM", "2025-12-31T23:59:59.999999Z", "+275760-09-13T00:00:00.001Z"}
	for _, zone := range []string{"America/New_York", "Asia/Kolkata", "Pacific/Auckland"} {
		location, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		var want [][2]string
		pioracle.Run(t, `
const theme = await load("pi-coding-agent/modes/interactive/theme/theme.js");
theme.initTheme("dark", false);
const mod = await load("pi-coding-agent/modes/interactive/components/tree-selector.js");
const component = new mod.TreeSelectorComponent([{ entry: { id: "a", type: "message", parentId: null, timestamp: "2020-01-01T00:00:00Z", message: { role: "user", content: "x", timestamp: 0 } }, children: [] }], "a", 20, () => {}, () => {}, () => {});
const list = component.treeList;
const RealDate = Date;
const fixed = RealDate.parse("2026-10-11T12:00:00Z");
globalThis.Date = class extends RealDate { constructor(...a) { if (a.length === 0) super(fixed); else super(...a); } static now() { return fixed; } };
emit([...input.instants.map((ms) => new RealDate(ms).toISOString()), ...input.extra].map((ts) => [ts, list.formatLabelTimestamp(ts)]));`, map[string]any{"instants": instants, "extra": extra}, &want, "TZ="+zone)
		if len(want) != len(instants)+len(extra) {
			t.Fatalf("Node answered %d of %d", len(want), len(instants)+len(extra))
		}
		bad := 0
		for _, pair := range want {
			if got := formatLabelTimestamp(pair[0], now.In(location)); got != pair[1] {
				if bad++; bad <= 10 {
					t.Errorf("%s %q: %q, Pi %q", zone, pair[0], got, pair[1])
				}
			}
		}
	}
}
