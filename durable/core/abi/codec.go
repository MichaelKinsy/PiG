// SPDX-License-Identifier: MIT

package abi

import (
	"encoding/binary"
	"errors"
	"math"
	"strconv"
)

// Value is a bound parameter or a returned column (ABI section 4.3).
type Value struct {
	Kind  ValueKind
	Int   int64
	Float float64
	Bytes []byte // text (UTF-8) or blob
}

// Null, Int, Float, Text and Blob build values.
func Null() Value              { return Value{Kind: ValueNull} }
func Int(v int64) Value        { return Value{Kind: ValueInt, Int: v} }
func Float(v float64) Value    { return Value{Kind: ValueFloat, Float: v} }
func Text(v string) Value      { return Value{Kind: ValueText, Bytes: []byte(v)} }
func TextBytes(v []byte) Value { return Value{Kind: ValueText, Bytes: v} }
func Blob(v []byte) Value      { return Value{Kind: ValueBlob, Bytes: v} }
func (v Value) String() string { return string(v.Bytes) }

// Number returns an integer or float value as a float64. A JavaScript host cannot tell an integral REAL from an INTEGER,
// so a core must read numeric columns through Number, never by tag.
func (v Value) Number() float64 {
	if v.Kind == ValueInt {
		return float64(v.Int)
	}
	return v.Float
}
func (v Value) Equal(w Value) bool {
	if v.Kind != w.Kind {
		return false
	}
	switch v.Kind {
	case ValueNull:
		return true
	case ValueInt:
		return v.Int == w.Int
	case ValueFloat:
		return math.Float64bits(v.Float) == math.Float64bits(w.Float)
	default:
		return string(v.Bytes) == string(w.Bytes)
	}
}

// Stmt is one statement of a commit: an index into the core's SQL table and its parameters.
type Stmt struct {
	SQL    uint16
	Params []Value
}

// Commit is one SQL transaction. Seq is the next_seq value it consumes, or -1 for a sidecar-only commit.
type Commit struct {
	Seq   int64
	Stmts []Stmt
}

// Read asks the host to run one query and answer it in a rows event.
type Read struct {
	ID     uint32
	SQL    uint16
	Params []Value
}

// Notice is published after the step's commits are applied.
type Notice struct {
	Kind    NoticeKind
	Payload []byte
}

// Effect is work the host starts after the step's commits are applied.
type Effect struct {
	ID      uint32
	Kind    EffectKind
	Payload []byte
}

// Step is the result of one event.
type Step struct {
	Status  Status
	Commits []Commit
	Reads   []Read
	Notices []Notice
	Effects []Effect
	Err     []byte // JSON, only when Status != StatusOK
}

// ReadRows answers one read.
type ReadRows struct {
	ID   uint32
	Cols uint8
	Rows [][]Value // each row has Cols values
}

// errShort is read-only.
var errShort = errors.New("abi: truncated input")

// AppendValue appends the wire form of v.
func AppendValue(dst []byte, v Value) []byte {
	dst = append(dst, byte(v.Kind))
	switch v.Kind {
	case ValueNull:
	case ValueInt:
		dst = binary.LittleEndian.AppendUint64(dst, uint64(v.Int))
	case ValueFloat:
		dst = binary.LittleEndian.AppendUint64(dst, math.Float64bits(v.Float))
	default:
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(v.Bytes)))
		dst = append(dst, v.Bytes...)
	}
	return dst
}

func appendParams(dst []byte, params []Value) []byte {
	dst = append(dst, byte(len(params)))
	for _, p := range params {
		dst = AppendValue(dst, p)
	}
	return dst
}

// AppendStep appends the wire form of s (ABI section 4.4).
func AppendStep(dst []byte, s *Step) []byte {
	base := len(dst)
	dst = append(dst, 0, 0, 0, 0, byte(s.Status))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Commits)))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Reads)))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Notices)))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Effects)))
	for i := range s.Commits {
		c := &s.Commits[i]
		lenAt := len(dst)
		dst = append(dst, 0, 0, 0, 0)
		dst = binary.LittleEndian.AppendUint64(dst, uint64(c.Seq))
		dst = binary.LittleEndian.AppendUint16(dst, uint16(len(c.Stmts)))
		for j := range c.Stmts {
			dst = binary.LittleEndian.AppendUint16(dst, c.Stmts[j].SQL)
			dst = appendParams(dst, c.Stmts[j].Params)
		}
		binary.LittleEndian.PutUint32(dst[lenAt:], uint32(len(dst)-lenAt-4))
	}
	for i := range s.Reads {
		r := &s.Reads[i]
		dst = binary.LittleEndian.AppendUint32(dst, r.ID)
		dst = binary.LittleEndian.AppendUint16(dst, r.SQL)
		dst = appendParams(dst, r.Params)
	}
	for i := range s.Notices {
		n := &s.Notices[i]
		dst = append(dst, byte(n.Kind))
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(n.Payload)))
		dst = append(dst, n.Payload...)
	}
	for i := range s.Effects {
		e := &s.Effects[i]
		dst = binary.LittleEndian.AppendUint32(dst, e.ID)
		dst = append(dst, byte(e.Kind))
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(e.Payload)))
		dst = append(dst, e.Payload...)
	}
	if s.Status != StatusOK {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(s.Err)))
		dst = append(dst, s.Err...)
	}
	binary.LittleEndian.PutUint32(dst[base:], uint32(len(dst)-base-4))
	return dst
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || len(r.b) < n {
		r.err = errShort
		return nil
	}
	out := r.b[:n:n]
	r.b = r.b[n:]
	return out
}
func (r *reader) u8() uint8 {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}
func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}
func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
func (r *reader) value() Value {
	kind := ValueKind(r.u8())
	switch kind {
	case ValueNull:
		return Value{}
	case ValueInt:
		return Value{Kind: kind, Int: int64(r.u64())}
	case ValueFloat:
		return Value{Kind: kind, Float: math.Float64frombits(r.u64())}
	case ValueText, ValueBlob:
		return Value{Kind: kind, Bytes: r.take(int(r.u32()))}
	default:
		r.err = errors.New("abi: unknown value tag " + strconv.Itoa(int(kind)))
		return Value{}
	}
}
func (r *reader) params() []Value {
	n := int(r.u8())
	out := make([]Value, 0, n)
	for i := 0; i < n && r.err == nil; i++ {
		out = append(out, r.value())
	}
	return out
}

// DecodeStep parses the wire form of a step. Slices in the result alias b.
func DecodeStep(b []byte) (*Step, error) {
	r := &reader{b: b}
	size := r.u32()
	if r.err == nil && int(size) != len(b)-4 {
		return nil, errors.New("abi: step size " + strconv.Itoa(int(size)) + " does not match " + strconv.Itoa(len(b)-4) + " bytes")
	}
	s := &Step{Status: Status(r.u8())}
	nCommits, nReads, nNotices, nEffects := int(r.u16()), int(r.u16()), int(r.u16()), int(r.u16())
	for i := 0; i < nCommits && r.err == nil; i++ {
		body := &reader{b: r.take(int(r.u32()))}
		c := Commit{Seq: int64(body.u64())}
		n := int(body.u16())
		for j := 0; j < n && body.err == nil; j++ {
			c.Stmts = append(c.Stmts, Stmt{SQL: body.u16(), Params: body.params()})
		}
		if body.err != nil {
			return nil, body.err
		}
		if len(body.b) != 0 {
			return nil, errors.New("abi: trailing bytes in commit")
		}
		s.Commits = append(s.Commits, c)
	}
	for i := 0; i < nReads && r.err == nil; i++ {
		s.Reads = append(s.Reads, Read{ID: r.u32(), SQL: r.u16(), Params: r.params()})
	}
	for i := 0; i < nNotices && r.err == nil; i++ {
		kind := NoticeKind(r.u8())
		s.Notices = append(s.Notices, Notice{Kind: kind, Payload: r.take(int(r.u32()))})
	}
	for i := 0; i < nEffects && r.err == nil; i++ {
		id := r.u32()
		kind := EffectKind(r.u8())
		s.Effects = append(s.Effects, Effect{ID: id, Kind: kind, Payload: r.take(int(r.u32()))})
	}
	if s.Status != StatusOK && r.err == nil {
		s.Err = r.take(int(r.u32()))
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(r.b) != 0 {
		return nil, errors.New("abi: trailing bytes in step")
	}
	return s, nil
}

// AppendRows appends the payload of a rows event (ABI section 4.5).
func AppendRows(dst []byte, answers []ReadRows) []byte {
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(answers)))
	for i := range answers {
		a := &answers[i]
		dst = binary.LittleEndian.AppendUint32(dst, a.ID)
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(a.Rows)))
		dst = append(dst, a.Cols)
		for _, row := range a.Rows {
			for _, v := range row {
				dst = AppendValue(dst, v)
			}
		}
	}
	return dst
}

// DecodeRows parses the payload of a rows event. Slices in the result alias b.
func DecodeRows(b []byte) ([]ReadRows, error) {
	r := &reader{b: b}
	n := int(r.u16())
	out := make([]ReadRows, 0, n)
	for i := 0; i < n && r.err == nil; i++ {
		a := ReadRows{ID: r.u32()}
		rows := int(r.u32())
		a.Cols = r.u8()
		for j := 0; j < rows && r.err == nil; j++ {
			row := make([]Value, 0, a.Cols)
			for c := 0; c < int(a.Cols) && r.err == nil; c++ {
				row = append(row, r.value())
			}
			a.Rows = append(a.Rows, row)
		}
		out = append(out, a)
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(r.b) != 0 {
		return nil, errors.New("abi: trailing bytes in rows event")
	}
	return out, nil
}

// SQLTable encodes the sql_table export: u16 n, then n x (u32 len, UTF-8).
func SQLTable(statements []string) []byte {
	out := binary.LittleEndian.AppendUint16(nil, uint16(len(statements)))
	for _, s := range statements {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(s)))
		out = append(out, s...)
	}
	return out
}

// Payload is a little-endian reader for the binary event payloads (effect IDs, timer IDs, phases).
type Payload struct{ r reader }

// NewPayload starts reading b.
func NewPayload(b []byte) *Payload { return &Payload{r: reader{b: b}} }

// U8 reads one byte.
func (p *Payload) U8() uint8 { return p.r.u8() }

// U32 reads a little-endian u32.
func (p *Payload) U32() uint32 { return p.r.u32() }

// F64 reads a little-endian f64.
func (p *Payload) F64() float64 { return math.Float64frombits(p.r.u64()) }

// Rest returns the unread bytes.
func (p *Payload) Rest() []byte { return p.r.take(len(p.r.b)) }

// Err reports a truncated payload.
func (p *Payload) Err() error { return p.r.err }

// TimerEffect encodes the payload of a timer effect.
func TimerEffect(timerID uint32, at float64, durable bool) []byte {
	out := binary.LittleEndian.AppendUint32(nil, timerID)
	out = binary.LittleEndian.AppendUint64(out, math.Float64bits(at))
	if durable {
		return append(out, 1)
	}
	return append(out, 0)
}

// U32Payload encodes a payload that is one u32 (timer_clear, cancel).
func U32Payload(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// F64Payload encodes a payload that is one f64 (liveness).
func F64Payload(v float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v))
}
