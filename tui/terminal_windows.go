//go:build windows

package tui

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// resizePollInterval is how often the Windows resize watcher samples the
// console size. Windows has no SIGWINCH, so pig polls (the spike on the
// joe-jump host confirmed GetConsoleScreenBufferInfo tracks live resizes over
// ConPTY). ~120ms is imperceptible for re-render yet cheap.
const resizePollInterval = 120 * time.Millisecond

// pollReadable is a no-op drain probe on Windows: it always reports "not
// readable" so DrainInput returns immediately without ever issuing a blocking
// console read.
//
// The unix drain guards against slow-SSH key-release bytes leaking into the
// parent shell on exit. Windows skips the drain, so a restore never issues a
// console read that could block.
func pollReadable(_ uintptr, _ int) (int, error) { return 0, nil }

type terminalInputWaiter struct {
	ctx          context.Context
	cancelEvent  windows.Handle
	stopCallback func() bool
	callbackDone chan struct{}
}

func newTerminalInputWaiter(ctx context.Context) (*terminalInputWaiter, error) {
	cancelEvent, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	w := &terminalInputWaiter{
		ctx:          ctx,
		cancelEvent:  cancelEvent,
		callbackDone: make(chan struct{}),
	}
	w.stopCallback = context.AfterFunc(ctx, func() {
		_ = windows.SetEvent(cancelEvent)
		close(w.callbackDone)
	})
	return w, nil
}

// wait waits for either console input or cancellation before issuing ReadFile.
// Stop therefore cannot leave an old reader blocked inside the read that
// belongs to the next terminal owner.
func (w *terminalInputWaiter) wait(file *os.File) (bool, error) {
	event, err := windows.WaitForMultipleObjects(
		[]windows.Handle{windows.Handle(file.Fd()), w.cancelEvent},
		false,
		windows.INFINITE,
	)
	if w.ctx.Err() != nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return event == windows.WAIT_OBJECT_0, nil
}

func (w *terminalInputWaiter) close() {
	if !w.stopCallback() {
		<-w.callbackDone
	}
	_ = windows.CloseHandle(w.cancelEvent)
}

// consoleInputs holds one reader per console input handle. Every terminal that
// reads the handle shares it, so input one terminal took from the console but
// has not delivered passes to the next terminal, as it did when all of them
// read os.Stdin.
var consoleInputs sync.Map // windows.Handle -> *consoleInput

// terminalInput returns the reader for keyboard input from file: the shared
// consoleInput for a console handle, and file itself for anything else.
func terminalInput(file *os.File) io.Reader {
	handle := windows.Handle(file.Fd())
	if c, ok := consoleInputs.Load(handle); ok {
		return c.(*consoleInput)
	}
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return file
	}
	c, _ := consoleInputs.LoadOrStore(handle, &consoleInput{handle: handle})
	return c.(*consoleInput)
}

// terminalInputBuffered reports whether file's reader holds input it has
// already taken from the console. That input does not signal the handle.
func terminalInputBuffered(file *os.File) bool {
	c, ok := terminalInput(file).(*consoleInput)
	return ok && c.buffered()
}

// terminalInputPending reports, after file's handle has signalled, whether a
// read returns without blocking. See consoleInput.pending.
func terminalInputPending(file *os.File) (bool, error) {
	c, ok := terminalInput(file).(*consoleInput)
	if !ok {
		return true, nil
	}
	return c.pending()
}

// consoleInput reads a console input handle as UTF-8. os.File.Read on a console
// reports a Ctrl+Z (0x1A) at the start of a read as end of file and ends a read
// before a later one. Node's TTY, which Pi reads, delivers Ctrl+Z as a key, and
// Pi binds it to undo on win32.
type consoleInput struct {
	mu     sync.Mutex
	handle windows.Handle
	units  [256]uint16
	high   rune // a high surrogate whose low half the next read returns
	buf    []byte
	off    int
}

func (c *consoleInput) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.off == len(c.buf) {
		var n uint32
		if err := windows.ReadConsole(c.handle, &c.units[0], uint32(len(c.units)), &n, nil); err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		c.buf, c.off = c.appendUTF8(c.buf[:0], c.units[:n]), 0
	}
	n := copy(b, c.buf[c.off:])
	c.off += n
	return n, nil
}

func (c *consoleInput) buffered() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.off < len(c.buf)
}

// pending removes the leading console input records that yield no character,
// such as key releases, bare modifier presses and focus changes, and reports
// whether a read now returns without blocking. ReadConsoleW skips those
// records too, but when they are all the console holds it blocks until the
// next keystroke, where a stopped terminal's reader could not be ended.
func (c *consoleInput) pending() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.off < len(c.buf) {
		return true, nil
	}
	var records [64]inputRecord
	for {
		n, err := peekConsoleInput(c.handle, records[:])
		if err != nil || n == 0 {
			return false, err
		}
		skip := 0
		for skip < n && !records[skip].yieldsCharacter() {
			skip++
		}
		if skip > 0 {
			if _, err := readConsoleInput(c.handle, records[:skip]); err != nil {
				return false, err
			}
		}
		if skip < n {
			return true, nil
		}
	}
}

// inputRecord is the Win32 INPUT_RECORD: an event type and its event.
type inputRecord struct {
	EventType uint16
	_         uint16
	Event     [16]byte
}

// yieldsCharacter reports whether ReadConsoleW can return a character for the
// record in raw virtual-terminal input mode. A key press does unless it only
// presses a modifier; with ENABLE_VIRTUAL_TERMINAL_INPUT, arrows and function
// keys yield escape sequences. A release yields nothing, except releasing Alt
// after a code typed on the numeric keypad, which delivers the composed
// character.
func (r *inputRecord) yieldsCharacter() bool {
	if r.EventType != windows.KEY_EVENT {
		return false
	}
	// KEY_EVENT_RECORD: bKeyDown, wRepeatCount, wVirtualKeyCode,
	// wVirtualScanCode, uChar, dwControlKeyState.
	keyDown := binary.LittleEndian.Uint32(r.Event[0:4]) != 0
	virtualKey := binary.LittleEndian.Uint16(r.Event[6:8])
	char := binary.LittleEndian.Uint16(r.Event[10:12])
	if !keyDown {
		return virtualKey == windows.VK_MENU && char != 0
	}
	switch virtualKey {
	case windows.VK_SHIFT, windows.VK_CONTROL, windows.VK_MENU, windows.VK_CAPITAL,
		windows.VK_NUMLOCK, windows.VK_SCROLL, windows.VK_LWIN, windows.VK_RWIN,
		windows.VK_LSHIFT, windows.VK_RSHIFT, windows.VK_LCONTROL, windows.VK_RCONTROL,
		windows.VK_LMENU, windows.VK_RMENU:
		return char != 0
	}
	return true
}

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procPeekConsoleInputW = kernel32.NewProc("PeekConsoleInputW")
	procReadConsoleInputW = kernel32.NewProc("ReadConsoleInputW")
)

// peekConsoleInput copies the console's queued input records into records
// without removing them and returns how many it copied.
func peekConsoleInput(handle windows.Handle, records []inputRecord) (int, error) {
	return consoleInputCall(procPeekConsoleInputW, handle, records)
}

// readConsoleInput removes len(records) queued input records into records.
func readConsoleInput(handle windows.Handle, records []inputRecord) (int, error) {
	return consoleInputCall(procReadConsoleInputW, handle, records)
}

func consoleInputCall(proc *windows.LazyProc, handle windows.Handle, records []inputRecord) (int, error) {
	var n uint32
	r1, _, err := proc.Call(uintptr(handle), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&n))) //nolint:gosec // G103: PeekConsoleInputW and ReadConsoleInputW fill the INPUT_RECORD array inputRecord mirrors and write the count, and x/sys has no wrapper for them.
	if r1 == 0 {
		return 0, err
	}
	return int(n), nil
}

// appendUTF8 appends units to buf as UTF-8, joining a surrogate pair split
// across reads and replacing an unpaired surrogate with U+FFFD.
func (c *consoleInput) appendUTF8(buf []byte, units []uint16) []byte {
	for _, unit := range units {
		r := rune(unit)
		if c.high != 0 {
			pair := utf16.DecodeRune(c.high, r)
			c.high = 0
			if pair != utf8.RuneError {
				buf = utf8.AppendRune(buf, pair)
				continue
			}
			buf = utf8.AppendRune(buf, utf8.RuneError)
		}
		if r >= 0xD800 && r < 0xDC00 {
			c.high = r
			continue
		}
		buf = utf8.AppendRune(buf, r)
	}
	return buf
}

// isEINTR reports whether a read error is an interrupted syscall. Windows has
// no EINTR, so this is always false.
func isEINTR(_ error) bool { return false }

// startResizeWatcher polls the console size and invokes onResize on change,
// kicking once on startup. Returns a stop func; the goroutine also exits when
// ctx is cancelled. This is pig's platform mechanism for the same observable
// "resize -> re-render" behavior upstream gets from Node's stdout "resize"
// event (no user-visible divergence).
func (t *ProcessTerminal) startResizeWatcher(ctx context.Context, onResize func()) func() {
	go func() {
		ticker := time.NewTicker(resizePollInterval)
		defer ticker.Stop()
		lastW, lastH := t.Columns(), t.Rows()
		onResize() // startup kick, mirroring the unix SIGWINCH self-send
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, h := t.Columns(), t.Rows()
				if w != lastW || h != lastH {
					lastW, lastH = w, h
					onResize()
				}
			}
		}
	}()
	return func() {} // goroutine stops on ctx cancellation
}

// enableVTProcessing configures the output console for pig's ANSI renderer and
// returns a restore func. No-op when stdout is not a console.
//
// Two flags matter, together giving the same exact cursor control pig assumes
// on unix:
//   - ENABLE_VIRTUAL_TERMINAL_PROCESSING: interpret pig's ANSI escapes.
//   - DISABLE_NEWLINE_AUTO_RETURN: stop conhost from auto-scrolling / returning
//     the cursor on a write to the last column/row. Without it, bottom-row
//     writes desync pig's cursor-precise incremental renderer (symptom: typed
//     input not painting until a full-clear repaint forced by a resize).
func (t *ProcessTerminal) enableVTProcessing() func() {
	if t.stdout == nil {
		return func() {}
	}
	h := windows.Handle(t.stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return func() {} // not a console (redirected / print mode)
	}
	want := mode | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN
	if want == mode {
		return func() {} // already configured
	}
	if err := windows.SetConsoleMode(h, want); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
