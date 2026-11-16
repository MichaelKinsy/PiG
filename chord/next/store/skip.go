package store

import (
	"context"
	"slices"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/store/skip.ts
//
// The skip test (slice 14 §3.3): whether a commit's writes can have changed a read's result. A skip proves the result
// unchanged by the row before the write (the keys and matched values the read kept of its last run) and by the row
// after it (the probe: the read's own statement, limited to the rows the written row pairs with). There is no second
// evaluator: the database decides with the real condition.

// The kinds of a Change.
const (
	ChangePut    = "put"
	ChangeUpdate = "update"
	ChangeDelete = "delete"
	ChangeBulk   = "bulk"
)

// Change is one write of a transaction as the skip test reads it. A single-key write (Kind put, update, delete) names its
// Key and, for an update, the columns it Assigns; a bulk one (Kind bulk) the columns it assigns (Pi's `undefined`, a nil
// Assigned: it deletes rows; an update's Assigned is never empty, `CompileSet` refuses an empty set, so nil is no empty
// update).
type Change struct {
	Table    string
	Kind     string
	Key      map[string]sqlite.SqliteValue
	Assigned []string
}

// probesPerRead is how many probes one read may ask in one step before it runs instead. A probe costs a few microseconds
// and a run of a page a few hundred (`benchmarks.md`), so a transaction writing many rows (an import, a bulk `put`) must
// not probe a read once per row: past this many, running it is cheaper than asking on.
const probesPerRead = 8

// Skips is whether changes (the writes of one step, already applied) leave read's result as it is: every change is
// skipped for every use of its table by the read. A read without a result (loading, failed) is never skipped.
func Skips(read *RegisteredRead, changes []Change, step *Step, ctx context.Context) (bool, error) {
	latest := read.Latest(step.Executor)
	if latest == nil || latest.Kind == "failed" {
		return false, nil
	}
	var result *ReadResult
	if latest.Kind == "rows" {
		result = latest.Result
	}
	budget := probesPerRead
	for _, change := range changes {
		for index := range read.Plan.Uses {
			use := &read.Plan.Uses[index]
			if use.Table != change.Table {
				continue
			}
			skipped, err := skipped(read, use, change, result, step, ctx, &budget)
			if err != nil || !skipped {
				return false, err
			}
		}
	}
	return true, nil
}

// skipped is one change against one use of its table (the table of slice 14 §3.3, row by row).
func skipped(read *RegisteredRead, use *Use, change Change, result *ReadResult, step *Step, ctx context.Context, budget *int) (bool, error) {
	assigns := func(columns map[string]bool) bool {
		return change.Assigned == nil || slices.ContainsFunc(change.Assigned, func(name string) bool { return columns[name] })
	}
	// "Any": an `update` (a bulk one too) that assigns none of the columns the read depends on in that table.
	if change.Kind != ChangePut && change.Kind != ChangeDelete && !assigns(use.Depends) {
		return true, nil
	}
	// A bulk write names no rows, and a count keeps none: nothing more can be proved.
	if change.Kind == ChangeBulk || result == nil {
		return false, nil
	}
	// Where the value before is unknown and matters, the read runs: a matched column outside the key may have been
	// re-pointed, and the rows it paired with before are unknown.
	if !use.Stable {
		return false, nil
	}
	values := make([]sqlite.SqliteValue, len(use.PairedBy))
	for index, name := range use.PairedBy {
		values[index] = change.Key[name]
	}
	fetched := result.Paired[use]
	if use.Role == UseOwn {
		fetched = result.Keys
	}
	// Before: a fetched row the written row pairs with decided the result (unknown pairs count as one).
	if fetched == nil || fetched[IdentityOf(values)] {
		return false, nil
	}
	// After. A row that was not in the result cannot enter it by leaving (its own delete, its partner's), nor by an
	// `update` of columns that decide neither membership nor position. An `exists` row's delete or update can bring
	// its own rows in (`not(exists(…))`): only the probe tells.
	if use.Role != UseExists && (change.Kind == ChangeDelete || (change.Kind == ChangeUpdate && !assigns(use.Enters))) {
		return true, nil
	}
	if !use.Keyed {
		return false, nil
	}
	spent := *budget == 0
	*budget--
	if spent {
		return false, nil
	}
	found, err := probe(read, use.Pairs, values, result, step, ctx)
	return !found, err
}

// probe is whether, after the step's writes, a row with values in columns is in the read's range.
func probe(read *RegisteredRead, columns []string, values []sqlite.SqliteValue, result *ReadResult, step *Step, ctx context.Context) (bool, error) {
	if metrics := chordctx.Telemetry(ctx).Metrics(); metrics != nil {
		metrics.Counter("chord.query.probes", telemetry.InstrumentOptions{}).Add(1, nil)
	}
	statement := read.Plan.Probe(read.Probed(result), columns, values)
	row, err := step.Get(statement.SQL, statement.Params...)
	return row != nil, err
}
