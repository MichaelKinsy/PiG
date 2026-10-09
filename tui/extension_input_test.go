package tui

// pi: packages/coding-agent/src/modes/interactive/components/extension-input.ts

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestExtensionInputComponentRender(t *testing.T) {
	input := NewExtensionInputComponent("Rename Session", "session name", nil, nil)
	input.SetText("alpha")

	lines := input.Render(40)
	if len(lines) < 7 {
		t.Fatalf("Render returned %d lines, want >= 7", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Rename Session") {
		t.Fatalf("render missing title: %q", joined)
	}
	// Pi Input.setValue keeps the initial cursor before the first character.
	if !strings.Contains(joined, "> "+widthx.CursorMarker+"\x1b[7ma\x1b[27mlpha") {
		t.Fatalf("render missing bare input line: %q", joined)
	}
	if !strings.Contains(joined, "submit") || !strings.Contains(joined, "cancel") {
		t.Fatalf("render missing key hints: %q", joined)
	}
}

func TestExtensionInputComponentDelegatesDoneState(t *testing.T) {
	input := NewExtensionInputComponent("Prompt", "", nil, nil)
	input.HandleInput("h")
	input.HandleInput("i")
	input.HandleInput("\r")
	if !input.Done() {
		t.Fatal("expected done after submit")
	}
	if input.Cancelled() {
		t.Fatal("expected submitted input not to be cancelled")
	}
	if got := input.Text(); got != "hi" {
		t.Fatalf("Text() = %q want %q", got, "hi")
	}
}

// ExtensionInputComponent extends Container (extension-input.ts:18): border, spacer, title, spacer, input, spacer, hint,
// spacer, border are its children, and a countdown rewrites the title Text child.
func TestExtensionInputComponentIsAContainerWhoseCountdownRewritesItsTitleChild(t *testing.T) {
	e := NewExtensionInputComponent("Name", "", nil, nil)
	if got := len(e.Children()); got != 9 {
		t.Fatalf("children = %d, want 9", got)
	}
	if strings.Contains(stripANSI(strings.Join(e.Render(60), "\n")), "(5s)") {
		t.Fatal("title carries a countdown before one started")
	}
	e.SetCountdown(5)
	if !strings.Contains(stripANSI(strings.Join(e.Render(60), "\n")), "Name (5s)") {
		t.Fatalf("countdown title missing after SetCountdown:\n%s", strings.Join(e.Render(60), "\n"))
	}
}

type renderCounter struct {
	TUI
	renders chan struct{}
}

func (r *renderCounter) RequestRender(...bool) { r.renders <- struct{}{} }

// extension-input.ts handleInput: confirm (and a bare "\n") call onSubmit with the input's value, cancel calls onCancel,
// and nothing else is called.
func TestExtensionInputComponentCallsSubmitAndCancelCallbacks(t *testing.T) {
	var submitted []string
	cancels := 0
	e := NewExtensionInputComponent("Prompt", "", func(v string) { submitted = append(submitted, v) }, func() { cancels++ })
	e.HandleInput("h")
	e.HandleInput("i")
	if len(submitted) != 0 || cancels != 0 {
		t.Fatalf("typing called a callback: %v %d", submitted, cancels)
	}
	e.HandleInput("\r")
	if len(submitted) != 1 || submitted[0] != "hi" || cancels != 0 || !e.Done() {
		t.Fatalf("submit: %v cancels=%d done=%v", submitted, cancels, e.Done())
	}

	c := NewExtensionInputComponent("Prompt", "", func(v string) { submitted = append(submitted, v) }, func() { cancels++ })
	c.HandleInput("\x1b")
	if cancels != 1 || len(submitted) != 1 || !c.Cancelled() {
		t.Fatalf("cancel: %v cancels=%d cancelled=%v", submitted, cancels, c.Cancelled())
	}

	n := NewExtensionInputComponent("Prompt", "", func(v string) { submitted = append(submitted, v) }, nil)
	n.HandleInput("\n")
	if len(submitted) != 2 || submitted[1] != "" {
		t.Fatalf("a bare newline submits the value: %v", submitted)
	}
}

// extension-input.ts constructor: initialValue pre-fills the input and description is a text row under the title.
func TestExtensionInputComponentOptionsInitialValueAndDescription(t *testing.T) {
	e := NewExtensionInputComponent("Name", "", nil, nil, ExtensionInputOptions{InitialValue: "seed", Description: "Pick a name"})
	if got := e.Text(); got != "seed" {
		t.Fatalf("Text() = %q, want seed", got)
	}
	if got := len(e.Children()); got != 11 {
		t.Fatalf("children = %d, want 11 (spacer and description text added)", got)
	}
	plain := stripANSI(strings.Join(e.Render(60), "\n"))
	if strings.Index(plain, "Name") > strings.Index(plain, "Pick a name") || !strings.Contains(plain, "Pick a name") {
		t.Fatalf("description missing or above the title:\n%s", plain)
	}
	// The description is theme.fg("text", description) (extension-input.ts:54), padded as the title is.
	var descriptionLine string
	for _, line := range e.Render(60) {
		if strings.Contains(stripANSI(line), "Pick a name") {
			descriptionLine = line
		}
	}
	if want := NewPaddedText(ActiveTheme().Fg("text", "Pick a name"), 1, 0, nil).Render(60); len(want) != 1 || descriptionLine != want[0] {
		t.Fatalf("description line = %q, want the text-colored row %q", descriptionLine, want)
	}
	if got := NewExtensionInputComponent("Name", "", nil, nil, ExtensionInputOptions{}).Text(); got != "" {
		t.Fatalf("empty initialValue left %q", got)
	}
}

// extension-input.ts focused setter: the flag propagates to the input so the hardware cursor lands in it.
func TestExtensionInputComponentFocusPropagatesToInput(t *testing.T) {
	e := NewExtensionInputComponent("Name", "", nil, nil)
	if e.Focused() {
		t.Fatal("Focused starts false, as upstream's _focused")
	}
	e.SetFocused(true)
	if !e.Focused() || !e.input.Focused {
		t.Fatalf("focused=%v input=%v", e.Focused(), e.input.Focused)
	}
	e.SetFocused(false)
	if e.Focused() || e.input.Focused || strings.Contains(strings.Join(e.Render(40), ""), widthx.CursorMarker) {
		t.Fatal("unfocused input still emits the hardware cursor")
	}
}

// extension-input.ts: a positive timeout with a tui starts a CountdownTimer whose first tick titles the input at once
// and does not request a render; expiry calls onCancel; dispose stops the timer. No timeout or no tui means no timer.
func TestExtensionInputComponentCountdownOptions(t *testing.T) {
	r := &renderCounter{renders: make(chan struct{}, 4)}
	loop := make(chan func(), 4)
	cancels := 0
	e := NewExtensionInputComponent("Name", "", nil, func() { cancels++ }, ExtensionInputOptions{
		TUI: r, Timeout: time.Second, Dispatch: func(f func()) { loop <- f },
	})
	defer e.Dispose()
	if !strings.Contains(stripANSI(strings.Join(e.Render(60), "\n")), "Name (1s)") {
		t.Fatalf("first tick missing:\n%s", strings.Join(e.Render(60), "\n"))
	}
	select {
	case <-r.renders:
		t.Fatal("the constructor's first tick requested a render")
	default:
	}
	select {
	case tick := <-loop:
		tick()
	case <-time.After(10 * time.Second):
		t.Fatal("countdown did not tick")
	}
	if cancels != 1 {
		t.Fatalf("countdown expiry called onCancel %d times, want 1", cancels)
	}
	if !e.Done() || !e.Cancelled() {
		t.Fatalf("expiry left done=%v cancelled=%v", e.Done(), e.Cancelled())
	}
	if len(r.renders) == 0 {
		t.Fatal("expiry tick requested no render")
	}

	for name, opts := range map[string]ExtensionInputOptions{
		"no tui":     {Timeout: time.Second},
		"no timeout": {TUI: r},
		"negative":   {TUI: r, Timeout: -time.Second},
	} {
		if NewExtensionInputComponent("Name", "", nil, nil, opts).countdown != nil {
			t.Fatalf("%s started a countdown", name)
		}
	}

	d := NewExtensionInputComponent("Name", "", nil, nil, ExtensionInputOptions{TUI: r, Timeout: time.Hour})
	if d.countdown == nil {
		t.Fatal("no countdown for a positive timeout")
	}
	timer := d.countdown
	d.Dispose()
	if d.countdown != nil || !timer.stopped {
		t.Fatal("Dispose left the countdown running")
	}
	d.Dispose()
}
