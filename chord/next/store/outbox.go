package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/next/store/outbox.ts
//
// The outbox (slice 6a §2; decisions "The outbox is optional, internal and versioned", small items 3-4): the store's
// commits as the sync layer reads them, one `{ seq, writes }` per commit from its floor on, in the current format.
//
// Go forced differences:
//   - Pi's `Number.isSafeInteger(afterSeq)` / `(limit)` checks of a number that may be fractional are designed out
//     (the arguments are int64); the range checks stay.
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
	Writes []StoreWrite
}

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
		writes, err := decodeRecordedWrites(text)
		if err != nil {
			return nil, err
		}
		commits[index] = OutboxCommit{Seq: seq, NextID: nextID, Writes: writes}
	}
	return commits, nil
}

// decodeRecordedWrites is `JSON.parse(writes) as StoreWrite[]`: the writes a commit recorded, typed.
func decodeRecordedWrites(text string) ([]StoreWrite, error) {
	parsed, err := delta.DecodeJson([]byte(text))
	if err != nil {
		return nil, err
	}
	items, ok := parsed.([]any)
	if !ok {
		return nil, errors.New("outbox: a commit's writes are no array")
	}
	writes := make([]StoreWrite, len(items))
	for index, item := range items {
		object, ok := item.(*delta.JsonObject)
		if !ok {
			return nil, errors.New("outbox: a recorded write is no object")
		}
		if writes[index], err = decodeStoreWrite(object, "outbox"); err != nil {
			return nil, err
		}
	}
	return writes, nil
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
