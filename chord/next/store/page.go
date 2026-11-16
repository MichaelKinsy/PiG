package store

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/store/page.ts
//
// Pages (decision "Query runner"; slice 14 §3.1): one page of a query read once, not live, with the opaque cursor that
// continues after it. A table handle's `page` and a named query's `page` are this.

// StoredPage is the stored rows of one page and, when more follow, the cursor to continue after it (nil otherwise).
type StoredPage struct {
	Stored []StoredRead
	Next   *string
}

// ReadPage is up to limit rows of plan's query after the cursor after (a trust boundary: an app hands it back; nil starts
// at the first row), read under the store's guard. limit is a positive integer (`invalid/args`).
func ReadPage(store *ChordStore, plan *ReadPlan, limit int64, after *string, ctx context.Context) (StoredPage, error) {
	query, table, order := plan.Query, plan.Table, plan.Order
	if limit < 1 {
		return StoredPage{}, data.Invalid(data.ReasonArgs, query.Table.Name+": limit is a positive integer")
	}
	var from Position
	if after != nil {
		decoded, err := DecodeCursor(query.Table, table, order, *after, query.Column)
		if err != nil {
			return StoredPage{}, err
		}
		from = decoded
	}
	// One row more than asked for: whether a further page exists.
	statement := plan.Select(Range{After: from, Limit: int(limit) + 1})
	stored, err := Read(store, func(executor sqlite.SqliteExecutor) ([]StoredRead, error) {
		return executor.All(statement.SQL, statement.Params...)
	}, ctx)
	if err != nil {
		return StoredPage{}, err
	}
	if int64(len(stored)) <= limit {
		return StoredPage{Stored: stored}, nil
	}
	stored = stored[:limit]
	last := stored[limit-1]
	values := make([]sqlite.SqliteValue, len(order.Columns))
	for index, name := range order.Columns {
		values[index] = last[name]
	}
	next := EncodeCursor(table, order, values)
	return StoredPage{Stored: stored, Next: &next}, nil
}
