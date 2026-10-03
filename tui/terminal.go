package tui

// Ports packages/tui/src/terminal.ts.

// This package keeps terminal control separate from application input routing.

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/term"
)

const (
	defaultEscapeTimeoutMs    = 10
	defaultSSHEscapeTimeoutMs = 100
)

// ResolveEscapeTimeoutMs returns how long, in milliseconds, input handling
// waits for the rest of an escape sequence before dispatching a lone ESC as the
// Escape key. PI_TUI_ESC_TIMEOUT wins when it is a finite positive number; SSH
// sessions default to 100ms because legacy Alt+key input is ESC plus another
// byte and high-latency transports split them. Mirrors upstream
// resolveEscapeTimeoutMs (terminal.ts).
func ResolveEscapeTimeoutMs(getenv func(string) string) float64 {
	if configured, ok := jsNumber(getenv("PI_TUI_ESC_TIMEOUT")); ok && !math.IsInf(configured, 0) && configured > 0 {
		return configured
	}
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return defaultSSHEscapeTimeoutMs
	}
	return defaultEscapeTimeoutMs
}

// jsNumber mirrors JavaScript Number(text) for environment values; ok is false
// when the result is NaN.
func jsNumber(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, true
	}
	for prefix, base := range map[string]int{"0x": 16, "0X": 16, "0o": 8, "0O": 8, "0b": 2, "0B": 2} {
		if digits, ok := strings.CutPrefix(trimmed, prefix); ok {
			value, err := strconv.ParseUint(digits, base, 64)
			return float64(value), err == nil
		}
	}
	switch trimmed {
	case "Infinity", "+Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	}
	if strings.ContainsAny(trimmed, "_xXpPiInN") {
		return 0, false
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	return value, err == nil
}

// Terminal mirrors the control surface of upstream `terminal.ts` in Go form.
//
// Faithful port note: upstream's `start(onInput, onResize)` / `stop()` pair
// is provided here as `Start` / `Stop`. They are thin wrappers over
// `EnterRawMode` + a goroutine input loop + SIGWINCH notify, so legacy
// caller-owns-loop code paths (interactive mode, session selectors, the
// extension UI) keep working through `EnterRawMode` directly. New callers
// can use `Start`/`Stop` for a behavior-equivalent surface to upstream.
type Terminal interface {
	// Start owns input framing, negotiation filtering and native normalization before invoking onInput for each event. The resize callback follows terminal dimension changes.
	Start(onInput func([]byte), onResize func()) error

	// Stop reverses Start: cancels and joins the input goroutine, removes the
	// resize handler, and restores cooked mode without draining unread input.
	// Call DrainInput explicitly before Stop only at process shutdown.
	Stop()

	DrainInput(maxWait, idleWait time.Duration) error
	Write(data string)
	Columns() int
	Rows() int
	KittyProtocolActive() bool
	MoveBy(lines int)
	HideCursor()
	ShowCursor()
	ClearLine()
	ClearFromCursor()
	ClearScreen()
	SetTitle(title string)
	SetProgress(active bool)
}

// ProcessTerminal is the concrete terminal control implementation backed by
// process stdin/stdout (or test doubles).
type ProcessTerminal struct {
	stdin  *os.File
	stdout *os.File
	out    io.Writer
	outMu  sync.Mutex

	// startMu guards reader, resize and protocol-query ownership. Start is idempotent until Stop releases that ownership.
	startMu         sync.Mutex
	stopRestore     func()             // restore closure from EnterRawMode; non-nil while running
	stopReader      context.CancelFunc // cancels the input goroutine
	readerDone      chan struct{}      // closes after the input goroutine can no longer consume stdin
	resizeStop      func()             // stops the platform resize watcher; non-nil while running
	protocolQueried bool
	// pendingKeyboardProtocolDeviceAttributes counts the DA1 replies owed to keyboard protocol queries. Later DA1 replies answer other queries, such as QueryTerminalColors, and are forwarded.
	pendingKeyboardProtocolDeviceAttributes atomic.Int32

	progressMu        sync.Mutex
	progressTicker    *time.Ticker
	progressStop      chan struct{}
	progressDone      chan struct{}
	progressKeepalive time.Duration
}

const (
	terminalProgressKeepalive = time.Second
	terminalProgressActiveSeq = "\x1b]9;4;3\x07"
	terminalProgressClearSeq  = "\x1b]9;4;0\x07"
)

// NewProcessTerminal constructs a terminal helper around the provided stdin and
// stdout files.
func NewProcessTerminal(stdin, stdout *os.File) *ProcessTerminal {
	return newProcessTerminal(stdin, stdout, stdout)
}

// NewProcessTerminalWithOutput constructs a terminal helper whose control bytes
// are written to out. Used by tests that want stdout-like behavior without
// touching the real terminal.
func NewProcessTerminalWithOutput(stdin, stdout *os.File, out io.Writer) *ProcessTerminal {
	if out == nil {
		out = stdout
	}
	return newProcessTerminal(stdin, stdout, out)
}

func newProcessTerminal(stdin, stdout *os.File, out io.Writer) *ProcessTerminal {
	return &ProcessTerminal{
		stdin:             stdin,
		stdout:            stdout,
		out:               out,
		progressKeepalive: terminalProgressKeepalive,
	}
}

var processTerminal = NewProcessTerminal(os.Stdin, os.Stdout)

var kittyProtocolActive atomic.Bool
var modifyOtherKeysActive atomic.Bool

// keyboardProtocolPushed records that extendedKeyInit sent the Kitty flags
// request. It is set even when the terminal turns out not to support the
// protocol, because the push must still be popped. Mirrors upstream's
// Terminal.keyboardProtocolPushed (terminal.ts:113).
var keyboardProtocolPushed atomic.Bool

const (
	// keyboardProtocolPop pops one entry off the terminal's Kitty flags stack.
	keyboardProtocolPop    = "\x1b[<u"
	modifyOtherKeysEnable  = "\x1b[>4;2m"
	modifyOtherKeysDisable = "\x1b[>4;0m"
)

var (
	kittyProtocolResponse    = lazyregexp.New(`^\x1b\[\?(\d+)u`)
	deviceAttributesResponse = lazyregexp.New(`^\x1b\[\?[\d;]*c`)
)

// SetKittyProtocolActive mirrors upstream keys.ts global Kitty state.
func SetKittyProtocolActive(active bool) {
	kittyProtocolActive.Store(active)
}

// IsKittyProtocolActive reports whether a Kitty protocol response was seen
// during the current raw-mode session.
func IsKittyProtocolActive() bool {
	return kittyProtocolActive.Load()
}

// kittyKeyboardProtocolPush pushes Pi's desired Kitty flags (disambiguation,
// event types, alternate keys) onto the active screen's stack.
const kittyKeyboardProtocolPush = "\x1b[>7u"

// kittyKeyboardProtocolQuery pushes the flags, queries the resulting flags,
// then sends Device Attributes as a sentinel. A terminal without Kitty still
// answers DA, which enables modifyOtherKeys without a startup timer.
const kittyKeyboardProtocolQuery = kittyKeyboardProtocolPush + "\x1b[?u\x1b[c"

// extendedKeyInit enables bracketed paste, then runs kittyKeyboardProtocolQuery.
const extendedKeyInit = "\x1b[?2004h" + kittyKeyboardProtocolQuery

func (t *ProcessTerminal) queryAndEnableKittyProtocol() {
	SetKittyProtocolActive(false)
	modifyOtherKeysActive.Store(false)
	t.protocolQueried = true
	keyboardProtocolPushed.Store(true)
	t.pendingKeyboardProtocolDeviceAttributes.Add(1)
	t.Write(kittyKeyboardProtocolQuery)
}

// EnterRawMode puts stdin into raw mode and returns a restore function.
//
// This preserves pig's main-buffer rendering model from `tui.go`: no alternate
// screen, no hidden terminal reader, and callers continue to own the input loop.
func EnterRawMode() (restore func(), err error) {
	return processTerminal.EnterRawMode()
}

// EnterRawModeWithDrain separates process-shutdown input draining from cooked-mode restoration. A renderer drains first, stops while output is still raw, then restores cooked mode. Temporary terminal handoffs call only restore.
func EnterRawModeWithDrain() (restore func(), drain func(), err error) {
	restore, err = EnterRawModeForHandoff()
	if err != nil {
		return nil, nil, err
	}
	return restore, func() { _ = processTerminal.DrainInput(time.Second, 50*time.Millisecond) }, nil
}

// EnterRawModeForHandoff enters raw mode with a restore closure that leaves unread input for the next terminal owner. The caller must join its reader before restoring and drain late releases separately on final shutdown.
func EnterRawModeForHandoff() (restore func(), err error) {
	return processTerminal.enterRawMode(false)
}

// EnterRawMode puts this terminal into raw mode and returns a restore closure
// that drains late extended-key releases before restoring cooked mode.
func (t *ProcessTerminal) EnterRawMode() (restore func(), err error) {
	return t.enterRawMode(true)
}

func (t *ProcessTerminal) enterRawMode(drainOnRestore bool) (restore func(), err error) {
	if t.stdin == nil || t.out == nil {
		return nil, fmt.Errorf("terminal raw mode requires stdin/stdout")
	}
	fd := int(t.stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	rememberSignalRestore(fd, state)
	// On Windows, enable VT output processing so pig's ANSI renderer displays;
	// term.MakeRaw only configures raw input. No-op on unix.
	vtRestore := t.enableVTProcessing()
	t.Write("\x1b[?2004h")
	t.queryAndEnableKittyProtocol()
	return func() {
		// Stop the terminal generating extended-key sequences before anything
		// else, so the drain below has a finite amount of input to consume.
		t.disableKeyboardProtocol()
		// Disable bracketed paste before restoring cooked mode. A caller that
		// is exiting the interactive process drains late key releases; a
		// ProcessTerminal Stop preserves unread input for the next consumer,
		// matching upstream stop().
		t.Write("\x1b[?2004l")
		if drainOnRestore {
			_ = t.DrainInput(time.Second, 50*time.Millisecond)
		}
		_ = term.Restore(fd, state)
		vtRestore()
		t.protocolQueried = false
	}, nil
}

// ReadInputChunk reads one unframed terminal chunk. The input-loop owner passes it to TerminalInput so negotiation is handled after sequence framing.
func ReadInputChunk(r io.Reader) ([]byte, error) {
	if file, ok := r.(*os.File); ok && file != nil {
		r = terminalInput(file)
	}
	buf := make([]byte, 256)
	n, err := r.Read(buf)
	return buf[:n], err
}

// isKeyboardProtocolNegotiationReply reports whether sequence is exactly one Kitty flags or DA1 reply, whether or not a query owns it.
func isKeyboardProtocolNegotiationReply(sequence string) bool {
	for _, pattern := range []*lazyregexp.Regexp{kittyProtocolResponse, deviceAttributesResponse} {
		if match := pattern.FindString(sequence); match != "" && match == sequence {
			return true
		}
	}
	return false
}

// handleKeyboardProtocolNegotiationSequence consumes a Kitty flags reply, or a DA1 reply owed to a keyboard protocol query, and reports whether it did. A DA1 reply that answers another query is left for the caller to forward.
func (t *ProcessTerminal) handleKeyboardProtocolNegotiationSequence(sequence string) bool {
	if match := kittyProtocolResponse.FindStringSubmatch(sequence); match != nil && match[0] == sequence {
		flags, err := strconv.ParseFloat(match[1], 64)
		if err != nil && !math.IsInf(flags, 0) {
			return false
		}
		if flags == 0 {
			t.enableModifyOtherKeys()
			return true
		}
		t.disableModifyOtherKeys()
		SetKittyProtocolActive(true)
		return true
	}
	if deviceAttributesResponse.MatchString(sequence) && deviceAttributesResponse.FindString(sequence) == sequence {
		if t.pendingKeyboardProtocolDeviceAttributes.Load() == 0 {
			return false
		}
		t.pendingKeyboardProtocolDeviceAttributes.Add(-1)
		if !IsKittyProtocolActive() {
			t.enableModifyOtherKeys()
		}
		return true
	}
	return false
}

func (t *ProcessTerminal) enableModifyOtherKeys() {
	if IsKittyProtocolActive() || modifyOtherKeysActive.Swap(true) {
		return
	}
	t.Write(modifyOtherKeysEnable)
}

func (t *ProcessTerminal) disableModifyOtherKeys() {
	if !modifyOtherKeysActive.Swap(false) {
		return
	}
	t.Write(modifyOtherKeysDisable)
}

// disableKeyboardProtocol returns the keyboard to the mode the terminal had
// before pig requested extended keys, and does it exactly once however many
// callers ask.
//
// Ownership sits here rather than at the call sites because the order matters
// and getting it wrong is silent: while the Kitty flags are still pushed the
// terminal keeps emitting escape sequences for every key event, so draining
// stdin without disabling first chases input the terminal is still producing,
// and whatever arrives after the drain window lands in the user's shell.
// Upstream makes the same guarantee by disabling inside drainInput() and again
// in restore(), with its own state flags making the second call a no-op
// (terminal.ts:377-386, 423-433).
//
// Each protocol has its own write. The output lock keeps teardown writes adjacent to other control writes on this terminal.
func (t *ProcessTerminal) disableKeyboardProtocol() {
	popKitty := keyboardProtocolPushed.Swap(false)
	disableModify := modifyOtherKeysActive.Swap(false)
	if !popKitty && !disableModify {
		return
	}
	kittyProtocolActive.Store(false)
	t.outMu.Lock()
	defer t.outMu.Unlock()
	if popKitty {
		t.write(keyboardProtocolPop)
	}
	if disableModify {
		t.write(modifyOtherKeysDisable)
	}
}

// Start enters raw mode and owns input and resize delivery. Each onInput callback receives one framed, normalized event after keyboard negotiation filtering. Callbacks run synchronously in input order.
//
// Use [ProcessTerminal.StartWithReadError] to distinguish terminal closure from user input. Caller-owned loops use EnterRawMode, ReadInputStream and TerminalInput instead.
//
// Calling Start twice without an intervening Stop is a no-op.
func (t *ProcessTerminal) Start(onInput func([]byte), onResize func()) error {
	return t.StartWithReadError(onInput, onResize, nil)
}

// StartWithReadError starts terminal input and reports the error that ends the
// input loop. Cancellation through [ProcessTerminal.Stop] does not report an
// error.
func (t *ProcessTerminal) StartWithReadError(onInput func([]byte), onResize func(), onReadError func(error)) error {
	t.startMu.Lock()
	defer t.startMu.Unlock()
	if t.stopRestore != nil {
		return nil // already started
	}
	restore, err := t.enterRawMode(false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.stopRestore = restore
	t.stopReader = cancel

	// Input goroutine: reads stdin, forwards bytes to onInput.
	// Mirrors upstream's setupStdinBuffer + 'data' event forwarding.
	if onInput != nil && t.stdin != nil {
		done := make(chan struct{})
		t.readerDone = done
		go func() {
			defer close(done)
			t.forwardInput(ctx, onInput, onReadError)
		}()
	}

	// Resize watcher: platform-specific. Unix uses SIGWINCH; Windows polls
	// the console size (no SIGWINCH). Both invoke onResize on change and once
	// on startup, matching upstream's observable "resize -> re-render".
	if onResize != nil {
		t.resizeStop = t.startResizeWatcher(ctx, onResize)
	}

	return nil
}

var normalizeTerminalInput = NormalizeProcessInputSequence

func (t *ProcessTerminal) forwardInput(ctx context.Context, onInput func([]byte), onReadError func(error)) {
	t.forwardInputFrom(ctx, t.stdin, onInput, onReadError)
}

func (t *ProcessTerminal) forwardInputFrom(ctx context.Context, source io.Reader, onInput func([]byte), onReadError func(error)) {
	reader, err := newTerminalReader(ctx, source)
	if err != nil {
		if ctx.Err() == nil && onReadError != nil {
			onReadError(err)
		}
		return
	}
	defer reader.close()
	input := t.NewTerminalInput(func(sequence string) {
		onInput([]byte(normalizeTerminalInput(sequence)))
	})
	defer input.Close()
	for {
		if ctx.Err() != nil {
			return
		}
		ms := -1
		if deadline := input.nextDeadline(); !deadline.IsZero() {
			ms = max(0, int(math.Ceil(float64(time.Until(deadline))/float64(time.Millisecond))))
		}
		data, err := reader.read(ms)
		if len(data) > 0 {
			input.Process(data)
		} else if err == nil {
			input.Flush()
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			input.FlushPending()
			if ctx.Err() == nil && onReadError != nil {
				onReadError(err)
			}
			return
		}
		// Deliver bytes already consumed before checking cancellation; unread bytes belong to the next terminal owner.
		if ctx.Err() != nil {
			return
		}
	}
}

// Stop mirrors upstream `ProcessTerminal.stop()`. It cancels and joins the
// input goroutine, removes the resize handler, and restores cooked mode without
// draining unread input. This lets a subsequent terminal owner receive bytes
// typed during focus handoff. Safe to call multiple times; a standalone
// protocol query is unwound even when no reader was started.
func (t *ProcessTerminal) Stop() {
	t.startMu.Lock()
	defer t.startMu.Unlock()
	if t.clearProgressInterval() {
		t.Write(terminalProgressClearSeq)
	}
	if t.stopReader != nil {
		t.stopReader()
		t.stopReader = nil
	}
	if t.readerDone != nil {
		<-t.readerDone
		t.readerDone = nil
	}
	if t.resizeStop != nil {
		t.resizeStop()
		t.resizeStop = nil
	}
	if t.stopRestore != nil {
		t.stopRestore()
		t.stopRestore = nil
	} else if t.protocolQueried {
		t.Write("\x1b[?2004l")
		t.disableKeyboardProtocol()
		t.protocolQueried = false
	}
}

// DrainInput drains pending stdin bytes for up to maxWait, exiting early once
// no new bytes arrive within idleWait. This mirrors upstream's slow-SSH guard
// against key release sequences leaking into the parent shell on exit.
//
// Disables the extended-key protocols first, as upstream's drainInput does: a
// drain that runs while the terminal is still reporting key events has no
// stable end, because releasing the keys pressed during the drain produces more
// input. Idempotent, so a caller that already disabled loses nothing.
func (t *ProcessTerminal) DrainInput(maxWait, idleWait time.Duration) error {
	t.disableKeyboardProtocol()
	if t.stdin == nil {
		return nil
	}
	if maxWait <= 0 {
		maxWait = time.Second
	}
	if idleWait <= 0 {
		idleWait = 50 * time.Millisecond
	}

	fd := t.stdin.Fd()
	end := time.Now().Add(maxWait)
	lastData := time.Now()
	buf := make([]byte, 256)

	for {
		now := time.Now()
		if !now.Before(end) || now.Sub(lastData) >= idleWait {
			return nil
		}
		wait := minDuration(idleWait-now.Sub(lastData), end.Sub(now))
		if wait <= 0 {
			return nil
		}
		ms := int(wait.Milliseconds())
		if ms <= 0 {
			ms = 1
		}
		n, err := pollReadable(fd, ms)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if _, err := t.stdin.Read(buf); err != nil {
			if isEINTR(err) {
				continue
			}
			return nil
		}
		lastData = time.Now()
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Write emits data to the terminal output.
func (t *ProcessTerminal) Write(data string) {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	t.write(data)
}

func (t *ProcessTerminal) write(data string) {
	if t.out == nil {
		return
	}
	_, _ = io.WriteString(t.out, data)
}

// Columns returns the terminal width, falling back to COLUMNS or 80 when unavailable.
func (t *ProcessTerminal) Columns() int {
	if t.stdout != nil {
		if width, _, err := term.GetSize(int(t.stdout.Fd())); err == nil && width > 0 {
			return width
		}
	}
	if width, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && width > 0 {
		return width
	}
	return 80
}

// Rows returns the terminal height, falling back to LINES or 24 when unavailable.
func (t *ProcessTerminal) Rows() int {
	if t.stdout != nil {
		if _, height, err := term.GetSize(int(t.stdout.Fd())); err == nil && height > 0 {
			return height
		}
	}
	if height, err := strconv.Atoi(os.Getenv("LINES")); err == nil && height > 0 {
		return height
	}
	return 24
}

// KittyProtocolActive reports whether this raw-mode session received non-zero
// Kitty keyboard protocol flags.
func (*ProcessTerminal) KittyProtocolActive() bool { return IsKittyProtocolActive() }

// MoveBy moves the cursor relative to its current row.
func (t *ProcessTerminal) MoveBy(lines int) {
	switch {
	case lines > 0:
		t.Write(fmt.Sprintf("\x1b[%dB", lines))
	case lines < 0:
		t.Write(fmt.Sprintf("\x1b[%dA", -lines))
	}
}

// HideCursor hides the terminal cursor.
func (t *ProcessTerminal) HideCursor() { t.Write("\x1b[?25l") }

// ShowCursor shows the terminal cursor.
func (t *ProcessTerminal) ShowCursor() { t.Write("\x1b[?25h") }

// ClearLine clears from the cursor to the end of the current line.
func (t *ProcessTerminal) ClearLine() { t.Write("\x1b[K") }

// ClearFromCursor clears from the cursor to the end of the screen.
func (t *ProcessTerminal) ClearFromCursor() { t.Write("\x1b[J") }

// ClearScreen clears the screen and homes the cursor.
func (t *ProcessTerminal) ClearScreen() { t.Write("\x1b[2J\x1b[H") }

// SetTitle writes an OSC 0 title update sequence.
func (t *ProcessTerminal) SetTitle(title string) { t.Write("\x1b]0;" + title + "\x07") }

// SetProgress writes the OSC 9;4 progress indicator and keeps it alive during agent work. Clearing stops the keepalive and writes OSC 9;4;0 followed directly by BEL.
func (t *ProcessTerminal) SetProgress(active bool) {
	if active {
		t.Write(terminalProgressActiveSeq)
		t.progressMu.Lock()
		defer t.progressMu.Unlock()
		if t.progressTicker != nil {
			return
		}
		keepalive := t.progressKeepalive
		if keepalive <= 0 {
			keepalive = terminalProgressKeepalive
		}
		ticker := time.NewTicker(keepalive)
		stopCh := make(chan struct{})
		t.progressTicker = ticker
		t.progressStop = stopCh
		done := make(chan struct{})
		t.progressDone = done
		go func() {
			defer close(done)
			for {
				select {
				case <-stopCh:
					return
				case <-ticker.C:
					select {
					case <-stopCh:
						return
					default:
					}
					t.Write(terminalProgressActiveSeq)
				}
			}
		}()
		return
	}
	t.clearProgressInterval()
	t.Write(terminalProgressClearSeq)
}

func (t *ProcessTerminal) clearProgressInterval() bool {
	t.progressMu.Lock()
	defer t.progressMu.Unlock()
	if t.progressTicker == nil {
		return false
	}
	t.progressTicker.Stop()
	close(t.progressStop)
	<-t.progressDone
	t.progressTicker = nil
	t.progressStop = nil
	t.progressDone = nil
	return true
}

// SetTerminalTitle writes an OSC 0 sequence to stdout to update the terminal
// window title. An empty title clears back to terminal default.
func SetTerminalTitle(title string) {
	processTerminal.SetTitle(title)
}

// SetTerminalProgress toggles the OSC 9;4 terminal progress indicator.
func SetTerminalProgress(active bool) {
	processTerminal.SetProgress(active)
}

// BuildTerminalTitle formats the pig terminal title from a session name
// (may be empty) and a cwd path. Matches upstream
// interactive-mode.ts pattern using APP_TITLE semantics.
func BuildTerminalTitle(sessionName, cwd string) string {
	base := filepath.Base(cwd)
	if base == "" || base == "." {
		base = cwd
	}
	if sessionName != "" {
		return fmt.Sprintf("%s - %s - %s", codingAgentAppTitle(), sessionName, base)
	}
	return fmt.Sprintf("%s - %s", codingAgentAppTitle(), base)
}

func codingAgentAppTitle() string {
	// Keep terminal package decoupled from internal/codingagent to avoid an import cycle.
	return "pig"
}
