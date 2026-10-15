package tui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// upstream: extension-input.ts:60-76 and extension-selector.ts:56-72 (a positive opts.timeout creates a CountdownTimer whose tick rewrites the title to `${baseTitle} (${s}s)` and whose expiry calls onCancel) and dispose() 91-93 / 114-116 (countdown?.dispose()). The countdown shows the seconds at once, cancels the dialog at zero, and a disposed countdown never cancels.
func TestExtensionDialogsOwnTheirCountdown(t *testing.T) {
	type dialog struct {
		name      string
		start     func(time.Duration, func(func()), func(), func())
		dispose   func()
		render    func() string
		cancelled func() bool
	}
	dialogs := []func() dialog{
		func() dialog {
			c := NewExtensionInputComponent("Name", "", nil, nil)
			return dialog{"input", func(d time.Duration, dispatch func(func()), tick, expire func()) {
				c.StartCountdown(d, dispatch, tick, expire)
			}, c.Dispose, func() string { return widthx.StripAnsi(strings.Join(c.Render(40), "\n")) }, c.Cancelled}
		},
		func() dialog {
			c := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, nil, nil)
			return dialog{"selector", func(d time.Duration, dispatch func(func()), tick, expire func()) {
				c.StartCountdown(d, dispatch, tick, expire)
			}, c.Dispose, func() string { return widthx.StripAnsi(strings.Join(c.Render(40), "\n")) }, c.Cancelled}
		},
	}
	for _, newDialog := range dialogs {
		d := newDialog()
		t.Run(d.name+" expires", func(t *testing.T) {
			// Each countdown second reaches this goroutine through dispatch, as production hands it to the UI loop, so the test reads the dialog without a race.
			seconds := make(chan func(), 4)
			var ticks, expired atomic.Int32
			d.start(time.Second, func(second func()) { seconds <- second }, func() { ticks.Add(1) }, func() { expired.Add(1) })
			if got := d.render(); !strings.Contains(got, "(1s)") {
				t.Fatalf("the title must show the seconds at once:\n%s", got)
			}
			select {
			case second := <-seconds:
				second()
			case <-time.After(5 * time.Second):
				t.Fatal("the countdown never reached zero")
			}
			if !d.cancelled() || expired.Load() != 1 || ticks.Load() < 1 {
				t.Fatalf("cancelled=%v expired=%d ticks=%d", d.cancelled(), expired.Load(), ticks.Load())
			}
			d.dispose()
			d.dispose()
		})
		d = newDialog()
		t.Run(d.name+" dispose stops it", func(t *testing.T) {
			seconds := make(chan func(), 4)
			var expired atomic.Int32
			d.start(time.Second, func(second func()) { seconds <- second }, func() {}, func() { expired.Add(1) })
			d.dispose()
			select {
			case second := <-seconds:
				second()
				t.Fatalf("a disposed countdown ran a second (cancelled=%v expired=%d)", d.cancelled(), expired.Load())
			case <-time.After(1500 * time.Millisecond):
			}
			if d.cancelled() || expired.Load() != 0 {
				t.Fatalf("a disposed countdown cancelled the dialog (cancelled=%v expired=%d)", d.cancelled(), expired.Load())
			}
		})
	}
}
