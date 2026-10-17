// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) 2025 opentui
// SPDX-License-Identifier: MIT

package tui

// Ports packages/tui/src/stdin-buffer.ts.

import (
	"bytes"
	"cmp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

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

// terminalReplyHeaderRegex is upstream TERMINAL_REPLY_HEADER_PATTERN: color and program-status OSC replies, XTVersion, and Kitty
// graphics replies. Until their header is complete, the input may be batched legacy Alt keystrokes.
var terminalReplyHeaderRegex = lazyregexp.New(`^\x1b(?:\](?:1[01];|4;\d{1,3};|7501;\?)|P>\||_G(?:[a-z]=\d+,)*[a-z]=\d+;)`)

// isControlString is upstream isControlString: an OSC, DCS, or APC introducer.
func isControlString(data string) bool {
	//portlint:allow utf16units the two bytes compared are ESC and an ASCII introducer, each one UTF-16 code unit
	return len(data) >= 2 && data[0] == 0x1b && (data[1] == ']' || data[1] == 'P' || data[1] == '_')
}

// StdinBuffer accumulates partial escape sequences across reads and emits JavaScript UTF-16 units for ordinary text. Lone surrogate events use WTF-8. Bracketed pastes remain one framed payload. The zero value uses upstream's default timeouts.
//
// pig divergence (D114): its listener surface (stdin_buffer_events.go) has no per-event listener limit, so it has no GetMaxListeners, SetMaxListeners or MaxListenersExceededWarning.
type StdinBuffer struct {
	buffer    string
	pasteMode bool
	// pasteChunks is upstream's pasteBuffer, canonical, kept as the reads that delivered it so a paste split over many reads
	// is joined once rather than copied and searched again per read.
	pasteChunks    []string
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

// Process is upstream process(data): it feeds the string to the buffer and publishes each complete sequence to the listeners, returning nothing. ProcessString does the same and also returns the sequences it published.
//
// upstream: packages/tui/src/stdin-buffer.ts:296 process
func (b *StdinBuffer) Process(data string) { b.ProcessString(data) }

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
		payload, remaining, ended := b.appendPaste(b.buffer)
		b.buffer = ""
		if ended {
			b.pasteMode = false
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
		payload, remaining, ended := b.appendPaste(b.buffer)
		b.buffer = ""
		if ended {
			b.pasteMode = false
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

// FlushExpired is the flush timeout firing (stdin-buffer.ts process timeout callback). A flushed control string longer than its
// introducer is the Alt key followed by batched input: the introducer is emitted and the tail is parsed again, so text, Enter,
// and control keys are not swallowed as one event. The tail can leave a new remainder; HasPendingFlush arms its timeout.
func (b *StdinBuffer) FlushExpired() []string {
	sequence := b.buffer
	if !isControlString(sequence) || jsstring.Length(sequence) <= 2 {
		return b.Flush()
	}
	b.buffer = ""
	//portlint:allow utf16units isControlString held: the first two bytes are ESC and an ASCII introducer, sequence.slice(0, 2)
	out := b.emitDataSequence(nil, sequence[:2])
	b.pendingKittyCP = 0
	//portlint:allow utf16units the two ASCII introducer bytes are two code units, so this is sequence.slice(2)
	return append(out, b.ProcessString(sequence[2:])...)
}

// GetBuffer returns the pending incomplete input, excluding paste content.
func (b *StdinBuffer) GetBuffer() string { return b.buffer }

// HasPendingFlush reports whether the remainder waits on the flush timeout. A remainder with a recognized reply header waits
// for its terminator instead: flushing it would turn a delayed tail into text and an OSC's BEL terminator into Ctrl+G. Other
// input, including Alt+] followed by text, keeps bounded recovery (stdin-buffer.ts process).
func (b *StdinBuffer) HasPendingFlush() bool {
	return b.buffer != "" && !terminalReplyHeaderRegex.MatchString(b.buffer)
}

// Destroy releases the buffer the way terminal.ts stop() does: it clears pending input (stdin-buffer.ts:441-443 destroy calls clear).
func (b *StdinBuffer) Destroy() { b.Clear() }

func (b *StdinBuffer) Clear() {
	b.buffer = ""
	b.pasteMode = false
	b.pasteChunks = nil
	b.pendingKittyCP = 0
	b.utf8Pending = nil
}

// appendPaste is upstream's `pasteBuffer += buffer` followed by the search for the end marker. text is canonical; the joined
// paste is canonical as the concatenation is except where a high surrogate ending the paste pairs with a low surrogate
// starting text. Only the new text and the bytes before it that can start the marker are searched. Once the marker arrived it
// returns the paste before it and the text after it and starts a new paste.
func (b *StdinBuffer) appendPaste(text string) (payload, remaining string, ended bool) {
	if n := len(b.pasteChunks); n > 0 && text != "" {
		//portlint:allow utf16units byte test for the 3-byte encoding of a lone high surrogate (ED A0-AF ..) that canonical text keeps at a chunk end
		if last := b.pasteChunks[n-1]; len(last) >= 3 && last[len(last)-3] == 0xed && last[len(last)-2]&0xf0 == 0xa0 {
			b.pasteChunks[n-1] = last[:len(last)-3]
			text = jsstring.Canonical(last[len(last)-3:] + text)
		}
	}
	before := b.pasteTail(len(bracketedPasteEnd) - 1)
	b.pasteChunks = append(b.pasteChunks, text)
	at := strings.Index(before+text, bracketedPasteEnd)
	if at < 0 {
		return "", "", false
	}
	paste := strings.Join(b.pasteChunks, "")
	b.pasteChunks = nil
	end := len(paste) - len(text) - len(before) + at
	return paste[:end], paste[end+len(bracketedPasteEnd):], true
}

// pasteTail is the last n bytes of the paste, or all of it when it is shorter.
func (b *StdinBuffer) pasteTail(n int) string {
	tail := ""
	for i := len(b.pasteChunks) - 1; i >= 0 && len(tail) < n; i-- {
		tail = b.pasteChunks[i] + tail
	}
	//portlint:allow utf16units the last n bytes, where n < len of the ASCII end marker, searched only for that marker
	return tail[max(0, len(tail)-n):]
}

var unmodifiedKittyPrintableRegex = lazyregexp.New(`^\x1b\[(\d+)(?::\d*)?(?::\d+)?u$`)

func parseUnmodifiedKittyPrintableCodepoint(sequence string) (int, bool) {
	match := unmodifiedKittyPrintableRegex.FindStringSubmatch(sequence)
	if match == nil {
		return 0, false
	}
	codepoint := keyNumber(match[1])
	return keyCodepoint(codepoint), codepoint >= 32
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
		terminalReply := len(remaining) > 2 && (remaining[1] == ']' || remaining[1] == 'P' || remaining[1] == '_') && terminalReplyHeaderRegex.MatchString(jsstring.FromUTF16(remaining))
		complete := false
		for end := 1; end <= len(remaining); end++ {
			if terminalReply && end > 2 {
				switch c := remaining[end-1]; {
				case c == 0x18 || c == 0x1a:
					// CAN and SUB cancel the string, not subsequent keyboard input.
					pos += end
					complete = true
				case c == 0x1b && end < len(remaining) && remaining[end] != '\\':
					// A new escape sequence aborts an unterminated reply. A trailing ESC stays buffered until it can be told from ST.
					pos += end - 1
					complete = true
				}
				if complete {
					break
				}
			}
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
