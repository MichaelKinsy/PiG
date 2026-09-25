package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's synchronous handleTerminalInput finishes each listener pass before the next chunk, including Ctrl+C. Exercise the pump and listener pass without editor or socket costs so repeated scans of the remaining burst remain visible.
func TestTerminalInputBurstAllocationsGrowLinearly(t *testing.T) {
	for _, listener := range []string{"none", "responsive", "pending"} {
		t.Run(listener, func(t *testing.T) {
			measure := func(n int) float64 {
				return testing.AllocsPerRun(1, func() { drainTerminalInputBurst(t, n, listener) })
			}
			small, large := measure(1024), measure(4096)
			t.Logf("1024 -> 4096 bytes: %.0f -> %.0f allocations", small, large)
			// Four times the input permits five times the allocations, allowing fixed setup and slice-growth variation but rejecting quadratic suffix scans.
			if large > 5*small {
				t.Fatalf("fourfold input grew allocations %.2fx; want at most 5x", large/small)
			}
		})
	}
}

func TestTerminalInputBurstDeliversAllBytes(t *testing.T) {
	for _, listener := range []string{"none", "responsive", "pending"} {
		for _, n := range []int{0, 1, 256, 65536} {
			t.Run(fmt.Sprintf("%s/%d", listener, n), func(t *testing.T) {
				drainTerminalInputBurst(t, n, listener)
			})
		}
	}
}

func BenchmarkTerminalInputBurst(b *testing.B) {
	for _, listener := range []string{"none", "responsive", "pending"} {
		for _, n := range []int{1024, 4096} {
			b.Run(fmt.Sprintf("%s/%d", listener, n), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(n))
				for b.Loop() {
					drainTerminalInputBurst(b, n, listener)
				}
			})
		}
	}
}

// Run the real pump and listener pass. Remote verdicts resume on the owner task queue, just as in inputLoop; the pending case holds the first verdict until the owner services a separate event. Every byte must reach handling.
func drainTerminalInputBurst(tb testing.TB, n int, listener string) {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &InteractiveMode{backgroundCtx: ctx, uiTaskCh: make(chan func(), 64), tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	asked, release := make(chan struct{}), make(chan struct{})
	if listener != "none" {
		first := true
		m.addRemoteTerminalInputHandler("burst", func(context.Context, string) extension.TerminalInputResult {
			if listener == "pending" && first {
				first = false
				close(asked)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return extension.TerminalInputResult{}
		})
	}
	readCh := make(chan inputChunk)
	errCh := make(chan error, 1)
	go m.pumpTerminalInput(ctx, strings.NewReader(strings.Repeat("x", n)), readCh, errCh)
	count := 0
	for readCh != nil {
		select {
		case chunk, ok := <-readCh:
			if !ok {
				readCh = nil
				continue
			}
			_ = m.passTerminalInput(ctx, string(chunk.data), chunk.ticket, func(_ context.Context, data string) error {
				count += len(data)
				return nil
			})
			chunk.ticket.settle()
		case fn := <-m.uiTaskCh:
			fn()
		case <-asked:
			close(release)
			asked = nil
		}
	}
	m.backgroundTasks.Wait()
	if err := <-errCh; !errors.Is(err, io.EOF) {
		tb.Fatalf("pump error = %v, want EOF", err)
	}
	if count != n {
		tb.Fatalf("handled %d bytes, want %d", count, n)
	}
}

// Upstream tui.ts handleTerminalInput delivers Ctrl+C to listeners normally, after earlier input. A listener may pass, consume, or rewrite it. Only a passed Ctrl+C press reaches InteractiveMode.handleCtrlC and clears the editor.
func TestCtrlCWaitsForTerminalInputVerdictsInOrder(t *testing.T) {
	for _, controlKey := range []struct{ data, passed string }{
		{"\x03", "s"},
		{"\x1b[99;5u", "s"},
		{"\x1b[27;5;99~", "s"},
		{"\x1b[99;5:3u", "Qrs"},
	} {
		key := controlKey.data
		for _, verdict := range []string{"pass", "consume", "rewrite"} {
			t.Run(fmt.Sprintf("%q/%s", key, verdict), func(t *testing.T) {
				q := newInputQueueMode(t)
				ext := attachRemoteInputExtension(t, q.InteractiveMode, "slow")
				seen := watchTerminalInput(q.InteractiveMode)
				q.start(t)
				q.typeKeys(t, "q")
				pending := ext.next(t, "q")
				q.typeKeys(t, "r"+key+"s")
				if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != "" {
					t.Fatalf("input overtook the pending verdict: editor = %q", got)
				}
				ext.answer(t, pending, map[string]any{"data": "Q"})
				expectSeen(t, seen, "Q")
				ext.answer(t, ext.next(t, "r"), map[string]any{})
				expectSeen(t, seen, "r")
				if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != "Qr" {
					t.Fatalf("before Ctrl+C editor = %q, want Qr", got)
				}
				control := ext.next(t, key)
				want := controlKey.passed
				switch verdict {
				case "pass":
					ext.answer(t, control, map[string]any{})
					expectSeen(t, seen, key)
				case "consume":
					ext.answer(t, control, map[string]any{"consume": true})
					want = "Qrs"
				case "rewrite":
					ext.answer(t, control, map[string]any{"data": "C"})
					expectSeen(t, seen, "C")
					want = "QrCs"
				}
				ext.answer(t, ext.next(t, "s"), map[string]any{})
				expectSeen(t, seen, "s")
				if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != want {
					t.Fatalf("editor = %q, want %q", got, want)
				}
				if chat := runOnQueueLoop(t, q.InteractiveMode, func() string {
					q.showPendingExtensionErrors()
					return chatPlainText(q.InteractiveMode)
				}); chat != "" {
					t.Fatalf("unexpected terminal-input error: %s", chat)
				}
			})
		}
	}
}

// A pending listener must exert backpressure instead of accumulating every subsequent terminal read. The reader can hold one read ahead of the pump's current buffer. It must not request a third until the verdict settles.
func TestPendingTerminalInputBackpressuresReader(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprintf("shutdown=%t", shutdown), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				source := &countedInputReader{data: "xyz"}
				m := &InteractiveMode{}
				readCh, errCh := make(chan inputChunk), make(chan error, 1)
				go m.pumpTerminalInput(ctx, source, readCh, errCh)
				chunk := <-readCh
				synctest.Wait()
				// One routed read plus one held by the reader worker.
				if source.n != 2 {
					t.Errorf("reader reached read %d before the first input settled; want 2", source.n)
				}
				if shutdown {
					cancel()
				} else {
					chunk.ticket.settle()
				}
				count := len(chunk.data)
				for chunk := range readCh {
					count += len(chunk.data)
					chunk.ticket.settle()
				}
				if !shutdown {
					if err := <-errCh; !errors.Is(err, io.EOF) {
						t.Fatal(err)
					}
					if count != len(source.data) {
						t.Fatalf("delivered %d bytes, want %d", count, len(source.data))
					}
				}
				// synctest also rejects a pump or reader worker leaked on shutdown.
			})
		})
	}
}

type countedInputReader struct {
	data string
	n    int
}

func (r *countedInputReader) Read(p []byte) (int, error) {
	r.n++
	if r.n > len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.n-1]
	return 1, nil
}
