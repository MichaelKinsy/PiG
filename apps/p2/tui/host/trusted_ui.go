package host

// Trusted consent UI (p2-tui-port.md section 3.3), drawn by the launcher only. While it is active the client suspends worker IO: worker output is
// discarded (a full redraw follows) and every key is consumed here, never forwarded. It shows launcher-known facts and explicit data as data;
// control characters are made visible, never interpreted.
//
// pi: apps/p2/src/hosts/tui-trusted-ui.ts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ConsentFact is one launcher-known fact of a consent screen.
type ConsentFact struct{ Label, Value string }

// ConsentRequest is what a consent screen shows. Facts are launcher-known (origin, action, path, size); Preview is the exact data.
type ConsentRequest struct {
	// Action is the action in the launcher's words, e.g. "Copy 1 234 characters to the clipboard".
	Action string
	Facts  []ConsentFact
	// Preview is the full data the action uses (text, URL, patch); scrollable. HasPreview tells an empty preview from none.
	Preview    string
	HasPreview bool
	// ConfirmLabel defaults to "confirm".
	ConfirmLabel string
	// Ask false (`ask: false`): the launcher runs the action without the screen. Only for requests the server could make happen by itself, so asking
	// protects nothing (a dev restart of the server the runtime came from).
	Ask *bool
}

// Output is the terminal the trusted UI draws on.
type Output interface {
	Write(data string)
	Columns() int
	Rows() int
}

// VisibleText makes every control character a visible symbol (␛, ␇, …), so data can never act on the terminal.
func VisibleText(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case (r >= 0 && r <= 8) || (r >= 0x0b && r <= 0x1f):
			b.WriteRune(0x2400 + r)
		case r == 0x7f:
			b.WriteRune(0x2421)
		case r >= 0x80 && r <= 0x9f:
			b.WriteRune(0xfffd)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func wrapText(text string, width int) []string {
	var lines []string
	text = strings.ReplaceAll(VisibleText(text), "\t", "  ")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	for _, raw := range strings.Split(text, "\n") {
		line := ""
		for _, char := range raw {
			if widthx.VisibleWidth(line+string(char)) > width {
				lines = append(lines, line)
				line = ""
			}
			line += string(char)
		}
		lines = append(lines, line)
	}
	return lines
}

type pickerEntry struct {
	name      string
	directory bool
}

type uiKind int

const (
	uiConfirm uiKind = iota + 1
	uiNotice
	uiPicker
)

type uiState struct {
	kind uiKind
	// confirm
	request ConsentRequest
	offset  int
	// notice
	text  string
	timer *time.Timer
	// picker
	title     string
	directory string
	entries   []pickerEntry
	filter    string
	selected  int
	err       string

	confirmed chan bool
	picked    chan *string
	noticed   chan struct{}
}

// TrustedUi is the launcher-drawn UI.
type TrustedUi struct {
	output Output
	origin string
	mu     sync.Mutex
	// OnClose is called when the UI closes, so the client restores worker modes and asks the worker for a full redraw.
	OnClose func()
	current *uiState
}

// NewTrustedUi draws on output for the server at origin.
func NewTrustedUi(output Output, origin string) *TrustedUi {
	return &TrustedUi{output: output, origin: origin, OnClose: func() {}}
}

// Active is whether launcher UI shows.
func (u *TrustedUi) Active() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.current != nil
}

// Confirm blocks until the screen closes; it is true only after Enter or `y` on this screen; anything else cancels or is ignored.
func (u *TrustedUi) Confirm(request ConsentRequest) bool {
	u.mu.Lock()
	if u.current != nil {
		u.mu.Unlock()
		return false
	}
	state := &uiState{kind: uiConfirm, request: request, confirmed: make(chan bool, 1)}
	u.current = state
	u.render()
	u.mu.Unlock()
	return <-state.confirmed
}

// Notice shows a one-line launcher summary ("Copied …") on the bottom row, over the worker's screen, and blocks until it closes. It still suspends
// worker IO while shown (rule 4); a key or click ends it early and is consumed.
func (u *TrustedUi) Notice(text string, d time.Duration) {
	u.mu.Lock()
	if u.current != nil {
		u.mu.Unlock()
		return
	}
	state := &uiState{kind: uiNotice, text: text, noticed: make(chan struct{})}
	state.timer = time.AfterFunc(d, func() { u.close(state, false) })
	u.current = state
	u.render()
	u.mu.Unlock()
	<-state.noticed
}

// Pick is the launcher's local file picker (section 3.2 `attach`): it browses this computer's directories; the worker never sees names or paths. It
// blocks and returns the chosen file's path, or false when cancelled.
func (u *TrustedUi) Pick(title, start string) (string, bool) {
	u.mu.Lock()
	if u.current != nil {
		u.mu.Unlock()
		return "", false
	}
	directory, err := filepath.Abs(start)
	if err != nil {
		directory = start
	}
	state := &uiState{kind: uiPicker, title: title, directory: directory, picked: make(chan *string, 1)}
	u.current = state
	u.readDirectory(state)
	u.render()
	u.mu.Unlock()
	path := <-state.picked
	if path == nil {
		return "", false
	}
	return *path, true
}

func (u *TrustedUi) readDirectory(state *uiState) {
	entries, err := os.ReadDir(state.directory)
	state.entries = nil
	state.err = ""
	if err != nil {
		state.err = "unreadable"
		if os.IsNotExist(err) {
			state.err = "ENOENT"
		} else if os.IsPermission(err) {
			state.err = "EACCES"
		}
	}
	for _, entry := range entries {
		directory := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(state.directory, entry.Name())); err == nil {
				directory = info.IsDir()
			}
		}
		state.entries = append(state.entries, pickerEntry{entry.Name(), directory})
	}
	sort.SliceStable(state.entries, func(i, j int) bool {
		a, b := state.entries[i], state.entries[j]
		if a.directory != b.directory {
			return a.directory
		}
		return a.name < b.name
	})
	state.filter = ""
	state.selected = 0
}

func (u *TrustedUi) pickerRows(state *uiState) []pickerEntry {
	rows := state.entries
	if state.filter != "" {
		rows = pitui.FuzzyFilter(state.entries, state.filter, func(e pickerEntry) string { return e.name })
	}
	return append([]pickerEntry{{"..", true}}, rows...)
}

var mouseReport = lazyregexp.New(`^\x1b\[<(\d+);\d+;\d+([Mm])$`)

func (u *TrustedUi) pickerInput(state *uiState, data string) {
	rows := u.pickerRows(state)
	page := max(1, u.output.Rows()-6)
	key := func(id string) bool { return pitui.MatchesKeyID(data, id) }
	switch {
	case key("escape") || key("ctrl+c"):
		u.closeLocked(state, false)
		return
	case key("up"):
		state.selected = max(0, state.selected-1)
	case key("down"):
		state.selected = min(len(rows)-1, state.selected+1)
	case key("pageUp"):
		state.selected = max(0, state.selected-page)
	case key("pageDown"):
		state.selected = min(len(rows)-1, state.selected+page)
	case key("backspace"):
		if state.filter != "" {
			runes := []rune(state.filter)
			state.filter = string(runes[:len(runes)-1])
			state.selected = 0
		} else {
			state.directory = filepath.Dir(state.directory)
			u.readDirectory(state)
		}
	case key("enter"):
		if state.selected >= len(rows) {
			return
		}
		row := rows[state.selected]
		path := filepath.Join(state.directory, row.name)
		if row.name == ".." {
			path = filepath.Dir(state.directory)
		}
		if row.directory {
			state.directory = path
			u.readDirectory(state)
		} else {
			u.current = nil
			u.callOnClose()
			state.picked <- &path
			return
		}
	default:
		text, ok := pitui.DecodeKittyPrintable(data)
		if !ok && len(data) == 1 && data[0] >= 0x20 && data[0] != 0x7f {
			text, ok = data, true
		}
		if !ok || text == "" {
			return
		}
		state.filter += text
		state.selected = 0
	}
	u.render()
}

// HandleInput consumes every input while active.
func (u *TrustedUi) HandleInput(data string) {
	u.mu.Lock()
	current := u.current
	if current == nil || pitui.IsKeyRelease(data) {
		u.mu.Unlock()
		return
	}
	switch current.kind {
	case uiPicker:
		u.pickerInput(current, data)
		u.mu.Unlock()
		return
	case uiNotice:
		// Pointer movement, wheel and releases do not end it; a key or a click does.
		mouse := mouseReport.FindStringSubmatch(data)
		code := 0
		if mouse != nil {
			code, _ = strconv.Atoi(mouse[1])
		}
		if mouse == nil || (mouse[2] == "M" && code&96 == 0) {
			u.mu.Unlock()
			u.close(current, false)
			return
		}
		u.mu.Unlock()
		return
	}
	key := func(id string) bool { return pitui.MatchesKeyID(data, id) }
	switch {
	case key("enter") || key("y") || key("shift+y"):
		u.mu.Unlock()
		u.close(current, true)
		return
	case key("escape") || key("n") || key("shift+n") || key("ctrl+c"):
		u.mu.Unlock()
		u.close(current, false)
		return
	}
	page := max(1, u.previewHeight(current))
	delta := 0
	switch {
	case key("up"):
		delta = -1
	case key("down"):
		delta = 1
	case key("pageUp"):
		delta = -page
	case key("pageDown"):
		delta = page
	}
	if delta != 0 {
		current.offset = max(0, min(current.offset+delta, len(u.previewLines(current))-page))
		u.render()
	}
	u.mu.Unlock()
}

// Cancel cancels whatever is shown (client closing, worker swap).
func (u *TrustedUi) Cancel() {
	u.mu.Lock()
	current := u.current
	u.mu.Unlock()
	if current != nil {
		u.close(current, false)
	}
}

func (u *TrustedUi) callOnClose() {
	// The close hook writes to the terminal and posts to the worker: never under the lock.
	u.mu.Unlock()
	defer u.mu.Lock()
	u.OnClose()
}

func (u *TrustedUi) close(state *uiState, confirmed bool) {
	u.mu.Lock()
	u.closeLocked(state, confirmed)
	u.mu.Unlock()
}

// closeLocked ends state (u.mu held on entry and exit; released around the OnClose hook).
func (u *TrustedUi) closeLocked(state *uiState, confirmed bool) {
	if u.current != state {
		return
	}
	u.current = nil
	if state.kind != uiNotice {
		u.output.Write("\x1b[0m\x1b[2J\x1b[H")
	}
	u.callOnClose()
	switch state.kind {
	case uiConfirm:
		state.confirmed <- confirmed
	case uiPicker:
		state.picked <- nil
	default:
		state.timer.Stop()
		close(state.noticed)
	}
}

func (u *TrustedUi) width() int { return max(20, u.output.Columns()) }

func (u *TrustedUi) previewLines(state *uiState) []string {
	if state.kind == uiConfirm && state.request.HasPreview {
		return wrapText(state.request.Preview, u.width()-4)
	}
	return nil
}

func (u *TrustedUi) previewHeight(state *uiState) int {
	if state.kind != uiConfirm {
		return 0
	}
	return max(1, u.output.Rows()-7-len(state.request.Facts))
}

// Render redraws on a known terminal state (section 3.3 step 2); also called on resize.
func (u *TrustedUi) Render() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.render()
}

func (u *TrustedUi) render() {
	state := u.current
	if state == nil {
		return
	}
	width := u.width()
	line := func(text string) string { return widthx.TruncateToWidth(text, width, "…", false) }
	bar := func(text string) string {
		padded := " " + text
		if n := width - widthx.VisibleWidth(padded); n > 0 {
			padded += strings.Repeat(" ", n)
		}
		return "\x1b[7m" + line(padded) + "\x1b[27m"
	}
	rows := u.output.Rows()
	if state.kind == uiNotice {
		// Only the bottom row is drawn (the transcript stays visible); position and attributes are absolute.
		u.output.Write(fmt.Sprintf("\x1b[?2026l\x1b[0m\x1b[r\x1b[%d;1H\x1b[2K%s", max(1, rows), bar("p2 launcher: "+VisibleText(state.text))))
		return
	}
	lines := []string{bar("p2 launcher · on this computer · not the server's screen")}
	if state.kind == uiPicker {
		pick := u.pickerRows(state)
		height := max(1, rows-6)
		offset := max(0, min(state.selected-height/2, len(pick)-height))
		lines = append(lines, line(" "+VisibleText(state.title)))
		suffix := ""
		if state.err != "" {
			suffix = "  (" + state.err + ")"
		}
		lines = append(lines, line(" "+VisibleText(state.directory)+suffix))
		lines = append(lines, line(" Filter: "+VisibleText(state.filter)))
		for index := offset; index < min(len(pick), offset+height); index++ {
			row := pick[index]
			text := " " + VisibleText(row.name)
			if row.directory {
				text += "/"
			}
			if index == state.selected {
				lines = append(lines, "\x1b[7m"+line(">"+text)+"\x1b[27m")
			} else {
				lines = append(lines, line(" "+text))
			}
		}
		lines = append(lines, line(" Enter: open or choose · Backspace: up · type to filter · Esc: cancel"))
		u.output.Write("\x1b[?2026l\x1b[0m\x1b[r\x1b[?25h\x1b[2J\x1b[H" + strings.Join(lines[:min(len(lines), rows)], "\r\n"))
		return
	}
	request := state.request
	lines = append(lines, line(" Server: "+VisibleText(u.origin)), line(" Action: "+VisibleText(request.Action)))
	for _, fact := range request.Facts {
		lines = append(lines, line(" "+VisibleText(fact.Label)+": "+VisibleText(fact.Value)))
	}
	preview := u.previewLines(state)
	if request.HasPreview {
		height := u.previewHeight(state)
		lines = append(lines, line("\x1b[2m"+strings.Repeat("─", width)+"\x1b[22m"))
		for index := state.offset; index < min(len(preview), state.offset+height); index++ {
			lines = append(lines, line("  "+preview[index]))
		}
		lines = append(lines, line(fmt.Sprintf("\x1b[2m%s %d-%d of %d lines\x1b[22m", strings.Repeat("─", max(0, width-30)), state.offset+1, min(len(preview), state.offset+height), len(preview))))
	}
	label := request.ConfirmLabel
	if label == "" {
		label = "confirm"
	}
	scroll := ""
	if len(preview) > 0 {
		scroll = " · ↑↓ PgUp PgDn: scroll"
	}
	lines = append(lines, line(" Enter or y: "+label+" · Esc or n: cancel"+scroll))
	// End synchronized output, reset SGR and the scroll region, show the cursor, clear: a state the worker cannot spoof.
	u.output.Write("\x1b[?2026l\x1b[0m\x1b[r\x1b[?25h\x1b[2J\x1b[H" + strings.Join(lines[:min(len(lines), rows)], "\r\n"))
}
