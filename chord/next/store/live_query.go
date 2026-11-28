package store

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/store/live-query.ts
//
// Live queries (decision "Live queries over an object's own tables"; slice 14 §3): the rows of a query as a JSON
// watchable, kept current by the store's reads. `self.query(table, q, limit)` is the owner's own unnamed window (its
// value the rows); `self.liveQuery(namedQuery, args, { first })` reads a named query (its value `{ rows, more }`).
// Both are this source over a shared read (shared_read.go: identical reads of a named query are one); a commit's run
// is published in that commit's batch as the diff from the previous rows (W1).

// CheckLiveQuery refuses what the types cannot (plugin code is bundled without type checks; declaration policy): a query of
// another table than table, and a limit that is no positive integer (a live query must be bounded: one run reads at most
// limit rows).
func CheckLiveQuery(table *TableDefinition, q *Query, limit int64, label string) error {
	if err := q.Err(); err != nil {
		return err
	}
	if q.Table != table {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf(`%s: self.query: a query of table "%s" over "%s"`, label, q.Table.Name, table.Name))
	}
	if limit < 1 {
		return data.Invalid(data.ReasonArgs, label+": self.query: a limit is required, a positive integer")
	}
	return nil
}

// LiveQuery is a live query as a JSON source (a state member, a derive source, an `on` source): the rows of one range of a
// query, or its count, as a shared read gives them (shared_read.go). `Read` gives it the query; a later `Read`
// (arguments that follow a value, a provider's successor) shows the previous value until the new read has an outcome,
// then switches in one frame.
type LiveQuery struct {
	state.FramedSource
	site  ReadSite
	shape LiveShape
	// current is the shared read its value is of, and next the one that replaces it once it has an outcome: never the same one.
	current *SharedRead
	next    *SharedRead
}

var (
	_ state.SourceImpl = (*LiveQuery)(nil)
	_ ReadSubscriber   = (*LiveQuery)(nil)
)

// NewLiveQuery is a live query of shape, owned by owner, in site.
func NewLiveQuery(site ReadSite, owner state.FramedOwner, shape LiveShape) *LiveQuery {
	query := &LiveQuery{site: site, shape: shape}
	query.Init(site.Batch, owner)
	return query
}

// Load is the first run of the owner's own window, before its object goes live (activation, after its tables are prepared): a
// failure is returned and fails the activation.
func (query *LiveQuery) Load(target LiveTarget, ctx context.Context) error {
	query.site.Batch.Section(query.Quiet)
	query.Read(target, ctx)
	var failure error
	query.site.Batch.Section(func() { failure = query.Failure() })
	return failure
}

// Read reads target from now on (its shape's range of it, or its count), through the shared read of its key. Blocks
// until that read has an outcome, or its first run ended without one (a table it reads is held).
func (query *LiveQuery) Read(target LiveTarget, ctx context.Context) *ReadRun {
	return query.Open(target, ctx)()
}

// Open is the part of Read that Pi's `read` runs when it is called, before its first await: the shared read is opened
// (or joined) and subscribed. Returns the load function that waits for (and, if newly created, runs) the first load.
func (query *LiveQuery) Open(target LiveTarget, ctx context.Context) (load func() *ReadRun) {
	var read *SharedRead
	var created bool
	var wait <-chan struct{}
	query.site.Batch.Section(func() {
		if query.Detached() {
			return
		}
		read, created = SharedReadOf(query.site, query.shape, target, ctx)
		replaced := query.next
		// The read its value is of (arguments that changed and changed back): nothing replaces it.
		if read == query.current {
			query.next = nil
		} else {
			query.next = read
		}
		if replaced != nil && replaced != read {
			replaced.Unsubscribe(query)
		}
		wait = read.Subscribe(query, ctx)
	})
	if read == nil {
		return func() *ReadRun { return nil }
	}
	return func() *ReadRun {
		if created {
			return read.Load()
		} else if wait != nil {
			select {
			case <-wait:
			case <-ctx.Done():
			}
		}
		return nil
	}
}

// Fail is: what it should read cannot be read (its arguments threw, its provider refused them): a failed member, as a failed
// run makes it. Its subscriptions end: neither the read of the previous arguments nor a pending one may publish or heal
// the failure; the next `Read` does.
func (query *LiveQuery) Fail(err error, ctx context.Context) {
	query.site.Batch.Section(func() {
		if query.Detached() {
			return
		}
		query.close()
		// The failure has no error to return.
		_ = query.site.Batch.Within(func() error { query.FailWith(err); return nil }, ctx)
	})
}

// Detach is: its attempt ended: its subscriptions end, reads return err (the object's), its watches end `ended`.
func (query *LiveQuery) Detach(err error) {
	query.close()
	query.End(err)
}

func (query *LiveQuery) close() {
	if query.current != nil {
		query.current.Unsubscribe(query)
	}
	if query.next != nil {
		query.next.Unsubscribe(query)
	}
	query.current, query.next = nil, nil
}

// Adopted takes an outcome of one of its shared reads, inside a batch. The first outcome of the read that replaces the
// current one makes it the current one. A failure makes the query a failed member.
func (query *LiveQuery) Adopted(read *SharedRead, outcome ReadOutcome) {
	if query.Detached() {
		return
	}
	if read == query.next {
		if query.current != nil {
			query.current.Unsubscribe(query)
		}
		query.current = read
		query.next = nil
	} else if read != query.current {
		return
	}
	if outcome.Kind == "failed" {
		query.FailWith(outcome.Err)
	} else {
		query.Set(outcome.Value)
	}
}

// DecodedRowJSON is a stored row of definition's table as the JSON object a live query shows (Pi's `rows.decode(stored,
// context) as JsonValue`): its columns' values, scalars and JSON, a row the code cannot read is the error.
func DecodedRowJSON(definition *TableDefinition, stored StoredRead, ctx context.Context) (delta.JsonValue, error) {
	decoded, err := definition.Rows.Decode(ctx, stored)
	if err != nil {
		return nil, err
	}
	return rowJSON(definition, decoded), nil
}

// rowJSON is a decoded row as a JSON object in the table's column order (absent optional columns left out; integers as JSON
// numbers; a `host(...)` cell as the address's object).
func rowJSON(definition *TableDefinition, row Row) *delta.JsonObject {
	object := delta.NewJsonObject(len(definition.Columns))
	for _, column := range definition.Columns {
		value, present := row[column.Name]
		if !present || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int64:
			value = float64(typed)
		case contracts.Address:
			// A `host(...)` cell is the address's plain object (a JSON value cannot hold the struct).
			value = delta.JsonObjectOf("kind", typed.Kind, "params", typed.Params, "key", typed.Key)
		}
		object.Set(column.Name, value)
	}
	return object
}
