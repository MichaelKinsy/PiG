// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package counts is the Durable core's deterministic count gate (Durable plan 2, item 9) and its flight recorder format.
//
// A run of the production native stack (sqlhost on SQLite, the driver, the bench workload on a fixture of N turns) is
// recorded as the stream of everything the core was given: its events, the answers to its pig.read calls and the ids
// pig.uuidv7 returned. The core is a pure function of that stream (ABI section 3, ADR D8: the clock and randomness are inputs),
// so Replay drives the same core again with no storage, no model and no goroutines, as the native build and as the Go and
// TinyGo Wasm builds, and counts what it costs: rows read and written, crossings, bytes allocated, mallocs, the live set, the
// Wasm linear memory high-water and, on an instrumented module, Wasm instructions. Every count is a function of the stream
// and the binary, never of the clock or the machine. A replay also compares each step with the step the recorded run produced,
// so a count is never reported for a core that behaved differently.
//
// The same stream is the deterministic replay of a trap report (host/cf/src/observe.ts writes it): a stream captured from a
// failing Durable Object replays here under a debugger.
package counts

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// Item kinds of a recording. A recording is a sequence of items, each u8 kind, u32 body length, body (little endian).
const (
	// ItemABI is the abi_id the core spoke: 64 ASCII bytes, first in the file.
	ItemABI byte = iota + 1
	// ItemEvent is one event delivered to the core: u8 event kind, f64 now, the wire payload (abi.Event.Wire).
	ItemEvent
	// ItemRead is one pig.read call the core made during the step of the last event: u32 request length, the request
	// (abi.AppendReadRequest), the answer (abi.AppendAnswer).
	ItemRead
	// ItemUUID is one pig.uuidv7 value the core asked for during the step: 36 ASCII bytes.
	ItemUUID
	// ItemStep is the SHA-256 of the step the core returned for the last event, in its wire form (abi.AppendStep).
	ItemStep
	// ItemMark names a boundary of the workload: u8 name bytes.
	ItemMark
	// ItemRowsChanged is the rows the host's store changed applying the commits of the last step: u64. It is a record of the
	// host, not an input of the core.
	ItemRowsChanged
)

// Item is one decoded item.
type Item struct {
	Kind byte
	Body []byte
}

// Recorder writes a recording. Its methods are safe for concurrent use: the owner goroutine of a host records events, and the
// workload records marks between turns.
type Recorder struct {
	mu  sync.Mutex
	w   *bufio.Writer
	f   io.Closer
	err error
}

// NewRecorder starts a recording at path.
func NewRecorder(path string) (*Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	r := &Recorder{w: bufio.NewWriterSize(f, 1<<20), f: f}
	r.item(ItemABI, []byte(abi.ID))
	return r, r.err
}

func (r *Recorder) item(kind byte, parts ...[]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	head := binary.LittleEndian.AppendUint32([]byte{kind}, uint32(n))
	if _, r.err = r.w.Write(head); r.err != nil {
		return
	}
	for _, p := range parts {
		if _, r.err = r.w.Write(p); r.err != nil {
			return
		}
	}
}

// Mark records a boundary of the workload.
func (r *Recorder) Mark(name string) { r.item(ItemMark, []byte(name)) }

// RowsChanged records the rows the store changed for the commits of the last step.
func (r *Recorder) RowsChanged(n uint64) {
	r.item(ItemRowsChanged, binary.LittleEndian.AppendUint64(nil, n))
}

// UUID wraps the host's uuidv7 generator so each value the core takes is recorded.
func (r *Recorder) UUID(next func() string) func() string {
	return func() string {
		id := next()
		r.item(ItemUUID, []byte(id))
		return id
	}
}

// Close finishes the recording and returns the first error of any write.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.err
	if ferr := r.w.Flush(); err == nil {
		err = ferr
	}
	if cerr := r.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Stepper wraps a native session so the recording holds its events, its reads and its steps. It satisfies the sqlhost.Stepper
// of the host that drives it, and forwards SetReader.
func (r *Recorder) Stepper(inner *core.Session) *RecordingStepper {
	return &RecordingStepper{r: r, inner: inner}
}

// RecordingStepper is a core.Session that records what it is given.
type RecordingStepper struct {
	r     *Recorder
	inner *core.Session
}

// SetReader installs the host's read, recording each request and answer.
func (s *RecordingStepper) SetReader(read core.Reader) {
	s.inner.SetReader(func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
		rows, err := read(sql, params)
		if err != nil {
			s.r.mu.Lock()
			if s.r.err == nil {
				s.r.err = fmt.Errorf("counts: a pig.read failed and cannot be replayed: %w", err)
			}
			s.r.mu.Unlock()
			return nil, err
		}
		req := abi.AppendReadRequest(nil, sql, params)
		cols := 0
		if len(rows) > 0 {
			cols = len(rows[0])
		}
		answer := abi.AppendAnswer(nil, uint8(cols), rows)
		s.r.item(ItemRead, binary.LittleEndian.AppendUint32(nil, uint32(len(req))), req, answer)
		return rows, nil
	})
}

// Step records the event, delivers it and records the digest of the step.
func (s *RecordingStepper) Step(ev abi.Event) *abi.Step {
	s.r.item(ItemEvent, []byte{byte(ev.Kind)}, binary.LittleEndian.AppendUint64(nil, math.Float64bits(ev.Now)), ev.Wire())
	step := s.inner.Step(ev)
	sum := sha256.Sum256(abi.AppendStep(nil, step))
	s.r.item(ItemStep, sum[:])
	return step
}

// Recording is a loaded recording, grouped by event.
type Recording struct {
	ABI     string
	Entries []Entry
	// Marks[i] names the boundary before Entries[At]; a mark after the last event has At == len(Entries).
	Marks []Mark
}

// Mark is a named boundary of the workload.
type Mark struct {
	Name string
	At   int
}

// Entry is one event with what the core asked for while it ran and what it returned.
type Entry struct {
	Kind   abi.EventKind
	Now    float64
	Wire   []byte
	Reads  []ReadCall
	UUIDs  []string
	Digest [sha256.Size]byte
	// Verified is whether the recording holds the step the core returned. A recording the host's flight recorder wrote does
	// not (it never hashes a step): its replay runs the core on the inputs and compares nothing.
	Verified    bool
	RowsChanged uint64
}

// ReadCall is one pig.read call and the host's answer.
type ReadCall struct {
	Request []byte
	Answer  []byte
	Rows    int // rows in the answer
}

// Load reads and groups a recording. Slices alias the file's bytes.
func Load(path string) (*Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse groups a recording held in memory. Slices of the result alias data.
func Parse(data []byte) (*Recording, error) {
	rec := &Recording{}
	var cur *Entry
	for i := 0; i < len(data); {
		if len(data)-i < 5 {
			return nil, errors.New("counts: truncated item header")
		}
		kind, n := data[i], int(binary.LittleEndian.Uint32(data[i+1:]))
		i += 5
		if len(data)-i < n {
			return nil, fmt.Errorf("counts: item of kind %d runs past the end of the recording", kind)
		}
		body := data[i : i+n]
		i += n
		switch kind {
		case ItemABI:
			rec.ABI = string(body)
		case ItemEvent:
			if len(body) < 9 {
				return nil, errors.New("counts: short event item")
			}
			rec.Entries = append(rec.Entries, Entry{Kind: abi.EventKind(body[0]), Now: math.Float64frombits(binary.LittleEndian.Uint64(body[1:])), Wire: body[9:]})
			cur = &rec.Entries[len(rec.Entries)-1]
		case ItemRead:
			if cur == nil || len(body) < 4 {
				return nil, errors.New("counts: a read outside an event")
			}
			rl := int(binary.LittleEndian.Uint32(body))
			if len(body) < 4+rl+5 {
				return nil, errors.New("counts: short read item")
			}
			answer := body[4+rl:]
			cur.Reads = append(cur.Reads, ReadCall{Request: body[4 : 4+rl], Answer: answer, Rows: int(binary.LittleEndian.Uint32(answer))})
		case ItemUUID:
			if cur == nil {
				return nil, errors.New("counts: a uuid outside an event")
			}
			cur.UUIDs = append(cur.UUIDs, string(body))
		case ItemStep:
			if cur == nil || len(body) != sha256.Size {
				return nil, errors.New("counts: a step digest outside an event")
			}
			copy(cur.Digest[:], body)
			cur.Verified = true
		case ItemMark:
			rec.Marks = append(rec.Marks, Mark{Name: string(body), At: len(rec.Entries)})
		case ItemRowsChanged:
			if cur == nil || len(body) != 8 {
				return nil, errors.New("counts: rows changed outside an event")
			}
			cur.RowsChanged += binary.LittleEndian.Uint64(body)
		default:
			return nil, fmt.Errorf("counts: unknown item kind %d", kind)
		}
	}
	if rec.ABI != abi.ID {
		return nil, fmt.Errorf("counts: the recording is of abi_id %q, this build speaks %q", rec.ABI, abi.ID)
	}
	return rec, nil
}
