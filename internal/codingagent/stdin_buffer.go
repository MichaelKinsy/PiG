// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) 2025 opentui
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"cmp"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui"
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

// StdinBuffer mirrors upstream packages/tui/src/stdin-buffer.ts.
// It accumulates partial escape sequences across reads so the input loop
// only sees complete chunks. Bracketed pastes are re-emitted as a single
// framed payload because dispatchKey/classifyKey currently consume the
// upstream ESC[200~...ESC[201~ form directly. The zero value uses upstream's
// default timeouts.
type StdinBuffer struct {
	buffer         string
	pasteMode      bool
	pasteBuffer    string
	pendingKittyCP int
	timeout        time.Duration
	escapeTimeout  time.Duration
	// utf8Pending holds the leading bytes of a character split across reads.
	utf8Pending []byte
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

// newProcessStdinBuffer builds the buffer terminal input loops use. Mirrors
// upstream ProcessTerminal.setupStdinBuffer, which passes
// resolveEscapeTimeoutMs() as the escape timeout.
func newProcessStdinBuffer() *StdinBuffer {
	ms := tui.ResolveEscapeTimeoutMs(os.Getenv)
	return NewStdinBuffer(StdinBufferOptions{EscapeTimeout: time.Duration(ms * float64(time.Millisecond))})
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
	return b.ProcessString(string(data))
}

// stdinFlushTimer is the flush timeout a terminal input loop arms while its
// StdinBuffer holds an incomplete sequence. The loop selects on C; after a
// receive it calls stop and then Flush.
type stdinFlushTimer struct {
	timer *time.Timer
	C     <-chan time.Time
}

// sync re-arms the timer for b's pending remainder, or stops it when nothing
// is pending. Mirrors upstream process(), which clears its timeout on every
// call and schedules a new one only while the buffer is non-empty.
func (f *stdinFlushTimer) sync(b *StdinBuffer) {
	f.stop()
	if b.HasPendingFlush() {
		f.timer = time.NewTimer(b.FlushTimeout())
		f.C = f.timer.C
	}
}

func (f *stdinFlushTimer) stop() {
	if f.timer != nil {
		f.timer.Stop()
	}
	f.timer = nil
	f.C = nil
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
		s = string(data)
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
	b.buffer += s

	if b.pasteMode {
		b.pasteBuffer += b.buffer
		b.buffer = ""
		if end := strings.Index(b.pasteBuffer, bracketedPasteEnd); end >= 0 {
			payload := b.pasteBuffer[:end]
			remaining := b.pasteBuffer[end+len(bracketedPasteEnd):]
			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKittyCP = 0
			out := []string{bracketedPasteStart + payload + bracketedPasteEnd}
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
			result, remainder := extractCompleteSequences(b.buffer[:start])
			out = b.emitDataSequence(out, result...)
			if remainder != "" {
				out = b.emitDataSequence(out, remainder)
			}
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

// Flush emits any incomplete remainder as a single chunk. Mirrors upstream's
// timeout-based fallback path; the input loop owns the timer.
func (b *StdinBuffer) Flush() []string {
	if b.buffer == "" {
		b.pendingKittyCP = 0
		return nil
	}
	out := b.emitDataSequence(nil, b.buffer)
	b.buffer = ""
	b.pendingKittyCP = 0
	return out
}

func (b *StdinBuffer) HasPendingFlush() bool {
	return b.buffer != ""
}

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
	}
	return out
}

func singleRuneCodepoint(s string) int {
	if s == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		return 0
	}
	if size != len(s) {
		return 0
	}
	return int(r)
}

func isCompleteSequence(data string) string {
	if !strings.HasPrefix(data, escByte) {
		return "not-escape"
	}
	if len(data) == 1 {
		return "incomplete"
	}
	afterEsc := data[1:]
	switch {
	case strings.HasPrefix(afterEsc, "[M"):
		// X10 mouse reports are ESC[M followed by three encoded bytes. The M
		// introduces the payload; unlike an ordinary CSI final byte it does
		// not complete the sequence by itself.
		if len(data) >= 6 {
			return "complete"
		}
		return "incomplete"
	case strings.HasPrefix(afterEsc, "["):
		return isCompleteCSISequence(data)
	case strings.HasPrefix(afterEsc, "]"):
		return isCompleteOSCSequence(data)
	case strings.HasPrefix(afterEsc, "P"):
		return isCompleteDCSSequence(data)
	case strings.HasPrefix(afterEsc, "_"):
		return isCompleteAPCSequence(data)
	case strings.HasPrefix(afterEsc, "O"):
		if len(afterEsc) >= 2 {
			return "complete"
		}
		return "incomplete"
	case len(afterEsc) == 1:
		return "complete"
	default:
		return "complete"
	}
}

func isCompleteCSISequence(data string) string {
	if !strings.HasPrefix(data, escByte+"[") {
		return "complete"
	}
	if len(data) < 3 {
		return "incomplete"
	}
	payload := data[2:]
	lastChar := payload[len(payload)-1]
	if lastChar >= 0x40 && lastChar <= 0x7e {
		if strings.HasPrefix(payload, "<") {
			if sgrMousePayload(payload) {
				return "complete"
			}
			if lastChar == 'M' || lastChar == 'm' {
				parts := strings.Split(payload[1:len(payload)-1], ";")
				if len(parts) == 3 {
					allDigits := true
					for _, p := range parts {
						if p == "" || strings.Trim(p, "0123456789") != "" {
							allDigits = false
							break
						}
					}
					if allDigits {
						return "complete"
					}
				}
			}
			return "incomplete"
		}
		return "complete"
	}
	return "incomplete"
}

func sgrMousePayload(payload string) bool {
	if len(payload) < 5 || payload[0] != '<' {
		return false
	}
	if payload[len(payload)-1] != 'M' && payload[len(payload)-1] != 'm' {
		return false
	}
	parts := strings.Split(payload[1:len(payload)-1], ";")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return true
}

func isCompleteOSCSequence(data string) string {
	if strings.HasPrefix(data, escByte+"]") && !strings.HasSuffix(data, escByte+"\\") && !strings.HasSuffix(data, "\x07") {
		return "incomplete"
	}
	return "complete"
}

func isCompleteDCSSequence(data string) string {
	if strings.HasPrefix(data, escByte+"P") && !strings.HasSuffix(data, escByte+"\\") {
		return "incomplete"
	}
	return "complete"
}

func isCompleteAPCSequence(data string) string {
	if strings.HasPrefix(data, escByte+"_") && !strings.HasSuffix(data, escByte+"\\") {
		return "incomplete"
	}
	return "complete"
}

func extractCompleteSequences(buffer string) ([]string, string) {
	var sequences []string
	for pos := 0; pos < len(buffer); {
		remaining := buffer[pos:]
		if strings.HasPrefix(remaining, escByte) {
			foundComplete := false
			seqEnd := 1
			for seqEnd <= len(remaining) {
				candidate := remaining[:seqEnd]
				status := isCompleteSequence(candidate)
				switch status {
				case "complete":
					// When candidate is ESC+ESC and a sequence-introducer
					// follows, emit only the first ESC and restart from the
					// second (WezTerm Kitty-keyboard concatenates the Escape
					// press '\x1b' with a following CSI-u release). Upstream
					// reads remaining[seqEnd] as undefined when seqEnd is past
					// the end (ESC+ESC at the buffer tail) and falls through;
					// Go must bounds-check or it panics (stdin-buffer.ts:217).
					if candidate == escByte+escByte && seqEnd < len(remaining) {
						nextChar := remaining[seqEnd]
						if nextChar == '[' || nextChar == ']' || nextChar == 'O' || nextChar == 'P' || nextChar == '_' {
							sequences = append(sequences, escByte)
							pos++
							foundComplete = true
							seqEnd = len(remaining) + 1
							continue
						}
					}
					sequences = append(sequences, candidate)
					pos += seqEnd
					foundComplete = true
					seqEnd = len(remaining) + 1
				case "incomplete":
					seqEnd++
				default:
					sequences = append(sequences, candidate)
					pos += seqEnd
					foundComplete = true
					seqEnd = len(remaining) + 1
				}
			}
			if foundComplete {
				continue
			}
			return sequences, remaining
		}
		// Upstream emits one character at a time; a byte split would break a
		// multi-byte character apart and defeat Kitty printable dedup.
		_, size := utf8.DecodeRuneInString(remaining)
		sequences = append(sequences, remaining[:size])
		pos += size
	}
	return sequences, ""
}

// dropKeyReleases removes Kitty key-release events (CSI-u with event type :3)
// from chunks bound for the given focused component, honouring a
// tui.KeyReleaseReceiver opt-in. Delegates the per-chunk decision to
// tui.ShouldDeliverKey so modal dispatch shares the one rule that governs the
// editor and extension dialogs.
func dropKeyReleases(component tui.Component, chunks []string) []string {
	return slices.DeleteFunc(chunks, func(chunk string) bool {
		return !tui.ShouldDeliverKey(component, chunk)
	})
}

// dispatchModalInput feeds complete input sequences to a focused modal
// component, dropping key releases and stopping as soon as the component
// reports done. The process input pump has already decoded terminal reads with
// StdinBuffer before focus routing.
func dispatchModalInput(component tui.Component, chunks []string, handleInput func(string), done func() bool) {
	for _, chunk := range dropKeyReleases(component, chunks) {
		if chunk == "" {
			continue
		}
		handleInput(chunk)
		if done() {
			return
		}
	}
}

// drainModalInput applies any input messages already queued on ch without
// blocking, dispatching each split chunk through handle. It exists so a burst
// of buffered keystrokes (key-repeat, paste) collapses into a single render:
// the caller dispatches the first message, drains the rest here, then renders
// once. End state is identical to rendering per-message; only intermediate
// frames are skipped. handle returns true when the component is done, which
// stops the drain immediately.
func drainModalInput(component tui.Component, ch <-chan []byte, handle func(chunk string) bool) {
	for {
		select {
		case more := <-ch:
			if slices.ContainsFunc(dropKeyReleases(component, []string{string(more)}), handle) {
				return
			}
		default:
			return
		}
	}
}
