package codingagent

import (
	"context"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// upstream 0.99.1 tui.ts consumes a color reply before focused-editor work can delay it.
func TestTerminalColorReplyBypassesPendingAutocomplete(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	mode := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 80, 24), autocompletePending: make(chan struct{})}
	defer func() { cancel(); mode.backgroundTasks.Wait() }()
	query := mode.tuiInst.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
	for _, reply := range []string{"\x1b]11;#ffffff\x07", "\x1b[?62c"} {
		if err := mode.dispatchKey(ctx, reply); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case result := <-query:
		if result.Colors.Background == nil || result.Colors.Background.R != 255 || result.Err != nil {
			t.Fatalf("reply=%+v, want white", result)
		}
	default:
		t.Fatal("color reply waited for editor autocomplete")
	}
}
