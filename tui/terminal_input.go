package tui

// Ports packages/tui/src/terminal.ts.

import (
	"strings"
	"time"
)

const keyboardProtocolResponseFragmentTimeout = 150 * time.Millisecond

// owedReplyTailTimeout is how long after a forwarded bare `ESC[` fragment the rest of an owed reply is still absorbed.
// pig divergence (D143): Pi forwards the rest of a reply split across slow reads as printable key input (Pi #10657).
const owedReplyTailTimeout = time.Second

// TerminalInput owns framing and protocol-response deadlines for one input loop. The caller serializes Process, Flush, FlushPending, and Close, and selects C alongside raw input or waits until NextDeadline. Callbacks run synchronously in sequence order; the dispatch owner applies native modifier normalization once before focus routing.
type TerminalInput struct {
	terminal          *ProcessTerminal
	buffer            *StdinBuffer
	onInput           func(string)
	barrier           func()
	closed            bool
	negotiationBuffer string
	sequenceDeadline  time.Time
	fragmentDeadline  time.Time
	replyTail         string    // the forwarded fragment of an owed reply, extended by every absorbed continuation
	replyTailDeadline time.Time // while it is in the future, the rest of a split owed reply is absorbed
	timeout           inputTimer
	C                 <-chan time.Time
}

// NewTerminalInput constructs the process terminal's decoder for a caller-owned input loop.
func NewTerminalInput(onInput func(string)) *TerminalInput {
	return processTerminal.NewTerminalInput(onInput)
}

// NewTerminalInput constructs an input decoder bound to this terminal's protocol control output.
func (t *ProcessTerminal) NewTerminalInput(onInput func(string)) *TerminalInput {
	return &TerminalInput{terminal: t, buffer: newProcessStdinBuffer(), onInput: onInput}
}

// SetSequenceBarrier installs barrier, which runs before the decoder interprets a sequence after forwarding input: before each framed sequence and after a forwarded negotiation fragment. Pi's data listeners run synchronously before the next sequence changes terminal state (terminal.ts setupStdinBuffer); a caller whose onInput returns before the input's listeners settle waits for them in barrier.
func (p *TerminalInput) SetSequenceBarrier(barrier func()) {
	p.barrier = barrier
}

// NextDeadline reports when Flush next has a framing or negotiation deadline to dispatch, the zero time when none is pending. A caller that waits for raw input itself waits only until then, instead of selecting C.
func (p *TerminalInput) NextDeadline() time.Time {
	if p.closed {
		return time.Time{}
	}
	return p.nextDeadline()
}

func (p *TerminalInput) settleForwarded() {
	if p.barrier != nil {
		p.barrier()
	}
}

// Process decodes a UTF-8 terminal read and dispatches complete sequences. Input ready alongside a timeout is processed first, so continuations can finish their pending sequence.
func (p *TerminalInput) Process(data []byte) {
	if p.closed {
		return
	}
	p.route(p.buffer.ProcessTerminalBytes(data))
	p.sequenceDeadline = stdinBufferDeadline(p.buffer)
	p.armTimer()
}

// Flush dispatches expired framing and negotiation deadlines in chronological order.
func (p *TerminalInput) Flush() {
	for !p.closed {
		deadline := p.nextDeadline()
		if deadline.IsZero() || time.Now().Before(deadline) {
			break
		}
		if deadline.Equal(p.fragmentDeadline) {
			fragment := p.negotiationBuffer
			p.flushNegotiationBuffer()
			p.settleForwarded()
			p.armOwedReplyTail(fragment)
		} else {
			p.route(p.buffer.FlushExpired())
			// A reparsed control-string tail can leave a new remainder, which arms its own timeout as upstream process() does.
			p.sequenceDeadline = stdinBufferDeadline(p.buffer)
		}
	}
	p.armTimer()
}

// FlushPending delivers any remaining input when the source reaches EOF.
func (p *TerminalInput) FlushPending() {
	p.sequenceDeadline = time.Time{}
	p.route(p.buffer.Flush())
	p.flushNegotiationBuffer()
	p.stopTimer()
}

func isKeyboardProtocolNegotiationSequencePrefix(sequence string) bool {
	if sequence == "\x1b[" {
		return true
	}
	payload, ok := strings.CutPrefix(sequence, "\x1b[?")
	return ok && strings.Trim(payload, "0123456789;") == ""
}

func (p *TerminalInput) route(sequences []string) {
	for _, sequence := range sequences {
		if p.settleForwarded(); p.closed {
			return
		}
		// Paste is a separate StdinBuffer event upstream; it bypasses negotiation without flushing an earlier prefix.
		if strings.HasPrefix(sequence, bracketedPasteStart) {
			p.forward(sequence)
			continue
		}
		// upstream: terminal.ts data handler checks isProgramStatusReply before keyboard negotiation.
		if p.terminal.handleProgramStatusReply(sequence) {
			continue
		}
		if p.absorbOwedReplyTail(sequence) {
			continue
		}
		if p.negotiationBuffer != "" {
			combined := p.negotiationBuffer + sequence
			if p.terminal.handleKeyboardProtocolNegotiationSequence(combined) {
				p.clearNegotiationBuffer()
				continue
			}
			if isKeyboardProtocolNegotiationReply(combined) {
				// A reassembled reply that answers another query is forwarded whole.
				p.clearNegotiationBuffer()
				p.forward(combined)
				continue
			}
			if isKeyboardProtocolNegotiationSequencePrefix(combined) {
				p.setNegotiationBuffer(combined)
				continue
			}
			p.flushNegotiationBuffer()
			if p.closed {
				return
			}
			p.settleForwarded()
		}
		if p.terminal.handleKeyboardProtocolNegotiationSequence(sequence) {
			continue
		}
		if isKeyboardProtocolNegotiationSequencePrefix(sequence) {
			p.setNegotiationBuffer(sequence)
			continue
		}
		p.forward(sequence)
	}
}

func (p *TerminalInput) forward(sequence string) {
	if !p.closed && p.onInput != nil {
		p.onInput(sequence)
	}
}

func (p *TerminalInput) setNegotiationBuffer(sequence string) {
	p.negotiationBuffer = sequence
	// pig divergence (D143): a private CSI reply (`ESC[?`) cannot be keyboard input, so it waits for its final byte however long the
	// transport pauses (Pi #10657 reference fix). Only a bare `ESC[` is replayed as input after the fragment timeout.
	if strings.HasPrefix(sequence, "\x1b[?") {
		p.fragmentDeadline = time.Time{}
		return
	}
	p.fragmentDeadline = time.Now().Add(keyboardProtocolResponseFragmentTimeout)
}

func (p *TerminalInput) clearNegotiationBuffer() {
	p.negotiationBuffer = ""
	p.fragmentDeadline = time.Time{}
}

func (p *TerminalInput) flushNegotiationBuffer() {
	if p.negotiationBuffer == "" {
		return
	}
	sequence := p.negotiationBuffer
	p.clearNegotiationBuffer()
	p.forward(sequence)
}

// armOwedReplyTail starts absorbing the rest of a negotiation reply whose bare `ESC[` introducer the fragment timeout forwarded while a DA reply is owed. Pi's tests require that replay; an `ESC[?` prefix is never timed out.
func (p *TerminalInput) armOwedReplyTail(fragment string) {
	if fragment != "" && p.terminal.pendingKeyboardProtocolDeviceAttributes.Load() > 0 {
		p.replyTail = fragment
		//portlint:allow clock a wall-clock input deadline like the fragment flush's setTimeout (terminal.ts:376-381); TerminalInput holds no TUI.now render seam
		p.replyTailDeadline = time.Now().Add(owedReplyTailTimeout)
	}
}

// absorbOwedReplyTail consumes a sequence that continues the forwarded fragment towards a negotiation reply, settles the reply it completes, and reports whether it consumed the sequence.
func (p *TerminalInput) absorbOwedReplyTail(sequence string) bool {
	if p.replyTail == "" {
		return false
	}
	//portlint:allow clock the wall-clock deadline armOwedReplyTail set; TerminalInput holds no TUI.now render seam
	if !time.Now().Before(p.replyTailDeadline) {
		p.endOwedReplyTail()
		return false
	}
	combined := p.replyTail + sequence
	if p.terminal.handleKeyboardProtocolNegotiationSequence(combined) {
		p.endOwedReplyTail()
		return true
	}
	if isKeyboardProtocolNegotiationSequencePrefix(combined) {
		p.replyTail = combined
		//portlint:allow clock a wall-clock input deadline like the fragment flush's setTimeout (terminal.ts:376-381); TerminalInput holds no TUI.now render seam
		p.replyTailDeadline = time.Now().Add(owedReplyTailTimeout)
		return true
	}
	p.endOwedReplyTail()
	return false
}

func (p *TerminalInput) endOwedReplyTail() {
	p.replyTail = ""
	p.replyTailDeadline = time.Time{}
}

func (p *TerminalInput) nextDeadline() time.Time {
	if p.sequenceDeadline.IsZero() {
		return p.fragmentDeadline
	}
	if p.fragmentDeadline.IsZero() || p.sequenceDeadline.Before(p.fragmentDeadline) {
		return p.sequenceDeadline
	}
	return p.fragmentDeadline
}

func (p *TerminalInput) armTimer() {
	deadline := p.nextDeadline()
	if p.closed {
		deadline = time.Time{}
	}
	p.timeout.arm(deadline)
	p.C = p.timeout.C
}

func (p *TerminalInput) stopTimer() {
	p.timeout.stop()
	p.C = nil
}

// Close stops owned timers and discards pending input without dispatching it. It can be called from the input callback to stop the rest of a batch.
func (p *TerminalInput) Close() {
	p.closed = true
	p.stopTimer()
	p.buffer.Destroy()
	p.sequenceDeadline = time.Time{}
	p.clearNegotiationBuffer()
}
