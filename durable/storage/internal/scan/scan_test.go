package scan_test

// pi: packages/durable/src/storage/scan.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
)

// Ports packages/durable/src/storage/scan.ts scanStart: a cursor continues in its own order, a different requested order
// throws, a cursor written before scans had an order continues in the default, and a malformed cursor is invalid.
func TestStartOf(t *testing.T) {
	cases := []struct {
		name      string
		requested durable.ScanOrder // "" is a query without an order
		cursor    durable.Cursor
		fallback  durable.ScanOrder
		want      scan.Start
		wantError string
	}{
		{"no cursor, default order", "", nil, durable.ScanAscending, scan.Start{Order: durable.ScanAscending}, ""},
		{"no cursor, requested order", durable.ScanDescending, nil, durable.ScanAscending, scan.Start{Order: durable.ScanDescending}, ""},
		{"cursor keeps its order when the query omits it", "", scan.NextCursor(7, durable.ScanDescending), durable.ScanAscending, scan.Start{Order: durable.ScanDescending, After: 7, HasAfter: true}, ""},
		{"cursor accepts the repeated order", durable.ScanDescending, scan.NextCursor(7, durable.ScanDescending), durable.ScanAscending, scan.Start{Order: durable.ScanDescending, After: 7, HasAfter: true}, ""},
		{"cursor rejects the other order", durable.ScanAscending, scan.NextCursor(7, durable.ScanDescending), durable.ScanAscending, scan.Start{}, "The cursor continues a descending scan; the query asks for ascending"},
		{"cursor without an order continues in the default", "", durable.Cursor{"after": int64(3)}, durable.ScanDescending, scan.Start{Order: durable.ScanDescending, After: 3, HasAfter: true}, ""},
		// scan.ts:28-31: a cursor without an order continues in the default order, so asking for the other order throws.
		{"cursor without an order rejects the other order", durable.ScanDescending, durable.Cursor{"after": int64(3)}, durable.ScanAscending, scan.Start{}, "The cursor continues a ascending scan; the query asks for descending"},
		{"cursor without an order accepts the default order", durable.ScanAscending, durable.Cursor{"after": int64(3)}, durable.ScanAscending, scan.Start{Order: durable.ScanAscending, After: 3, HasAfter: true}, ""},
		// scan.ts:24: a cursor with no numeric position is invalid.
		{"a cursor without a position", "", durable.Cursor{"order": "ascending"}, durable.ScanAscending, scan.Start{}, "Invalid storage cursor"},
		{"an empty cursor is no cursor", durable.ScanDescending, durable.Cursor{}, durable.ScanAscending, scan.Start{Order: durable.ScanDescending}, ""},
		{"a decoded JSON cursor", "", durable.Cursor{"after": float64(3), "order": "ascending"}, durable.ScanDescending, scan.Start{Order: durable.ScanAscending, After: 3, HasAfter: true}, ""},
		{"an invalid requested order", "sideways", nil, durable.ScanAscending, scan.Start{}, "Invalid scan order: sideways"},
		{"an invalid cursor order", "", durable.Cursor{"after": int64(3), "order": "sideways"}, durable.ScanAscending, scan.Start{}, "Invalid storage cursor"},
		{"a non-integer cursor position", "", durable.Cursor{"after": 1.5}, durable.ScanAscending, scan.Start{}, "Invalid storage cursor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requested *durable.ScanOrder
			if c.requested != "" {
				requested = &c.requested
			}
			got, err := scan.StartOf(requested, c.cursor, c.fallback)
			if c.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantError) {
					t.Fatalf("StartOf error = %v, want %q", err, c.wantError)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("StartOf = %+v %v, want %+v", got, err, c.want)
			}
		})
	}
}
