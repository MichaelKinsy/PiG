// SPDX-License-Identifier: MIT

package abi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
)

// Value is a bound parameter or a returned column (ABI section 4.3). Kind comes last, so the value is 32 bytes on wasm32
// (40 with Kind first); a 20,000-task commit binds seven per task.
type Value struct {
	Int   int64
	Float float64
	Bytes []byte // text (UTF-8) or blob; read-only, so values may share one array
	Kind  ValueKind
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
		return bytes.Equal(v.Bytes, w.Bytes)
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
	Kind NoticeKind
	// Origin is the host async context the notice was published from (ABI section 5.4): the host runs its listeners in it. 0: none.
	Origin  uint32
	Payload []byte
	// Chunks, when not nil, is the payload in blocks (see Chunks), and Payload is nil. A decoded step never has it.
	Chunks Chunks
}

// Data returns the payload in one slice: Payload, or a copy of the Chunks' concatenation.
func (n *Notice) Data() []byte { return joinPayload(n.Payload, n.Chunks) }

// Effect is work the host starts after the step's commits are applied.
type Effect struct {
	ID   uint32
	Kind EffectKind
	// Origin is the host async context the effect starts in (ABI section 5.4). 0: none; a hops effect always has 0.
	Origin  uint32
	Payload []byte
	// Chunks, when not nil, is the payload in blocks (see Chunks), and Payload is nil. A decoded step never has it.
	Chunks Chunks
}

// Data returns the payload in one slice: Payload, or a copy of the Chunks' concatenation.
func (e *Effect) Data() []byte { return joinPayload(e.Payload, e.Chunks) }

func joinPayload(payload []byte, chunks Chunks) []byte {
	if chunks == nil {
		return payload
	}
	return bytes.Join(chunks, nil)
}

// payloadLen is the length of a notice's or effect's payload.
func payloadLen(payload []byte, chunks Chunks) int {
	if chunks == nil {
		return len(payload)
	}
	return chunks.Len()
}

// Step is the result of one event.
type Step struct {
	Status  Status
	Commits []Commit
	Reads   []Read
	Notices []Notice
	Effects []Effect
	// Order lists the notices and effects in Pi's synchronous order (ABI section 5.3). Empty means all notices, then all effects.
	Order []OrderRef
	Err   []byte // JSON, only when Status != StatusOK
}

// Region of an OrderRef.
const (
	OrderNotice uint8 = iota
	OrderEffect
)

// OrderRef names one notice (OrderNotice) or effect (OrderEffect) of a step by its index in that list.
type OrderRef struct {
	Region uint8
	Index  uint16
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

func valueSize(v *Value) int {
	switch v.Kind {
	case ValueNull:
		return 1
	case ValueInt, ValueFloat:
		return 9
	}
	return 5 + len(v.Bytes)
}

func paramsSize(params []Value) int {
	n := 1
	for i := range params {
		n += valueSize(&params[i])
	}
	return n
}

// StepSize is the length of AppendStep's wire form of s, so a binding sizes its output buffer once for a step that
// carries a large payload.
func StepSize(s *Step) int {
	n := 13
	for i := range s.Commits {
		n += 14
		for j := range s.Commits[i].Stmts {
			n += 2 + paramsSize(s.Commits[i].Stmts[j].Params)
		}
	}
	for i := range s.Reads {
		n += 6 + paramsSize(s.Reads[i].Params)
	}
	for i := range s.Notices {
		n += 9 + payloadLen(s.Notices[i].Payload, s.Notices[i].Chunks)
	}
	for i := range s.Effects {
		n += 13 + payloadLen(s.Effects[i].Payload, s.Effects[i].Chunks)
	}
	n += 2
	if len(s.Notices) != 0 && len(s.Effects) != 0 {
		n += 3 * len(s.Order)
	}
	if s.Status != StatusOK {
		n += 4 + len(s.Err)
	}
	return n
}

// AppendStep appends the wire form of s (ABI section 4.4).
func AppendStep(dst []byte, s *Step) []byte {
	dst, _ = appendStep(dst, s, 0, nil)
	return dst
}

// Part is one piece of a step's wire form: Ext when it is not nil, else bytes [Off, End) of the buffer AppendStepParts
// appended to.
type Part struct {
	Off, End int
	Ext      []byte
}

// AppendStepParts appends the wire form of s as AppendStep does, except that the payload of a notice or effect of at
// least big bytes, and each block of a payload in Chunks, is not copied: it is a part of its own, between the parts of
// dst around it. The concatenation of the parts is AppendStep's wire form, so a binding hands a model request (the whole
// transcript, megabytes) to its host from where the core built it. StepSizeInline(s, big) is the length appended to dst.
func AppendStepParts(dst []byte, s *Step, big int, parts []Part) ([]byte, []Part) {
	return appendStep(dst, s, big, parts)
}

// StepSizeInline is the length AppendStepParts appends to dst for s and big: StepSize less the payloads it does not
// copy.
func StepSizeInline(s *Step, big int) int {
	n := StepSize(s)
	for i := range s.Notices {
		n -= s.Notices[i].Chunks.Len()
		if l := len(s.Notices[i].Payload); l >= big {
			n -= l
		}
	}
	for i := range s.Effects {
		n -= s.Effects[i].Chunks.Len()
		if l := len(s.Effects[i].Payload); l >= big {
			n -= l
		}
	}
	return n
}

// appendStep is AppendStep; with big > 0 it is AppendStepParts.
func appendStep(dst []byte, s *Step, big int, parts []Part) ([]byte, []Part) {
	base := len(dst)
	sp := stepParts{big: big, parts: parts, from: base}
	dst = append(dst, 0, 0, 0, 0, byte(s.Status))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Commits)))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Reads)))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Notices)))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(s.Effects)))
	for i := range s.Commits {
		dst = appendCommit(dst, &s.Commits[i])
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
		dst = binary.LittleEndian.AppendUint32(dst, n.Origin)
		dst = binary.LittleEndian.AppendUint32(dst, uint32(payloadLen(n.Payload, n.Chunks)))
		dst = sp.payload(dst, n.Payload, n.Chunks)
	}
	for i := range s.Effects {
		e := &s.Effects[i]
		dst = binary.LittleEndian.AppendUint32(dst, e.ID)
		dst = append(dst, byte(e.Kind))
		dst = binary.LittleEndian.AppendUint32(dst, e.Origin)
		dst = binary.LittleEndian.AppendUint32(dst, uint32(payloadLen(e.Payload, e.Chunks)))
		dst = sp.payload(dst, e.Payload, e.Chunks)
	}
	order := s.Order
	if len(s.Notices) == 0 || len(s.Effects) == 0 {
		order = nil // one region keeps its own order
	}
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(order)))
	for _, o := range order {
		dst = append(dst, o.Region)
		dst = binary.LittleEndian.AppendUint16(dst, o.Index)
	}
	if s.Status != StatusOK {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(s.Err)))
		dst = append(dst, s.Err...)
	}
	binary.LittleEndian.PutUint32(dst[base:], uint32(len(dst)-base-4+sp.ext))
	if big > 0 && len(dst) > sp.from {
		sp.parts = append(sp.parts, Part{Off: sp.from, End: len(dst)})
	}
	return dst, sp.parts
}

// appendCommit appends the wire form of one commit: u32 length of the rest, i64 seq, u16 statement count, statements.
func appendCommit(dst []byte, c *Commit) []byte {
	lenAt := len(dst)
	dst = append(dst, 0, 0, 0, 0)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(c.Seq))
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(c.Stmts)))
	for j := range c.Stmts {
		dst = binary.LittleEndian.AppendUint16(dst, c.Stmts[j].SQL)
		dst = appendParams(dst, c.Stmts[j].Params)
	}
	binary.LittleEndian.PutUint32(dst[lenAt:], uint32(len(dst)-lenAt-4))
	return dst
}

// commit parses one commit written by appendCommit.
func (r *reader) commit() (Commit, error) {
	body := &reader{b: r.take(int(r.u32()))}
	c := Commit{Seq: int64(body.u64())}
	n := int(body.u16())
	for j := 0; j < n && body.err == nil; j++ {
		c.Stmts = append(c.Stmts, Stmt{SQL: body.u16(), Params: body.params()})
	}
	if body.err != nil {
		return Commit{}, body.err
	}
	if len(body.b) != 0 {
		return Commit{}, errors.New("abi: trailing bytes in commit")
	}
	return c, r.err
}

// Hops is the payload of a hops effect (ABI section 5.3): the host runs N microtask hops and then sends hopped; after hop At
// (0 < At < N) it applies Commits, in order, as the core would have had it emit them in the step of a hopped event at that
// hop. At is 0 and Commits empty for a chain that carries none.
type Hops struct {
	N, At   uint32
	Commits []Commit
}

// HopsSize is the length of AppendHops' wire form of h.
func HopsSize(h Hops) int {
	n := 8
	for i := range h.Commits {
		n += 14
		for j := range h.Commits[i].Stmts {
			n += 2 + paramsSize(h.Commits[i].Stmts[j].Params)
		}
	}
	return n
}

// AppendHops appends the wire form of a hops payload: u32 n, u32 at, then the commits, each as in a step.
func AppendHops(dst []byte, h Hops) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, h.N)
	dst = binary.LittleEndian.AppendUint32(dst, h.At)
	for i := range h.Commits {
		dst = appendCommit(dst, &h.Commits[i])
	}
	return dst
}

// DecodeHops parses a hops payload. Slices in the result alias b.
func DecodeHops(b []byte) (Hops, error) {
	r := &reader{b: b}
	h := Hops{N: r.u32(), At: r.u32()}
	for len(r.b) > 0 && r.err == nil {
		c, err := r.commit()
		if err != nil {
			return Hops{}, err
		}
		h.Commits = append(h.Commits, c)
	}
	switch {
	case r.err != nil:
		return Hops{}, r.err
	case h.N == 0:
		return Hops{}, errors.New("abi: hops with n = 0")
	case (h.At == 0) != (len(h.Commits) == 0) || (h.At != 0 && h.At >= h.N):
		return Hops{}, errors.New("abi: hops commits must come with 0 < at < n")
	}
	return h, nil
}

// stepParts is appendStep's progress through the parts: the bytes of dst from from on are not a part yet, and ext is the
// length of the payloads it did not copy.
type stepParts struct {
	big       int
	parts     []Part
	from, ext int
}

// payload appends a notice's or effect's payload to dst: b, or chunks when not nil. A payload of big bytes or more, and
// with big > 0 every block of chunks, becomes a part of its own instead.
func (sp *stepParts) payload(dst, b []byte, chunks Chunks) []byte {
	if chunks == nil {
		if sp.big <= 0 || len(b) < sp.big {
			return append(dst, b...)
		}
		return sp.own(dst, b)
	}
	for _, c := range chunks {
		if sp.big <= 0 {
			dst = append(dst, c...)
		} else {
			dst = sp.own(dst, c)
		}
	}
	return dst
}

// own ends the part of dst in progress and makes b a part of its own.
func (sp *stepParts) own(dst, b []byte) []byte {
	if len(dst) > sp.from {
		sp.parts = append(sp.parts, Part{Off: sp.from, End: len(dst)})
	}
	sp.parts = append(sp.parts, Part{Ext: b})
	sp.from, sp.ext = len(dst), sp.ext+len(b)
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
		c, err := r.commit()
		if err != nil {
			return nil, err
		}
		s.Commits = append(s.Commits, c)
	}
	for i := 0; i < nReads && r.err == nil; i++ {
		s.Reads = append(s.Reads, Read{ID: r.u32(), SQL: r.u16(), Params: r.params()})
	}
	for i := 0; i < nNotices && r.err == nil; i++ {
		kind := NoticeKind(r.u8())
		origin := r.u32()
		s.Notices = append(s.Notices, Notice{Kind: kind, Origin: origin, Payload: r.take(int(r.u32()))})
	}
	for i := 0; i < nEffects && r.err == nil; i++ {
		id := r.u32()
		kind := EffectKind(r.u8())
		origin := r.u32()
		s.Effects = append(s.Effects, Effect{ID: id, Kind: kind, Origin: origin, Payload: r.take(int(r.u32()))})
	}
	nOrder := int(r.u16())
	for i := 0; i < nOrder && r.err == nil; i++ {
		region := r.u8()
		idx := r.u16()
		if r.err == nil && ((region == OrderNotice && int(idx) >= nNotices) || (region == OrderEffect && int(idx) >= nEffects) || region > OrderEffect) {
			return nil, errors.New("abi: step order names a missing notice or effect")
		}
		s.Order = append(s.Order, OrderRef{Region: region, Index: idx})
	}
	if nOrder != 0 && nOrder != nNotices+nEffects && r.err == nil {
		return nil, errors.New("abi: step order must list every notice and effect")
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

// rowsChunk is the largest chunk DecodeRows and DecodeAnswer copy text and blob values into. A chunk is never longer than the
// payload left to copy from (the value being copied and the unread bytes after it), so a small answer, and the tail of a large
// one, gets a chunk no longer than itself: a record that keeps one value keeps its whole chunk under TinyGo's collector.
const rowsChunk = 16 << 10

// DecodeRows parses the payload of a rows event. Text and blob values are copies in chunks of a few KiB, not slices of b:
// the host reuses b for the next event, and a record the core keeps would otherwise keep a pointer into a buffer of
// hundreds of KiB, which under TinyGo costs the whole buffer on every collection (the collector finds the start of the
// object a pointer lands in by walking its blocks).
func DecodeRows(b []byte) ([]ReadRows, error) {
	var chunk []byte
	r := &reader{b: b}
	own := func(v []byte) []byte {
		if len(v) == 0 {
			return nil
		}
		if cap(chunk)-len(chunk) < len(v) {
			// What is left of the payload bounds what the remaining values can need: a one-row answer of a few hundred
			// bytes must not allocate a whole chunk.
			chunk = make([]byte, 0, max(len(v), min(rowsChunk, len(v)+len(r.b))))
		}
		at := len(chunk)
		chunk = append(chunk, v...)
		return chunk[at:len(chunk):len(chunk)]
	}
	n := int(r.u16())
	out := make([]ReadRows, 0, n)
	for i := 0; i < n && r.err == nil; i++ {
		a := ReadRows{ID: r.u32()}
		rows := int(r.u32())
		a.Cols = r.u8()
		for j := 0; j < rows && r.err == nil; j++ {
			row := make([]Value, 0, a.Cols)
			for c := 0; c < int(a.Cols) && r.err == nil; c++ {
				v := r.value()
				v.Bytes = own(v.Bytes)
				row = append(row, v)
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

// TimerClass is the u8 of a timer effect: what the timer is for, which decides how a host arms it.
type TimerClass uint8

// Timer classes.
const (
	// TimerVolatile paces work already held by a stream or tool (a throttle window): a process timer; it may be lost.
	TimerVolatile TimerClass = 0
	// TimerDurable resumes a sleeping task: re-derived from its checkpoint on reopen, and kept by the object alarm of a host that
	// wakes its process itself.
	TimerDurable TimerClass = 1
	// TimerBackground only frees memory (an idle context or document expiring): a host may decline it, as one that cannot
	// keep a pending timer without keeping its process alive does; the core frees that memory at later commits instead.
	TimerBackground TimerClass = 2
)

// TimerEffect encodes the payload of a timer effect.
func TimerEffect(timerID uint32, at float64, class TimerClass) []byte {
	out := binary.LittleEndian.AppendUint32(nil, timerID)
	out = binary.LittleEndian.AppendUint64(out, math.Float64bits(at))
	return append(out, byte(class))
}

// U32Payload encodes a payload that is one u32 (timer_clear, cancel).
func U32Payload(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// F64Payload encodes a payload that is one f64 (liveness).
func F64Payload(v float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v))
}

// AppendReadRequest appends the request of the pig.read import (ABI section 4.2): u16 sqlId, u8 nParams, value*.
func AppendReadRequest(dst []byte, sql uint16, params []Value) []byte {
	dst = binary.LittleEndian.AppendUint16(dst, sql)
	return appendParams(dst, params)
}

// DecodeReadRequest parses a pig.read request.
func DecodeReadRequest(b []byte) (uint16, []Value, error) {
	r := &reader{b: b}
	sql := r.u16()
	params := r.params()
	if r.err != nil {
		return 0, nil, r.err
	}
	if len(r.b) != 0 {
		return 0, nil, errors.New("abi: trailing bytes in read request")
	}
	return sql, params, nil
}

// AppendAnswer appends the answer of the pig.read import: u32 nRows, u8 nCols, value{nRows*nCols}.
func AppendAnswer(dst []byte, cols uint8, rows [][]Value) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(rows)))
	dst = append(dst, cols)
	for _, row := range rows {
		for _, v := range row {
			dst = AppendValue(dst, v)
		}
	}
	return dst
}

// DecodeAnswer parses a pig.read answer. Text and blob values are copies, as DecodeRows makes them: the binding reuses b.
func DecodeAnswer(b []byte) ([][]Value, error) {
	var chunk []byte
	r := &reader{b: b}
	own := func(v []byte) []byte {
		if len(v) == 0 {
			return nil
		}
		if cap(chunk)-len(chunk) < len(v) {
			// What is left of the payload bounds what the remaining values can need: a one-row answer of a few hundred
			// bytes must not allocate a whole chunk.
			chunk = make([]byte, 0, max(len(v), min(rowsChunk, len(v)+len(r.b))))
		}
		at := len(chunk)
		chunk = append(chunk, v...)
		return chunk[at:len(chunk):len(chunk)]
	}
	n := int(r.u32())
	cols := int(r.u8())
	out := make([][]Value, 0, min(n, len(b)))
	for j := 0; j < n && r.err == nil; j++ {
		row := make([]Value, 0, cols)
		for c := 0; c < cols && r.err == nil; c++ {
			v := r.value()
			v.Bytes = own(v.Bytes)
			row = append(row, v)
		}
		out = append(out, row)
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(r.b) != 0 {
		return nil, errors.New("abi: trailing bytes in read answer")
	}
	return out, nil
}
