// SPDX-License-Identifier: MIT

package abi

// Builder accumulates the step of one event. Every core package that commits, reads, publishes or starts an effect
// writes into the one Builder of its session; nothing else produces step content. A Builder lives as long as its session:
// Reset clears the step between events and keeps the ID counters, so effect, read and timer IDs are unique for the life
// of the handle (ABI section 3 invariant 6).
//
// Ordering rules the builder enforces for its callers (ABI section 3):
//   - commits keep the order of Commit calls;
//   - a step with reads carries no commits and no effects (Read panics otherwise; that is a core bug);
//   - notices and effects keep call order and are applied by the host after every commit of the step.
type Builder struct {
	step       Step
	nextEffect uint32
	nextRead   uint32
	nextTimer  uint32
}

// NewBuilder returns a builder whose first effect, read and timer IDs are 1.
func NewBuilder() *Builder { return &Builder{nextEffect: 1, nextRead: 1, nextTimer: 1} }

// Reset starts the step of the next event.
func (b *Builder) Reset() {
	b.step.Status = StatusOK
	b.step.Commits = b.step.Commits[:0]
	b.step.Reads = b.step.Reads[:0]
	b.step.Notices = b.step.Notices[:0]
	b.step.Effects = b.step.Effects[:0]
	b.step.Err = nil
}

// Step returns the step built since Reset. It is valid until the next Reset.
func (b *Builder) Step() *Step { return &b.step }

// Commit appends one SQL transaction. seq is the next_seq value it consumes, or -1 for a sidecar-only commit. The
// statements are owned by the builder from here on.
func (b *Builder) Commit(seq int64, stmts []Stmt) {
	if len(b.step.Reads) != 0 {
		panic("abi: commit in a read step")
	}
	b.step.Commits = append(b.step.Commits, Commit{Seq: seq, Stmts: stmts})
}

// Read asks the host to run statement sql with params and returns the read ID its rows event answers.
func (b *Builder) Read(sql uint16, params []Value) uint32 {
	if len(b.step.Commits) != 0 || len(b.step.Effects) != 0 {
		panic("abi: read in a step with commits or effects")
	}
	id := b.nextRead
	b.nextRead++
	b.step.Reads = append(b.step.Reads, Read{ID: id, SQL: sql, Params: params})
	return id
}

// Notice publishes payload after the step's commits.
func (b *Builder) Notice(kind NoticeKind, payload []byte) {
	b.step.Notices = append(b.step.Notices, Notice{Kind: kind, Payload: payload})
}

// Effect starts an effect after the step's commits and returns its ID.
func (b *Builder) Effect(kind EffectKind, payload []byte) uint32 {
	if len(b.step.Reads) != 0 {
		panic("abi: effect in a read step")
	}
	id := b.nextEffect
	b.nextEffect++
	b.step.Effects = append(b.step.Effects, Effect{ID: id, Kind: kind, Payload: payload})
	return id
}

// Timer arms a timer effect at the absolute Harness time at and returns the timer ID a timer event carries back.
// durable timers are re-derived from checkpoints on reopen and keep the object alarm; volatile ones may be lost (ADR D8).
func (b *Builder) Timer(at float64, durable bool) uint32 {
	id := b.nextTimer
	b.nextTimer++
	b.Effect(EffectTimer, TimerEffect(id, at, durable))
	return id
}

// ClearTimer cancels a timer armed by Timer.
func (b *Builder) ClearTimer(id uint32) { b.Effect(EffectTimerClear, U32Payload(id)) }

// Cancel aborts the signal of a running effect.
func (b *Builder) Cancel(effectID uint32) { b.Effect(EffectCancel, U32Payload(effectID)) }

// Reject makes the step a rejected step: the session's state must be unchanged, and every commit, read, notice and
// effect added so far is dropped. errJSON is Pi's error as {"name","message"}.
func (b *Builder) Reject(errJSON []byte) {
	b.Reset()
	b.step.Status = StatusRejected
	b.step.Err = errJSON
}

// Fatal makes the step fatal: the host discards the handle (ABI section 4.6).
func (b *Builder) Fatal(errJSON []byte) {
	b.Reset()
	b.step.Status = StatusFatal
	b.step.Err = errJSON
}

// ErrorJSON renders Pi's {"name","message"} error payload. name must be an identifier; message is escaped minimally
// (quotes, backslashes and control characters), as JSON.stringify does.
func ErrorJSON(name, message string) []byte {
	out := make([]byte, 0, len(name)+len(message)+24)
	out = append(out, `{"name":"`...)
	out = append(out, name...)
	out = append(out, `","message":"`...)
	for i := 0; i < len(message); i++ {
		c := message[i]
		switch {
		case c == '"' || c == '\\':
			out = append(out, '\\', c)
		case c == '\n':
			out = append(out, '\\', 'n')
		case c == '\r':
			out = append(out, '\\', 'r')
		case c == '\t':
			out = append(out, '\\', 't')
		case c < 0x20:
			out = append(out, '\\', 'u', '0', '0', "0123456789abcdef"[c>>4], "0123456789abcdef"[c&15])
		default:
			out = append(out, c)
		}
	}
	return append(out, `"}`...)
}
