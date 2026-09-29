package codingagent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestAutomaticThemeQueryFailureKeepsConcurrentFallback(t *testing.T) {
	// theme.ts:714-729 always starts background detection and awaits scheme precedence even when one query fails.
	t.Run("scheme failure still starts and awaits background", func(t *testing.T) {
		var output bytes.Buffer
		detection := newStartupThemeDetection(new("light/dark"), map[string]string{"COLORFGBG": "0;15"}, tui.NewWithOutput(&output, 80, 24))
		detection.start(func(string) error { return errors.New("scheme write failed") })
		if output.String() != terminalBackgroundQuery {
			t.Fatalf("scheme failure suppressed background query: %q", output.String())
		}
		if consumed, settled := detection.consume("\x1b]11;#000000\x07"); !consumed || !settled || detection.themeName() != "dark" {
			t.Fatalf("background fallback consumed=%v settled=%v theme=%s", consumed, settled, detection.themeName())
		}
	})
	t.Run("background failure does not outrun scheme", func(t *testing.T) {
		detection := newStartupThemeDetection(new("light/dark"), map[string]string{"COLORFGBG": "15;0"}, tui.NewWithOutput(themeErrorWriter{errors.New("background write failed")}, 80, 24))
		detection.start(func(string) error { return nil })
		if detection.readBackground() || detection.settled {
			t.Fatal("failed background query outran the pending scheme query")
		}
		if consumed, settled := detection.consume("\x1b[?997;2n"); !consumed || !settled || detection.themeName() != "light" {
			t.Fatalf("scheme precedence consumed=%v settled=%v theme=%s", consumed, settled, detection.themeName())
		}
	})
}

func TestInteractiveThemeSchemeFailureStillUsesBackgroundReply(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "0;15")
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.Settings.Theme = "light/dark"
	var background bytes.Buffer
	setThemeQueryTestOutput(mode, &background)
	query := mode.beginThemeDetection(themeErrorWriter{errors.New("scheme write failed")})
	if background.String() != terminalBackgroundQuery {
		t.Fatal("mode did not start its concurrent background query")
	}
	mode.consumeTerminalThemeInput("\x1b]11;#000000\x07")
	<-query.done
	if mode.themeState.terminalTheme != "dark" {
		t.Fatal("mode used environment before the concurrent background reply")
	}
}

func TestTerminalBackgroundReplyBypassesPendingAutocomplete(t *testing.T) {
	// Pi tui.ts:1006 consumes a query reply before focused-editor work can delay it.
	ctx, cancel := context.WithCancel(t.Context())
	mode := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 80, 24), autocompletePending: make(chan struct{})}
	defer func() { cancel(); mode.backgroundTasks.Wait() }()
	query := mode.tuiInst.QueryTerminalBackgroundColor(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
	if err := mode.dispatchKey(ctx, "\x1b]11;#ffffff\x07"); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-query:
		if result.Color == nil || result.Color.R != 255 || result.Err != nil {
			t.Fatalf("reply=%+v, want white", result)
		}
	default:
		t.Fatal("background reply waited for editor autocomplete")
	}
}

func TestInteractiveThemeSharesRendererQueryFIFO(t *testing.T) {
	// Every OSC 11 request uses the renderer queue, including automatic theme detection and a concurrent public query.
	synctest.Test(t, func(t *testing.T) {
		restoreStartupTheme(t)
		mode, _ := newCustomEditorDispatchMode(t)
		public := mode.tuiInst.QueryTerminalBackgroundColor(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
		detection := mode.beginThemeDetection(io.Discard)
		if !mode.consumeTerminalThemeInput("\x1b]11;#ffffff\x07") {
			t.Fatal("public reply not consumed")
		}
		if result := <-public; result.Color == nil || result.Color.R != 255 {
			t.Fatalf("public reply=%+v", result)
		}
		if detection.detection.backgroundAnswered {
			t.Fatal("first response settled the later theme query")
		}
		select {
		case <-detection.done:
			t.Fatal("theme completed before its reply")
		default:
		}
		mode.consumeTerminalThemeInput("\x1b]11;#000000\x07")
		<-detection.done
		if mode.themeState.terminalTheme != "dark" || len(mode.themeState.queries) != 0 {
			t.Fatal("theme did not settle its own reply or retained its completed detection")
		}
	})
}
