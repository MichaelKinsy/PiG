// SPDX-License-Identifier: MIT

package abi

// Builder accumulates the step of one event. Every core package that commits, reads, publishes or starts an effect
// writes into the one Builder of its session; nothing else produces step content. A Builder lives as long as its session:
// Reset clears the step between events and keeps the ID counters, so effect, read and timer IDs are unique for the life
// of the handle (ABI section 3 invariant 6).
//
// Ordering rules the builder enforces for its callers (ABI section 3):
//   - commits keep the order of Commit calls;
//   - a step with reads carries no effects, and its commits precede its reads (Read, Effect and Commit panic otherwise;
//     that is a core bug); the host applies the commits before it runs the reads;
//   - notices and effects keep call order and are applied by the host after every commit of the step.
type Builder struct {
	// Origin, when set, reports the host async context of the code that is running (ABI section 5.4): the builder stamps
	// every notice and effect it is given with it. A core without it stamps 0.
	Origin     func() uint32
	step       Step
	nextEffect uint32
	nextRead   uint32
	nextTimer  uint32
}

// NewBuilder returns a builder whose first effect, read and timer IDs are 1.
func NewBuilder() *Builder { return &Builder{nextEffect: 1, nextRead: 1, nextTimer: 1} }

// Reset starts the step of the next event. The step's slices keep their arrays for the next event, and their elements
// are cleared first: an element past the new length is still scanned by the collector, so a commit's statements, a read's
// parameters or a notice's payload would stay alive until a later step happened to overwrite its slot. The commit of a
// 20,000-task transaction held about 22 MB that way on the TinyGo core, past its 96 MiB ceiling.
func (b *Builder) Reset() {
	b.step.Status = StatusOK
	clear(b.step.Commits)
	clear(b.step.Reads)
	clear(b.step.Notices)
	clear(b.step.Effects)
	b.step.Commits = b.step.Commits[:0]
	b.step.Reads = b.step.Reads[:0]
	b.step.Notices = b.step.Notices[:0]
	b.step.Effects = b.step.Effects[:0]
	b.step.Order = b.step.Order[:0]
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

// DetachCommit removes and returns the step's last commit (a core that holds a line job's statements until Pi's COMMIT hop).
// Its slot is cleared, as Reset clears the others: the holder decides how long the statements live.
func (b *Builder) DetachCommit() Commit {
	n := len(b.step.Commits) - 1
	last := b.step.Commits[n]
	b.step.Commits[n] = Commit{}
	b.step.Commits = b.step.Commits[:n]
	return last
}

// Read asks the host to run statement sql with params and returns the read ID its rows event answers. The host applies
// the step's commits before it runs the reads (ABI section 3, invariant 3), so a read sees them.
func (b *Builder) Read(sql uint16, params []Value) uint32 {
	if len(b.step.Effects) != 0 {
		panic("abi: read in a step with effects")
	}
	id := b.nextRead
	b.nextRead++
	b.step.Reads = append(b.step.Reads, Read{ID: id, SQL: sql, Params: params})
	return id
}

// Notice publishes payload after the step's commits.
func (b *Builder) Notice(kind NoticeKind, payload []byte) {
	b.notice(Notice{Kind: kind, Payload: payload})
}

// NoticeChunks publishes the payload built in c after the step's commits: in one slice when it is one block.
func (b *Builder) NoticeChunks(kind NoticeKind, c Chunks) {
	if len(c) == 1 {
		b.Notice(kind, c[0])
		return
	}
	b.notice(Notice{Kind: kind, Chunks: c})
}

func (b *Builder) notice(n Notice) {
	if b.Origin != nil {
		n.Origin = b.Origin()
	}
	b.step.Order = append(b.step.Order, OrderRef{Region: OrderNotice, Index: uint16(len(b.step.Notices))})
	b.step.Notices = append(b.step.Notices, n)
}

// Effect starts an effect after the step's commits and returns its ID.
func (b *Builder) Effect(kind EffectKind, payload []byte) uint32 {
	return b.effect(Effect{Kind: kind, Payload: payload}, true)
}

// EffectChunks starts an effect with the payload built in c, in one slice when it is one block, and returns its ID.
func (b *Builder) EffectChunks(kind EffectKind, c Chunks) uint32 {
	if len(c) == 1 {
		return b.Effect(kind, c[0])
	}
	return b.effect(Effect{Kind: kind, Chunks: c}, true)
}

// EffectBare is Effect for an effect the host starts in no context: a hops chain runs no host code, and the core keeps the origin of
// the work parked on it.
func (b *Builder) EffectBare(kind EffectKind, payload []byte) uint32 {
	return b.effect(Effect{Kind: kind, Payload: payload}, false)
}

// EffectChunksBare is EffectChunks for an effect the host starts in no context (EffectBare).
func (b *Builder) EffectChunksBare(kind EffectKind, c Chunks) uint32 {
	if len(c) == 1 {
		return b.EffectBare(kind, c[0])
	}
	return b.effect(Effect{Kind: kind, Chunks: c}, false)
}

func (b *Builder) effect(e Effect, stamp bool) uint32 {
	if len(b.step.Reads) != 0 {
		panic("abi: effect in a read step")
	}
	if stamp && b.Origin != nil {
		e.Origin = b.Origin()
	}
	e.ID = b.nextEffect
	b.nextEffect++
	b.step.Order = append(b.step.Order, OrderRef{Region: OrderEffect, Index: uint16(len(b.step.Effects))})
	b.step.Effects = append(b.step.Effects, e)
	return e.ID
}

// Timer arms a timer effect at the absolute Harness time at and returns the timer ID a timer event carries back.
// durable timers are re-derived from checkpoints on reopen and keep the object alarm; volatile ones may be lost (ADR D8);
// background ones only free memory and may be declined by the host.
func (b *Builder) Timer(at float64, class TimerClass) uint32 {
	id := b.nextTimer
	b.nextTimer++
	b.Effect(EffectTimer, TimerEffect(id, at, class))
	return id
}

// ClearTimer cancels a timer armed by Timer.
func (b *Builder) ClearTimer(id uint32) { b.Effect(EffectTimerClear, U32Payload(id)) }

// Cancel aborts the signal of a running effect.
func (b *Builder) Cancel(effectID uint32) { b.Effect(EffectCancel, U32Payload(effectID)) }

// Reject makes the step a rejected step: the session's state must be unchanged, and every commit, read, notice and
// effect added so far is dropped. errJSON is Pi's error as {"name","message"}.
//
//go:noinline
func (b *Builder) Reject(errJSON []byte) {
	b.Reset()
	b.step.Status = StatusRejected
	b.step.Err = errJSON
}

// Fatal makes the step fatal: the host discards the handle (ABI section 4.6).
//
//go:noinline
func (b *Builder) Fatal(errJSON []byte) {
	b.Reset()
	b.step.Status = StatusFatal
	b.step.Err = errJSON
}

// ErrorJSON renders Pi's {"name","message"} error payload. name must be an identifier; message is escaped minimally
// (quotes, backslashes and control characters), as JSON.stringify does.
//
//go:noinline
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
