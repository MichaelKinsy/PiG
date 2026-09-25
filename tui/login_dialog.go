package tui

// login_dialog.go: OAuth login dialog overlay.
//
// Faithful port of upstream login-dialog.ts: bordered overlay with
// title "Login to <Provider>", async state updates for the device-flow
// (URL, waiting, progress, manual input), and "(Esc to cancel)" footer.
//
// The component owns no I/O. The caller feeds state updates via the
// Show* methods and drives the input event loop.

import (
	"runtime"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// LoginDialog is a stateful bordered overlay for the OAuth device flow
// (port of LoginDialogComponent in login-dialog.ts).
type LoginDialog struct {
	invalidatable
	mu               sync.Mutex
	providerName     string
	lines            []string // content lines (plain text, may contain ANSI)
	inputPrompt      string   // set when input is requested
	inputPlaceholder string
	inputBuf         string
	inputActive      bool
	inputCh          chan string // receives user input on Enter
	done             bool
	cancelled        bool
	onCancel         func()
}

// NewLoginDialog creates the dialog for providerName.
func NewLoginDialog(providerName string, onCancel func()) *LoginDialog {
	return &LoginDialog{
		providerName: providerName,
		onCancel:     onCancel,
	}
}

// ShowAuth sets the content to URL + hint.
func (d *LoginDialog) ShowAuth(url, instructions string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Wrap the URL in an OSC 8 terminal hyperlink so terminals that support
	// it render it as a clickable link. Mirrors upstream login-dialog.ts.
	linkedURL := "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
	clickHint := " Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		clickHint = " Cmd+click to open"
	}
	d.lines = []string{
		" " + linkedURL,
		clickHint,
	}
	if instructions != "" {
		d.lines = append(d.lines, "")
		d.lines = append(d.lines, " "+instructions)
	}
	d.Invalidate()
}

// ShowWaiting appends a waiting message.
func (d *LoginDialog) ShowWaiting(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append(d.lines, " "+msg)
	d.lines = append(d.lines, " (Esc to cancel)")
	d.Invalidate()
}

// ShowProgress appends a progress line.
func (d *LoginDialog) ShowProgress(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append(d.lines, " "+msg)
	d.Invalidate()
}

// ShowInput activates the text input row with a prompt.
// Returns a channel that receives the submitted string (closed on cancel).
func (d *LoginDialog) ShowInput(prompt, placeholder string) <-chan string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inputPrompt = prompt
	d.inputPlaceholder = placeholder
	d.inputActive = true
	d.inputBuf = ""
	d.inputCh = make(chan string, 1)
	d.Invalidate()
	return d.inputCh
}

// HandleInput processes Esc and, when input is active, text editing keys.
func (d *LoginDialog) HandleInput(data string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done {
		return
	}
	// Esc or Ctrl+C cancels.
	if data == "\x1b" || data == "\x03" {
		d.done = true
		d.cancelled = true
		if d.inputCh != nil {
			close(d.inputCh)
			d.inputCh = nil
		}
		if d.onCancel != nil {
			d.onCancel()
		}
		return
	}
	if !d.inputActive {
		return
	}
	switch data {
	case "\n", "\r":
		if d.inputCh != nil {
			d.inputCh <- d.inputBuf
			close(d.inputCh)
			d.inputCh = nil
		}
		d.inputActive = false
		d.Invalidate()
	case "\x7f", "\b":
		if len(d.inputBuf) > 0 {
			d.inputBuf = d.inputBuf[:len(d.inputBuf)-1]
			d.Invalidate()
		}
	default:
		if len(data) > 0 && data[0] >= 0x20 {
			d.inputBuf += data
			d.Invalidate()
		}
	}
}

// Render returns bordered ANSI lines.
func (d *LoginDialog) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := ActiveTheme()
	border := NewDynamicBorder("")

	var out []string
	out = append(out, border.Render(width)...)
	// Upstream: Text(theme.fg("accent", theme.bold(title)), 1, 0).
	out = append(out, wrapWithIndent(" "+t.Accent+"\x1b[1mLogin to "+d.providerName+SGRBoldDimReset+t.Reset, width)...)
	out = append(out, "")

	for _, line := range d.lines {
		out = append(out, wrapWithIndent(line, width)...)
	}

	if d.inputActive {
		out = append(out, "")
		out = append(out, wrapWithIndent(" "+d.inputPrompt, width)...)
		if d.inputPlaceholder != "" {
			out = append(out, wrapWithIndent(" e.g., "+d.inputPlaceholder, width)...)
		}
		out = append(out, wrapWithIndent("> "+d.inputBuf, width)...)
		out = append(out, wrapWithIndent(" (escape/ctrl+c to cancel, enter to submit)", width)...)
	}

	out = append(out, "")
	out = append(out, border.Render(width)...)

	// Pad lines to width.
	for i, line := range out {
		w := widthx.VisibleWidth(line)
		if w < width {
			out[i] = line + strings.Repeat(" ", width-w)
		}
	}
	return out
}

// Done reports whether the dialog is finished.
func (d *LoginDialog) Done() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.done
}

// Cancelled reports whether the user pressed Esc.
func (d *LoginDialog) Cancelled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancelled
}

// Success marks the dialog as completed successfully.
func (d *LoginDialog) Success() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append(d.lines, " ✓ Login successful")
	d.done = true
	d.Invalidate()
}

// Compile-time check.
var _ Component = (*LoginDialog)(nil)

// wrapWithIndent renders `line` the way upstream `Text(content, paddingX, 0)`
// does in @earendil-works/pi-tui, taking the leading-space count as paddingX:
// the content wraps inside that padding on both sides (the padding shrinks
// when the width cannot hold it), and every row fits the width. OSC 8
// hyperlink sequences are zero-width, so a hyperlinked URL wraps by its
// visible text.
func wrapWithIndent(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	body := strings.TrimLeft(line, " ")
	indent := len(line) - len(body)
	if body == "" {
		return []string{""}
	}
	return NewPaddedText(body, indent, 0, nil).Render(width)
}
