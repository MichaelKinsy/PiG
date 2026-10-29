package store

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/outbox.ts
//
// The outbox (slice 6a §2; decisions "The outbox is optional, internal and versioned", small items 3-4): the store's
// commits as the sync layer reads them, one `{ seq, writes }` per commit from its floor on, in the current format.
//
// Go forced differences:
//   - Pi's `Number.isSafeInteger(afterSeq)` / `(limit)` checks of a number that may be fractional are designed out
//     (the arguments are int64); the range checks stay.
//   - Pi hands the stored writes out as `JSON.parse(writes) as StoreWrite[]`, unvalidated: the only read failure is text
//     that is no JSON (V8's SyntaxError message, a plain error). OutboxCommit.Raw is that parsed value; Writes is the
//     typed view Go needs (see parseRecordedWrites), never a check.
//   - Async contract (Go form of Pi's `#queued` promise chain): Read and Trim run one at a time, in the order the
//     calls reach the outbox (a ticket, as the store's commit queue): a read checks the floor and reads with no trim
//     running (a trim's commit may have deleted rows before its floor is set), a trim waits for the reads in flight.
//     A settled entry never blocks the next whatever it returned; nothing queued is cancelled (ctx is checked at the
//     call, as Pi's `throwIfAborted`, and guards the read itself). The floor is read under a mutex from any goroutine.

// OutboxFormat is the write vocabulary's version an outbox commit is stored in; a newer one is refused, data intact.
const OutboxFormat int64 = 1

// OutboxCommit is one commit as the outbox gives it: its seq, the allocator's value it persisted (D3) and its writes
// in applied order.
type OutboxCommit struct {
	Seq    int64
	NextID int64
	// Raw is the commit's stored writes text parsed as `JSON.parse` gives it, whatever its shape: Pi's outbox does not
	// validate it, so Raw is the value Pi hands out.
	Raw delta.JsonValue
	// Writes is the typed view of Raw, Go's need: each element of an array Raw as the write it decodes to, or an
	// UnknownWrite carrying it as parsed when it decodes to none (nil when Raw is no array).
	Writes []StoreWrite
}

// UnknownWrite is a recorded element that is no store write the Go vocabulary decodes: Pi's outbox passes any JSON
// through, so the element is kept as it was parsed. Its kind is its `kind` member when it has a string one.
type UnknownWrite struct {
	Value delta.JsonValue
	// Panic is what decoding the element as a typed write panicked with, nil when it only did not decode to the same JSON.
	Panic any
}

// StoreWriteKind is the `kind` member of the element, "" when it has none.
func (write *UnknownWrite) StoreWriteKind() string {
	if object, ok := write.Value.(*delta.JsonObject); ok {
		kind, _ := object.Value("kind").(string)
		return kind
	}
	return ""
}

// MarshalJSON is the element as it was parsed.
func (write *UnknownWrite) MarshalJSON() ([]byte, error) { return jsstring.MarshalJSON(write.Value) }

// Outbox is a store's outbox: its commits after the floor (the seq it was turned on at, advanced by Trim).
type Outbox struct {
	store *ChordStore
	mu    sync.Mutex
	floor int64
	// tail is the queue's tail: the next operation starts when it is closed.
	tail <-chan struct{}
}

// NewOutbox is the outbox of store starting after floor.
func NewOutbox(store *ChordStore, floor int64) *Outbox {
	settled := make(chan struct{})
	close(settled)
	return &Outbox{store: store, floor: floor, tail: settled}
}

// Floor is the seq the outbox starts after: Read takes any afterSeq from it to LastSeq.
func (outbox *Outbox) Floor() int64 {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	return outbox.floor
}

// Read is up to limit commits after afterSeq, in seq order; afterSeq outside floor..lastSeq is `invalid/args`.
func (outbox *Outbox) Read(afterSeq, limit int64, ctx context.Context) ([]OutboxCommit, error) {
	if limit < 1 {
		return nil, data.Invalid(data.ReasonArgs, "outbox: limit is a positive integer")
	}
	previous, done := outbox.ticket()
	<-previous
	defer close(done)
	return outbox.read(afterSeq, limit, ctx)
}

// Trim drops the commits through throughSeq and advances the floor; idempotent; beyond lastSeq is `invalid/args`.
// Trims run one at a time, so the stored floor only rises and equals the one in memory.
func (outbox *Outbox) Trim(throughSeq int64, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if lastSeq := outbox.store.LastSeq(); throughSeq > lastSeq {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("outbox: trim through %d, beyond %d", throughSeq, lastSeq))
	}
	previous, done := outbox.ticket()
	<-previous
	defer close(done)
	return outbox.trim(throughSeq)
}

// ticket takes the next place in the queue: the channel to wait on, and the one to close when done.
func (outbox *Outbox) ticket() (previous <-chan struct{}, done chan struct{}) {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	previous = outbox.tail
	done = make(chan struct{})
	outbox.tail = done
	return previous, done
}

// read is Read, its turn come: the floor as no trim moves it.
func (outbox *Outbox) read(afterSeq, limit int64, ctx context.Context) ([]OutboxCommit, error) {
	floor, lastSeq := outbox.Floor(), outbox.store.LastSeq()
	if afterSeq < floor || afterSeq > lastSeq {
		return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("outbox: read after %d, outside %d..%d", afterSeq, floor, lastSeq))
	}
	rows, err := Read(outbox.store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(
			`SELECT seq, format, next_id, writes FROM `+TableOutbox+` WHERE seq > ? ORDER BY seq LIMIT ?`,
			afterSeq, limit,
		)
	}, ctx)
	if err != nil {
		return nil, err
	}
	commits := make([]OutboxCommit, len(rows))
	for index, row := range rows {
		seq, err := sqlite.RowInt(row, "seq")
		if err != nil {
			return nil, err
		}
		format, err := sqlite.RowInt(row, "format")
		if err != nil {
			return nil, err
		}
		if format != OutboxFormat {
			message := fmt.Sprintf("outbox: commit %d is in format %d, newer than %d", seq, format, OutboxFormat)
			return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: message}, nil)
		}
		nextID, err := sqlite.RowInt(row, "next_id")
		if err != nil {
			return nil, err
		}
		text, _ := row["writes"].(string)
		raw, writes, err := parseRecordedWrites(text)
		if err != nil {
			return nil, err
		}
		commits[index] = OutboxCommit{Seq: seq, NextID: nextID, Raw: raw, Writes: writes}
	}
	return commits, nil
}

// parseRecordedWrites is `JSON.parse(writes) as StoreWrite[]`: Pi parses the stored text and hands the value out with no
// check of any kind, so the only failure is text that is no JSON, which is the plain SyntaxError JSON.parse throws (V8's
// message, from jsonparse). Raw is the parsed value as it is, whatever its shape.
//
// Go forced difference: `Writes` is the typed view Go callers need, never a validation and never a change of what was
// recorded: an element of an array is its typed write only when that write serializes back to exactly the element (the
// stored form of a write the store recorded does), any other (an unknown kind, a malformed or extra member, a value that
// is no object) is an UnknownWrite carrying it as parsed, and a Raw that is no array has no Writes. decodeStoreWrite
// assumes a schema-checked object (archive.go's CheckWrite) and fills what is missing with zero values, so its result
// is trusted only through that round trip; a panic on a member of the wrong shape falls back to an UnknownWrite too.
func parseRecordedWrites(text string) (raw delta.JsonValue, writes []StoreWrite, err error) {
	if err := jsonparse.Validate([]byte(text)); err != nil {
		return nil, nil, err
	}
	raw, err = delta.DecodeJson([]byte(text))
	if err != nil {
		return nil, nil, err
	}
	items, ok := raw.([]any)
	if !ok {
		return raw, nil, nil
	}
	writes = make([]StoreWrite, len(items))
	for index, item := range items {
		writes[index] = typedWrite(item)
	}
	return raw, writes, nil
}

// typedWrite is a recorded element as the write it decodes to when that write is the same JSON, else as an UnknownWrite
// (see parseRecordedWrites).
func typedWrite(item delta.JsonValue) (write StoreWrite) {
	defer func() {
		if reason := recover(); reason != nil {
			write = &UnknownWrite{Value: item, Panic: reason}
		}
	}()
	if object, ok := item.(*delta.JsonObject); ok {
		if typed, err := decodeStoreWrite(object, "outbox"); err == nil && sameJSON(typed, item) {
			return typed
		}
	}
	return &UnknownWrite{Value: item}
}

// sameJSON reports whether a and b serialize to the same JSON text.
func sameJSON(a, b any) bool {
	first, err := jsstring.MarshalJSON(a)
	if err != nil {
		return false
	}
	second, err := jsstring.MarshalJSON(b)
	return err == nil && bytes.Equal(first, second)
}

func (outbox *Outbox) trim(throughSeq int64) error {
	if throughSeq <= outbox.Floor() {
		return nil
	}
	err := outbox.store.Maintain(func(executor sqlite.SqliteExecutor) error {
		if err := executor.Run(`DELETE FROM `+TableOutbox+` WHERE seq <= ?`, throughSeq); err != nil {
			return err
		}
		return executor.Run(`UPDATE `+TableMeta+` SET outbox_floor = ? WHERE singleton = 1`, throughSeq)
	})
	if err != nil {
		return err
	}
	outbox.mu.Lock()
	outbox.floor = throughSeq
	outbox.mu.Unlock()
	return nil
}
