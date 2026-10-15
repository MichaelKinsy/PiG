package tui

// Ports packages/coding-agent/src/modes/interactive/components/extension-input.ts

import "time"

// ExtensionInputOptions mirrors upstream ExtensionInputOptions (extension-input.ts:12-17).
type ExtensionInputOptions struct {
	// TUI receives a render request on every countdown tick.
	TUI TUI
	// Timeout is how long the input waits before it cancels itself; zero or less disables the countdown, which also needs TUI.
	Timeout time.Duration
	// InitialValue pre-fills the input.
	InitialValue string
	// Description is shown under the title.
	Description string
	// Dispatch runs each countdown second on the loop that owns the component; upstream's interval runs on its event loop. A nil Dispatch runs it on the timer goroutine.
	Dispatch func(func())
}

// ExtensionInputComponent wraps a bare TextInput with extension-style chrome.
type ExtensionInputComponent struct {
	Container
	input     *TextInput
	title     string
	baseTitle string
	done      bool
	cancelled bool
	focused   bool

	onSubmit  func(value string)
	onCancel  func()
	countdown *CountdownTimer

	// The title Text is built when the component is constructed and rewritten for a countdown tick, as upstream builds each Text with theme.fg; a theme change leaves it as built.
	titleText *Text
}

// NewExtensionInputComponent creates the editor-slot extension input wrapper, as upstream's constructor does
// (extension-input.ts:33-78). Placeholder is accepted for API parity; upstream ignores it. onSubmit receives the value
// when the user confirms and onCancel runs when the user cancels or the countdown expires; either may be nil, because a
// host that polls Done and Cancelled needs neither. The host-driven Done, Cancelled and Text state is set before the
// callback runs.
func NewExtensionInputComponent(title, placeholder string, onSubmit func(value string), onCancel func(), options ...ExtensionInputOptions) *ExtensionInputComponent {
	_ = placeholder
	var opts ExtensionInputOptions
	if len(options) > 0 {
		opts = options[0]
	}
	input := NewInput(InputOptions{})
	// This host wrapper occupies the active editor slot.
	input.Focused = true
	e := &ExtensionInputComponent{
		input:     input,
		title:     title,
		baseTitle: title,
		titleText: NewPaddedText(ActiveTheme().Fg("accent", title), 1, 0, nil),
		onSubmit:  onSubmit,
		onCancel:  onCancel,
	}
	// upstream: extension-input.ts constructor children.
	e.Add(NewDynamicBorder())
	e.Add(NewSpacer(1))
	e.Add(e.titleText)
	if opts.Description != "" {
		e.Add(NewSpacer(1))
		e.Add(NewPaddedText(ActiveTheme().Fg("text", opts.Description), 1, 0, nil))
	}
	e.Add(NewSpacer(1))
	if opts.Timeout > 0 && opts.TUI != nil {
		// Upstream requests a render on each interval tick, not on the first tick the constructor makes.
		started := false
		e.countdown = NewCountdownTimer(opts.Timeout, opts.Dispatch, func(seconds int) {
			e.SetCountdown(seconds)
			if started {
				opts.TUI.RequestRender()
			}
		}, e.expire)
		started = true
	}
	if opts.InitialValue != "" {
		input.SetText(opts.InitialValue)
	}
	e.Add(input)
	e.Add(NewSpacer(1))
	e.Add(NewPaddedText(extensionActionHint(KBSelectConfirm, "submit")+"  "+extensionActionHint(KBSelectCancel, "cancel"), 1, 0, nil))
	e.Add(NewSpacer(1))
	e.Add(NewDynamicBorder())
	return e
}

// SetCountdown shows the seconds left before the input times out, as
// upstream's countdown sets the title to `${baseTitle} (${s}s)`.
func (e *ExtensionInputComponent) SetCountdown(seconds int) {
	e.title = countdownTitle(e.baseTitle, seconds)
	e.titleText.SetText(ActiveTheme().Fg("accent", e.title))
	e.Invalidate()
}

// StartCountdown starts the timer that shows the seconds left in the title and, at zero, cancels the input (the constructor's opts.timeout branch, which owns a CountdownTimer). dispatch runs each second on the loop that owns the input; onTick runs after each title update and onExpire after the cancellation. A running countdown is replaced.
func (e *ExtensionInputComponent) StartCountdown(timeout time.Duration, dispatch func(func()), onTick, onExpire func()) {
	e.Dispose()
	e.countdown = NewCountdownTimer(timeout, dispatch, func(seconds int) {
		e.SetCountdown(seconds)
		onTick()
	}, func() {
		e.Cancel()
		onExpire()
	})
}

// Cancel completes the input as cancelled, as upstream's countdown expiry calls
// onCancel.
func (e *ExtensionInputComponent) Cancel() {
	if e.done {
		return
	}
	e.done = true
	e.cancelled = true
	e.Invalidate()
}

// expire is the countdown's expiry: upstream calls onCancelCallback.
func (e *ExtensionInputComponent) expire() {
	if e.done {
		return
	}
	e.Cancel()
	if e.onCancel != nil {
		e.onCancel()
	}
}

// Dispose stops the countdown. Mirrors upstream dispose (extension-input.ts:95-97).
func (e *ExtensionInputComponent) Dispose() {
	if e.countdown != nil {
		e.countdown.Dispose()
		e.countdown = nil
	}
}

// Focused reports the Focusable flag (upstream get focused, extension-input.ts:30-32).
func (e *ExtensionInputComponent) Focused() bool { return e.focused }

// SetFocused implements Focusable: the flag propagates to the input so the hardware cursor lands in it (upstream set
// focused, extension-input.ts:33-36).
func (e *ExtensionInputComponent) SetFocused(focused bool) {
	e.focused = focused
	e.input.SetFocused(focused)
}

// Done reports whether the user submitted or cancelled the input.
func (e *ExtensionInputComponent) Done() bool { return e.done }

// Cancelled reports whether the user cancelled the input.
func (e *ExtensionInputComponent) Cancelled() bool { return e.cancelled }

// Text returns the current value.
func (e *ExtensionInputComponent) Text() string { return e.input.Text() }

// SetText pre-fills the input.
func (e *ExtensionInputComponent) SetText(s string) { e.input.SetText(s) }

// HandleInput resolves selection actions before delegating text editing to Input. The inner input's submit action does not complete the dialog.
func (e *ExtensionInputComponent) HandleInput(data string) {
	if e.done {
		return
	}
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectConfirm) || data == "\n":
		e.done = true
		if e.onSubmit != nil {
			e.onSubmit(e.input.Text())
		}
	case kb.Matches(data, KBSelectCancel):
		e.done = true
		e.cancelled = true
		if e.onCancel != nil {
			e.onCancel()
		}
	default:
		e.input.HandleInput(data)
	}
	e.Invalidate()
}
