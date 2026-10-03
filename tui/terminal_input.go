package tui

// Ports packages/tui/src/terminal.ts.

import (
	"strings"
	"time"
)

const keyboardProtocolResponseFragmentTimeout = 150 * time.Millisecond

// TerminalInput owns framing and protocol-response deadlines for one input loop. The caller serializes Process, Flush, FlushPending, and Close, and selects C alongside raw input. Callbacks run synchronously in sequence order; the dispatch owner applies native modifier normalization once before focus routing.
type TerminalInput struct {
	terminal          *ProcessTerminal
	buffer            *StdinBuffer
	onInput           func(string)
	closed            bool
	negotiationBuffer string
	sequenceDeadline  time.Time
	fragmentDeadline  time.Time
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
			p.flushNegotiationBuffer()
		} else {
			p.sequenceDeadline = time.Time{}
			p.route(p.buffer.Flush())
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
		if p.closed {
			return
		}
		// Paste is a separate StdinBuffer event upstream; it bypasses negotiation without flushing an earlier prefix.
		if strings.HasPrefix(sequence, bracketedPasteStart) {
			p.forward(sequence)
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
	p.buffer.Clear()
	p.sequenceDeadline = time.Time{}
	p.clearNegotiationBuffer()
}
