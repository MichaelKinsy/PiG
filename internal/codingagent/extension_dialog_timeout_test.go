package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/extension-selector.ts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.87.1 interactive-mode.ts:2593-2632 and 2669-2705 pass opts.timeout to
// ExtensionSelectorComponent and ExtensionInputComponent. Their CountdownTimer
// (countdown-timer.ts) titles the dialog `${title} (${s}s)` from
// ceil(timeout/1000), counts down once a second on the event loop and cancels
// at zero in the same interval callback, so "(0s)" is never painted.
// showExtensionConfirm puts the countdown after the message, and typed input
// does not survive the expiry.
func TestExtensionDialogTimeoutCountsDownAndCancels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		call  func(context.Context, *ExtUIContext) (string, error)
		keys  []string
		ticks []string
		after time.Duration
	}{
		{"select", func(ctx context.Context, ui *ExtUIContext) (string, error) {
			return ui.Select(ctx, "Pick", []string{"first"}, extension.ExtensionUIDialogOptions{Timeout: new(float64(1500))})
		}, nil, []string{"Pick (2s)", "Pick (1s)"}, 2 * time.Second},
		{"confirm", func(ctx context.Context, ui *ExtUIContext) (string, error) {
			confirmed, err := ui.Confirm(ctx, "Timed", "Sure?", extension.ExtensionUIDialogOptions{Timeout: new(float64(1000))})
			if confirmed {
				return "confirmed", err
			}
			return "", err
		}, nil, []string{"Timed", "Sure? (1s)"}, time.Second},
		{"input", func(ctx context.Context, ui *ExtUIContext) (string, error) {
			return ui.Input(ctx, "Name", "", extension.ExtensionUIDialogOptions{Timeout: new(float64(1000))})
		}, []string{"abc"}, []string{"Name (1s)"}, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, output := newExtensionDialogProbe(t)
			ui := &ExtUIContext{m: m}
			start := time.Now()
			value, err := runTimedExtensionDialog(t, m, func() (string, error) { return tc.call(t.Context(), ui) }, tc.keys)
			elapsed := time.Since(start)
			if value != "" || !errors.Is(err, context.Canceled) {
				t.Fatalf("expired dialog = %q, %v; want cancellation", value, err)
			}
			if elapsed < tc.after-100*time.Millisecond {
				t.Fatalf("dialog expired after %v; want the %v countdown", elapsed, tc.after)
			}
			if m.extensionDialog != nil {
				t.Fatal("expired dialog is still installed")
			}
			plain := widthx.StripAnsi(output.String())
			at := 0
			for _, tick := range tc.ticks {
				next := strings.Index(plain[at:], tick)
				if next < 0 {
					t.Fatalf("countdown frame %q missing after offset %d in %q", tick, at, plain)
				}
				at += next + len(tick)
			}
			if strings.Contains(plain, "(0s)") {
				t.Fatalf("expiry painted a zero countdown: %q", plain)
			}
		})
	}
}

// An answered dialog disposes its countdown (extension-selector.ts:114-116):
// no later second reaches the owner loop and the result is the user's choice.
func TestExtensionDialogAnsweredBeforeTimeoutStopsCountdown(t *testing.T) {
	m, output := newExtensionDialogProbe(t)
	ui := &ExtUIContext{m: m}
	value, err := runTimedExtensionDialog(t, m, func() (string, error) {
		return ui.Select(t.Context(), "Pick", []string{"first"}, extension.ExtensionUIDialogOptions{Timeout: new(float64(1000))})
	}, []string{"\r"})
	if value != "first" || err != nil {
		t.Fatalf("answered dialog = %q, %v; want first", value, err)
	}
	quiet := time.After(1300 * time.Millisecond)
	for {
		select {
		case <-m.uiTaskCh:
			t.Fatal("a disposed countdown posted work to the owner loop")
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-quiet:
			if plain := widthx.StripAnsi(output.String()); !strings.Contains(plain, "Pick (1s)") || strings.Contains(plain, "(0s)") {
				t.Fatalf("countdown frames = %q; want only Pick (1s)", plain)
			}
			return
		}
	}
}

// runTimedExtensionDialog runs the owner loop's tasks and scheduled renders, as
// the interactive loop does, until call returns. keys follow the installation.
func runTimedExtensionDialog(t *testing.T, m *InteractiveMode, call func() (string, error), keys []string) (string, error) {
	t.Helper()
	type callResult struct {
		value string
		err   error
	}
	done := make(chan callResult, 1)
	go func() {
		value, err := call()
		done <- callResult{value, err}
	}()
	deadline := time.After(5 * time.Second)
	installed := false
	for {
		select {
		case task := <-m.uiTaskCh:
			task()
			if !installed && m.extensionDialog != nil {
				installed = true
				for _, key := range keys {
					if err := m.dispatchKey(context.Background(), key); err != nil {
						t.Fatal(err)
					}
				}
			}
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case result := <-done:
			return result.value, result.err
		case <-deadline:
			t.Fatal("timed dialog did not return")
		}
	}
}
