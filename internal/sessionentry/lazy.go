package sessionentry

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// This file is the funnel through which code reads what a session entry is: every assertion of MessageEntry, RawEntry or LazyEntry on a
// SessionEntry or FileEntry value lives here (TestOnlyTheFunnelAssertsMessageMembers), so a LazyEntry is read, verified and decoded before
// any caller sees its message.

// RecordAt locates a record in a RecordSource and identifies its bytes.
type RecordAt struct {
	// Off and Size are where the record lies in its source, and Hash the source's hash of its bytes.
	Off  int64
	Size int32
	Hash uint64
}

// RecordSource reads records back, verified against the place and hash they were indexed with.
type RecordSource interface {
	// ReadRecords sets out[i] to the record at ats[i]; out has the length of ats. On an error, bad is the index of the record it is about, or
	// -1 when it is about none.
	ReadRecords(ats []RecordAt, out []json.RawMessage) (bad int, err error)
}

// RecordChangedError reports a record that cannot be read back as it was indexed: the session file was changed or removed under the session.
type RecordChangedError struct {
	// ID is the entry's id, empty when the error is about no one record.
	ID  string
	Err error
}

func (e *RecordChangedError) Error() string {
	if e.ID == "" {
		return fmt.Sprintf("session entries cannot be read back: %v", e.Err)
	}
	return fmt.Sprintf("session entry %s cannot be read back: %v", e.ID, e.Err)
}

func (e *RecordChangedError) Unwrap() error { return e.Err }

// readFrom reads the records of entries, all of src, into out.
func readFrom(src RecordSource, entries []*LazyEntry, out []json.RawMessage) error {
	ats := make([]RecordAt, len(entries))
	for k, entry := range entries {
		ats[k] = entry.at
	}
	if bad, err := src.ReadRecords(ats, out); err != nil {
		changed := &RecordChangedError{Err: err}
		if bad >= 0 && bad < len(entries) {
			changed.ID = entries[bad].id
		}
		return changed
	}
	return nil
}

// LazyEntry is a message entry whose record stays in its source until it is read: Raw and MarshalJSON read the record, and AsMessage and
// Materialize decode it into the member DecodeSessionEntry gives it. Its base members are known without the record.
type LazyEntry struct {
	id, timestamp string
	parentID      *string
	src           RecordSource
	at            RecordAt
}

// NewLazyEntry is the entry of a message record with the given id, parentId and timestamp, read from src at at.
func NewLazyEntry(id string, parentID *string, timestamp string, src RecordSource, at RecordAt) LazyEntry {
	return LazyEntry{id: id, timestamp: timestamp, parentID: parentID, src: src, at: at}
}

// Indexed reports whether the entry was made by NewLazyEntry, not left zero.
func (e *LazyEntry) Indexed() bool { return e.src != nil }

func (e *LazyEntry) Base() SessionEntryBase {
	return SessionEntryBase{Type: "message", ID: e.id, ParentID: e.parentID, Timestamp: e.timestamp}
}

func (e *LazyEntry) fileEntry() {}

// Raw reads the record. It panics with a *RecordChangedError when the record changed on disk; code that reads entries a session returns uses
// ReadRaw, AsMessage or Materialize, which return that error.
func (e *LazyEntry) Raw() json.RawMessage {
	raw, err := e.read()
	if err != nil {
		panic(err)
	}
	return raw
}

// MarshalJSON writes the record as it was read.
func (e *LazyEntry) MarshalJSON() ([]byte, error) { return e.read() }

func (e *LazyEntry) read() (json.RawMessage, error) {
	out := make([]json.RawMessage, 1)
	if err := readFrom(e.src, []*LazyEntry{e}, out); err != nil {
		return nil, err
	}
	return out[0], nil
}

var errBaseDiffers = errors.New("its record has other base members than the index")

// decode is the member DecodeSessionEntry reads from raw, which must have the base members the entry was indexed with.
func (e *LazyEntry) decode(raw json.RawMessage) (SessionEntry, error) {
	decoded := DecodeSessionEntry(raw)
	if got := decoded.Base(); got.Type != "message" || !sameKey(got.ID, e.id) || got.Timestamp != e.timestamp ||
		(got.ParentID == nil) != (e.parentID == nil) || got.ParentID != nil && !sameKey(*got.ParentID, *e.parentID) {
		return nil, &RecordChangedError{ID: e.id, Err: errBaseDiffers}
	}
	// An object-valued id or parentId is keyed anew by each decode; the entry keeps the keys it was indexed with.
	switch entry := decoded.(type) {
	case MessageEntry:
		entry.ID, entry.ParentID = e.id, e.parentID
		return entry, nil
	case RawEntry:
		entry.ID, entry.ParentID = e.id, e.parentID
		return entry, nil
	}
	return decoded, nil
}

// ReadRaw is entry's record. It is Raw, with the error of a LazyEntry whose record changed on disk.
func ReadRaw(entry FileEntry) (json.RawMessage, error) {
	if lazy, ok := entry.(*LazyEntry); ok {
		return lazy.read()
	}
	return entry.Raw(), nil
}

// ReadRaws is the record of each entry, reading the records of LazyEntry values of one source together in source order.
func ReadRaws(entries []SessionEntry) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, len(entries))
	type pending struct {
		entries []*LazyEntry
		index   []int
	}
	var sources []RecordSource
	bySource := map[RecordSource]*pending{}
	for i, entry := range entries {
		lazy, ok := entry.(*LazyEntry)
		if !ok {
			out[i] = entry.Raw()
			continue
		}
		p := bySource[lazy.src]
		if p == nil {
			p = &pending{}
			bySource[lazy.src] = p
			sources = append(sources, lazy.src)
		}
		p.entries = append(p.entries, lazy)
		p.index = append(p.index, i)
	}
	for _, src := range sources {
		p := bySource[src]
		read := make([]json.RawMessage, len(p.entries))
		if err := readFrom(src, p.entries, read); err != nil {
			return nil, err
		}
		for k, i := range p.index {
			out[i] = read[k]
		}
	}
	return out, nil
}

// Materialize is entry with a LazyEntry read and decoded into the member DecodeSessionEntry gives its record (a MessageEntry, or a RawEntry
// when the message does not decode); any other entry is itself.
func Materialize(entry SessionEntry) (SessionEntry, error) {
	lazy, ok := entry.(*LazyEntry)
	if !ok {
		return entry, nil
	}
	raw, err := lazy.read()
	if err != nil {
		return nil, err
	}
	return lazy.decode(bytes.Clone(raw))
}

// MaterializeAll is Materialize of each entry, reading the records of LazyEntry values together and decoding a long run of them on several
// goroutines, joined before it returns; the error is the one of the first entry that fails. It returns entries itself when none is lazy.
func MaterializeAll(entries []SessionEntry) ([]SessionEntry, error) {
	lazy := 0
	for _, entry := range entries {
		if _, ok := entry.(*LazyEntry); ok {
			lazy++
		}
	}
	if lazy == 0 {
		return entries, nil
	}
	raws, err := ReadRaws(entries)
	if err != nil {
		return nil, err
	}
	out := make([]SessionEntry, len(entries))
	errs := make([]error, len(entries))
	decode := func(from, to int) {
		for i := from; i < to; i++ {
			l, ok := entries[i].(*LazyEntry)
			if !ok {
				out[i] = entries[i]
				continue
			}
			out[i], errs[i] = l.decode(bytes.Clone(raws[i]))
		}
	}
	workers := lazy / parallelDecodeMin
	if workers > 1 {
		// pig additive (D174): the workers count and run at the runtime default width.
		defer procpolicy.Widen()()
		workers = min(workers, runtime.GOMAXPROCS(0))
	}
	if workers <= 1 {
		decode(0, len(entries))
	} else {
		var wg sync.WaitGroup
		for w := range workers {
			wg.Go(func() { decode(w*len(entries)/workers, (w+1)*len(entries)/workers) })
		}
		wg.Wait()
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// parallelDecodeMin is the number of lazy entries each goroutine of MaterializeAll decodes at least.
// pig additive (D112): see parallelReadMinLines in internal/codingagent/session_store.go.
const parallelDecodeMin = 256

// AsMessage is entry as a MessageEntry: ok is false for any other entry and for a message entry whose record does not decode. A LazyEntry
// is read and decoded; err reports a record that changed on disk.
func AsMessage(entry SessionEntry) (message MessageEntry, ok bool, err error) {
	if entry, err = Materialize(entry); err != nil {
		return MessageEntry{}, false, err
	}
	message, ok = entry.(MessageEntry)
	return message, ok, nil
}

// IsUndescribed reports whether entry is a RawEntry: a record no member of the union describes. A LazyEntry is read and decoded to tell.
func IsUndescribed(entry SessionEntry) (bool, error) {
	entry, err := Materialize(entry)
	if err != nil {
		return false, err
	}
	_, undescribed := entry.(RawEntry)
	return undescribed, nil
}

// IsLazy reports whether entry is a LazyEntry: a message entry whose base members came from an index and whose record has not been read.
func IsLazy(entry SessionEntry) bool {
	_, lazy := entry.(*LazyEntry)
	return lazy
}
