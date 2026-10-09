// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) 2025 opentui
// SPDX-License-Identifier: MIT

package tui

// Ports packages/tui/src/stdin-buffer.ts.

import (
	"bytes"
	"cmp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const (
	escByte             = "\x1b"
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
	// defaultSequenceTimeout and defaultEscapeTimeout mirror upstream
	// DEFAULT_SEQUENCE_TIMEOUT_MS and DEFAULT_ESCAPE_TIMEOUT_MS.
	defaultSequenceTimeout = 50 * time.Millisecond
	defaultEscapeTimeout   = 10 * time.Millisecond
)

// StdinBuffer accumulates partial escape sequences across reads and emits JavaScript UTF-16 units for ordinary text. Lone surrogate events use WTF-8. Bracketed pastes remain one framed payload. The zero value uses upstream's default timeouts.
type StdinBuffer struct {
	buffer         string
	pasteMode      bool
	pasteBuffer    string
	pendingKittyCP int
	timeout        time.Duration
	escapeTimeout  time.Duration
	// utf8Pending holds the leading bytes of a character split across reads.
	utf8Pending []byte
	// events are the "data" and "paste" listeners (stdin-buffer.ts extends EventEmitter).
	events stdinListeners
}

// StdinBufferOptions mirrors upstream StdinBufferOptions. A zero field keeps
// the upstream default.
type StdinBufferOptions struct {
	// Timeout is how long an incomplete escape sequence waits for more input.
	Timeout time.Duration
	// EscapeTimeout is how long a lone ESC waits before it is the Escape key.
	EscapeTimeout time.Duration
}

// NewStdinBuffer mirrors the upstream StdinBuffer constructor.
func NewStdinBuffer(options StdinBufferOptions) *StdinBuffer {
	return &StdinBuffer{timeout: options.Timeout, escapeTimeout: options.EscapeTimeout}
}

// FlushTimeout is how long the buffered remainder waits for more input before
// Flush emits it: the escape timeout for a lone ESC, the sequence timeout for
// anything else. Mirrors the timeout StdinBuffer.process schedules upstream.
func (b *StdinBuffer) FlushTimeout() time.Duration {
	if b.buffer == escByte {
		return cmp.Or(b.escapeTimeout, defaultEscapeTimeout)
	}
	return cmp.Or(b.timeout, defaultSequenceTimeout)
}

// ProcessTerminalBytes feeds one terminal read. It decodes UTF-8 across reads
// the way upstream ProcessTerminal's stdin.setEncoding("utf8") does, so a
// character split between reads is held until it is complete and a lone high
// byte is never rewritten as a Meta key; the decoded text then goes through
// ProcessString.
func (b *StdinBuffer) ProcessTerminalBytes(data []byte) []string {
	if len(b.utf8Pending) > 0 {
		data = append(b.utf8Pending, data...)
		b.utf8Pending = nil
	}
	if cut := incompleteUTF8Suffix(data); cut > 0 {
		b.utf8Pending = bytes.Clone(data[len(data)-cut:])
		data = data[:len(data)-cut]
		if len(data) == 0 {
			return nil
		}
	}
	return b.ProcessString(jsstring.FromUTF8(data))
}

// incompleteUTF8Suffix returns the length of a trailing UTF-8 sequence that is
// still missing continuation bytes, or 0.
func incompleteUTF8Suffix(data []byte) int {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
		c := data[len(data)-back]
		if c < utf8.RuneSelf {
			return 0
		}
		if utf8.RuneStart(c) {
			if utf8.FullRune(data[len(data)-back:]) {
				return 0
			}
			return back
		}
	}
	return 0
}

// ProcessBytes mirrors upstream process(Buffer): it feeds raw bytes and
// returns any complete keystroke chunks ready for dispatch. Terminal loops use
// ProcessTerminalBytes, which matches upstream's utf8-decoded stdin instead.
func (b *StdinBuffer) ProcessBytes(data []byte) []string {
	if len(data) == 0 {
		if b.buffer == "" {
			return []string{""}
		}
		return nil
	}
	// Mirrors upstream's high-byte conversion for meta keys: a single byte
	// >127 is rewritten as ESC + (byte-128).
	var s string
	if len(data) == 1 && data[0] > 127 {
		s = escByte + string(rune(data[0]-128))
	} else {
		s = jsstring.FromUTF8(data)
	}
	return b.ProcessString(s)
}

// ProcessString mirrors upstream process(string).
func (b *StdinBuffer) ProcessString(s string) []string {
	if s == "" {
		if b.buffer == "" {
			return b.emitDataSequence(nil, "")
		}
		return nil
	}
	b.buffer = jsstring.Canonical(b.buffer + s)

	if b.pasteMode {
		b.pasteBuffer = jsstring.Canonical(b.pasteBuffer + b.buffer)
		b.buffer = ""
		if end := strings.Index(b.pasteBuffer, bracketedPasteEnd); end >= 0 {
			payload := b.pasteBuffer[:end]
			remaining := b.pasteBuffer[end+len(bracketedPasteEnd):]
			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKittyCP = 0
			out := []string{bracketedPasteStart + payload + bracketedPasteEnd}
			b.Emit(StdinBufferEventPaste, payload)
			if remaining != "" {
				out = append(out, b.ProcessString(remaining)...)
			}
			return out
		}
		return nil
	}

	if start := strings.Index(b.buffer, bracketedPasteStart); start >= 0 {
		var out []string
		if start > 0 {
			// An incomplete sequence directly before the paste marker is dropped, as upstream process() ignores that remainder.
			result, _ := extractCompleteSequences(b.buffer[:start])
			out = b.emitDataSequence(out, result...)
		}
		b.pendingKittyCP = 0
		b.buffer = b.buffer[start+len(bracketedPasteStart):]
		b.pasteMode = true
		b.pasteBuffer = b.buffer
		b.buffer = ""
		if end := strings.Index(b.pasteBuffer, bracketedPasteEnd); end >= 0 {
			payload := b.pasteBuffer[:end]
			remaining := b.pasteBuffer[end+len(bracketedPasteEnd):]
			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKittyCP = 0
			out = append(out, bracketedPasteStart+payload+bracketedPasteEnd)
			b.Emit(StdinBufferEventPaste, payload)
			if remaining != "" {
				out = append(out, b.ProcessString(remaining)...)
			}
		}
		return out
	}

	result, remainder := extractCompleteSequences(b.buffer)
	b.buffer = remainder
	return b.emitDataSequence(nil, result...)
}

// Flush returns an incomplete remainder as one chunk. An empty flush preserves pending Kitty printable deduplication; the input loop owns timeout cancellation.
func (b *StdinBuffer) Flush() []string {
	if b.buffer == "" {
		return nil
	}
	out := b.emitDataSequence(nil, b.buffer)
	b.buffer = ""
	b.pendingKittyCP = 0
	return out
}

// GetBuffer returns the pending incomplete input, excluding paste content.
func (b *StdinBuffer) GetBuffer() string { return b.buffer }

func (b *StdinBuffer) HasPendingFlush() bool {
	return b.buffer != ""
}

// Destroy releases the buffer the way terminal.ts stop() does: it clears pending input (stdin-buffer.ts:441-443 destroy calls clear).
func (b *StdinBuffer) Destroy() { b.Clear() }

func (b *StdinBuffer) Clear() {
	b.buffer = ""
	b.pasteMode = false
	b.pasteBuffer = ""
	b.pendingKittyCP = 0
	b.utf8Pending = nil
}

func parseUnmodifiedKittyPrintableCodepoint(sequence string) (int, bool) {
	if !strings.HasPrefix(sequence, escByte+"[") || !strings.HasSuffix(sequence, "u") {
		return 0, false
	}
	payload := sequence[2 : len(sequence)-1]
	if payload == "" {
		return 0, false
	}
	parts := strings.Split(payload, ":")
	head := parts[0]
	if head == "" {
		return 0, false
	}
	semi := strings.Split(head, ";")
	if len(semi) != 1 {
		return 0, false
	}
	cp, err := strconv.Atoi(semi[0])
	if err != nil || cp < 32 {
		return 0, false
	}
	if len(parts) > 1 && parts[1] != "" {
		if _, err := strconv.Atoi(parts[1]); err != nil {
			return 0, false
		}
	}
	if len(parts) > 2 {
		mods := strings.Split(parts[2], ";")
		if len(mods) > 2 {
			return 0, false
		}
		for _, p := range mods {
			if p == "" {
				continue
			}
			if _, err := strconv.Atoi(p); err != nil {
				return 0, false
			}
		}
	}
	if len(parts) > 3 {
		return 0, false
	}
	return cp, true
}

func (b *StdinBuffer) emitDataSequence(out []string, sequences ...string) []string {
	for _, sequence := range sequences {
		if rawCodepoint := singleRuneCodepoint(sequence); rawCodepoint != 0 && rawCodepoint == b.pendingKittyCP {
			b.pendingKittyCP = 0
			continue
		}
		if cp, ok := parseUnmodifiedKittyPrintableCodepoint(sequence); ok {
			b.pendingKittyCP = cp
		} else {
			b.pendingKittyCP = 0
		}
		out = append(out, sequence)
		b.Emit(StdinBufferEventData, sequence)
	}
	return out
}

func singleRuneCodepoint(s string) int {
	if jsstring.Length(s) != 1 {
		return 0
	}
	r, _ := jsstring.DecodeRuneInString(s)
	return int(r)
}

func isCompleteSequence(data []uint16) string {
	if len(data) == 0 || data[0] != 0x1b {
		return "not-escape"
	}
	if len(data) == 1 {
		return "incomplete"
	}
	switch data[1] {
	case '[':
		if len(data) >= 3 && data[2] == 'M' {
			if len(data) >= 6 {
				return "complete"
			}
			return "incomplete"
		}
		return isCompleteCSISequence(data)
	case ']', 'P', '_':
		if data[1] == ']' && data[len(data)-1] == 7 {
			return "complete"
		}
		if len(data) >= 2 && data[len(data)-2] == 0x1b && data[len(data)-1] == '\\' {
			return "complete"
		}
		return "incomplete"
	case 'O':
		if len(data) >= 3 {
			return "complete"
		}
		return "incomplete"
	default:
		return "complete"
	}
}

func isCompleteCSISequence(data []uint16) string {
	if len(data) < 3 {
		return "incomplete"
	}
	payload := data[2:]
	last := payload[len(payload)-1]
	if last < 0x40 || last > 0x7e {
		return "incomplete"
	}
	if payload[0] != '<' {
		return "complete"
	}
	if last != 'M' && last != 'm' {
		return "incomplete"
	}
	fields, digits := 1, 0
	for _, unit := range payload[1 : len(payload)-1] {
		if unit >= '0' && unit <= '9' {
			digits++
			continue
		}
		if unit != ';' || digits == 0 {
			return "incomplete"
		}
		fields++
		digits = 0
	}
	if fields == 3 && digits > 0 {
		return "complete"
	}
	return "incomplete"
}

func extractCompleteSequences(buffer string) ([]string, string) {
	units := jsstring.ToUTF16(buffer)
	var sequences []string
	for pos := 0; pos < len(units); {
		remaining := units[pos:]
		if remaining[0] != 0x1b {
			sequences = append(sequences, jsstring.FromUTF16(remaining[:1]))
			pos++
			continue
		}
		complete := false
		for end := 1; end <= len(remaining); end++ {
			candidate := remaining[:end]
			if isCompleteSequence(candidate) == "incomplete" {
				continue
			}
			if end == 2 && candidate[1] == 0x1b && end < len(remaining) && strings.ContainsRune("[]OP_", rune(remaining[end])) {
				sequences = append(sequences, escByte)
				pos++
			} else {
				sequences = append(sequences, jsstring.FromUTF16(candidate))
				pos += end
			}
			complete = true
			break
		}
		if !complete {
			return sequences, jsstring.FromUTF16(remaining)
		}
	}
	return sequences, ""
}
