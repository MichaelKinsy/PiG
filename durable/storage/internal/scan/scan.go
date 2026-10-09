// Package scan resolves the order and position of a built-in storage scan.
package scan

// Ports packages/durable/src/storage/scan.ts

import (
	"errors"
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/durable"
)

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the end of the durable ID space.
const maxSafeInteger = 1<<53 - 1

// ErrInvalidCursor is the error of a cursor that this backend did not write.
var ErrInvalidCursor = errors.New("Invalid storage cursor")

// Start is where a built-in storage scan starts: its order and the last ID a previous page returned.
type Start struct {
	Order durable.ScanOrder
	// After is the last ID a previous page returned; HasAfter is false for a scan that starts at the beginning.
	After    int64
	HasAfter bool
}

func validOrder(order durable.ScanOrder) bool {
	return order == durable.ScanAscending || order == durable.ScanDescending
}

// StartOf resolves a scan's order and position. A cursor continues in the order it was created with, whether the query
// repeats that order or omits it; a different order is an error. A cursor without an order, written before scans had
// one, continues in the scan's default order. An empty cursor is no cursor: Go callers pass a zero Cursor for the
// start of a scan.
func StartOf(requested *durable.ScanOrder, cursor durable.Cursor, fallback durable.ScanOrder) (Start, error) {
	if requested != nil && !validOrder(*requested) {
		return Start{}, fmt.Errorf("Invalid scan order: %s", *requested)
	}
	if len(cursor) == 0 {
		if requested != nil {
			return Start{Order: *requested}, nil
		}
		return Start{Order: fallback}, nil
	}
	after, ok := cursorAfter(cursor["after"])
	if !ok {
		return Start{}, ErrInvalidCursor
	}
	order := fallback
	if stored, present := cursor["order"]; present {
		text, isText := stored.(string)
		if !isText || !validOrder(durable.ScanOrder(text)) {
			return Start{}, ErrInvalidCursor
		}
		order = durable.ScanOrder(text)
	}
	if requested != nil && *requested != order {
		return Start{}, fmt.Errorf("The cursor continues a %s scan; the query asks for %s", order, *requested)
	}
	return Start{Order: order, After: after, HasAfter: true}, nil
}

// cursorAfter decodes the ID of a cursor: a safe integer in any numeric form a JSON round trip can leave.
func cursorAfter(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, typed >= -maxSafeInteger && typed <= maxSafeInteger
	case int:
		return int64(typed), int64(typed) >= -maxSafeInteger && int64(typed) <= maxSafeInteger
	case float64:
		if typed != math.Trunc(typed) || math.Abs(typed) > maxSafeInteger {
			return 0, false
		}
		return int64(typed), true
	default:
		return 0, false
	}
}

// NextCursor is the continuation of a scan whose last returned item has id.
func NextCursor(id int64, order durable.ScanOrder) durable.Cursor {
	return durable.Cursor{"after": id, "order": string(order)}
}
