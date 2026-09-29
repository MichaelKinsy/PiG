package tui

import (
	"errors"
	"io"
	"math"
	"testing"
	"testing/synctest"
	"time"
)

func TestTerminalBackgroundQueryFIFOAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := NewWithOutput(io.Discard, 80, 24)
		first := ui.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 1})
		second := ui.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 1000})
		time.Sleep(5 * time.Millisecond)
		if got := <-first; got.Color != nil || got.Err != nil {
			t.Fatalf("timeout=%+v", got)
		}
		if !ui.ConsumeOsc11BackgroundResponse("\x1b]11;#000000\x07") {
			t.Fatal("late reply not consumed")
		}
		select {
		case got := <-second:
			t.Fatalf("late first reply settled second: %+v", got)
		default:
		}
		if !ui.ConsumeOsc11BackgroundResponse("\x1b]11;#ffffff\x07") {
			t.Fatal("second reply not consumed")
		}
		if got := <-second; got.Err != nil || got.Color == nil || *got.Color != (RgbColor{255, 255, 255}) {
			t.Fatalf("second=%+v", got)
		}
		if ui.ConsumeOsc11BackgroundResponse("\x1b]11;#000000\x07") {
			t.Fatal("unsolicited reply consumed")
		}
		if len(ui.terminalBackground.pendingOsc11BackgroundQueries) != 0 {
			t.Fatal("matched slots retained")
		}
	})
}

func TestTerminalBackgroundQueryDeadlineSurvivesStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := NewWithOutput(io.Discard, 80, 24)
		query := ui.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 10})
		ui.Stop()
		time.Sleep(9 * time.Millisecond)
		select {
		case result := <-query:
			t.Fatalf("stop settled query early: %+v", result)
		default:
		}
		time.Sleep(time.Millisecond)
		if got := <-query; got.Color != nil || got.Err != nil {
			t.Fatalf("stopped timeout=%+v", got)
		}
		pending := ui.terminalBackground.pendingOsc11BackgroundQueries
		if len(pending) != 1 || !pending[0].settled || pending[0].timer != nil || pending[0].result != nil {
			t.Fatal("timeout must retain only the reply slot, not its timer or resolver")
		}
	})
}

type colorQueryWriter func([]byte) (int, error)

func (w colorQueryWriter) Write(data []byte) (int, error) { return w(data) }

func TestTerminalBackgroundQuerySynchronousReplyAndWriteFailure(t *testing.T) {
	var ui *TUI
	ui = NewWithOutput(colorQueryWriter(func(data []byte) (int, error) {
		if !ui.ConsumeOsc11BackgroundResponse("\x1b]11;#ffffff\x07") {
			t.Error("query slot not registered before write")
		}
		return len(data), nil
	}), 80, 24)
	result := <-ui.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 1000})
	if result.Color == nil || result.Color.R != 255 || result.Err != nil {
		t.Fatalf("synchronous=%+v", result)
	}
	writeErr := errors.New("terminal disconnected")
	ui = NewWithOutput(colorQueryWriter(func([]byte) (int, error) { return 0, writeErr }), 80, 24)
	result = <-ui.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 1000})
	if !errors.Is(result.Err, writeErr) || result.Color != nil {
		t.Fatalf("write failure=%+v", result)
	}
	if !ui.ConsumeOsc11BackgroundResponse("\x1b]11;#ffffff\x07") {
		t.Fatal("failed write discarded pending reply slot")
	}
}

func TestTerminalBackgroundQueryUsesNodeTimerDelay(t *testing.T) {
	for _, milliseconds := range []float64{0, -1, math.NaN(), math.Inf(1), float64(math.MaxInt32) + 1, 1.9} {
		synctest.Test(t, func(t *testing.T) {
			ui := NewWithOutput(io.Discard, 80, 24)
			query := ui.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: milliseconds})
			synctest.Wait()
			select {
			case got := <-query:
				t.Fatalf("%v settled before 1ms: %+v", milliseconds, got)
			default:
			}
			time.Sleep(time.Millisecond)
			if got := <-query; got.Color != nil || got.Err != nil {
				t.Fatalf("%v timeout=%+v", milliseconds, got)
			}
		})
	}
}

func TestTerminalBackgroundQueriesAreRendererLocal(t *testing.T) {
	mainScreen := NewWithOutput(io.Discard, 80, 24)
	altScreen := NewTuiAltScreenWithOutput(io.Discard, 80, 24, TuiAltScreenOptions{})
	mainQuery := mainScreen.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 1000})
	altQuery := altScreen.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 1000})
	if !mainScreen.ConsumeOsc11BackgroundResponse("\x1b]11;#ffffff\x07") {
		t.Fatal("main reply not consumed")
	}
	if got := <-mainQuery; got.Color == nil || got.Color.R != 255 {
		t.Fatal(got)
	}
	select {
	case result := <-altQuery:
		t.Fatalf("main reply settled alt: %+v", result)
	default:
	}
	if !altScreen.ConsumeOsc11BackgroundResponse("\x1b]11;#000000\x07") {
		t.Fatal("alt reply not consumed")
	}
	if got := <-altQuery; got.Color == nil || *got.Color != (RgbColor{}) {
		t.Fatal(got)
	}
}

func BenchmarkTerminalBackgroundQueryReply(b *testing.B) {
	ui := NewWithOutput(io.Discard, 80, 24)
	b.ReportAllocs()
	for b.Loop() {
		query := ui.QueryTerminalBackgroundColor(TerminalColorQueryOptions{TimeoutMs: 1000})
		ui.ConsumeOsc11BackgroundResponse("\x1b]11;rgb:0000/8000/ffff\x07")
		if result := <-query; result.Color == nil || result.Color.G != 128 {
			b.Fatal(result)
		}
	}
}
