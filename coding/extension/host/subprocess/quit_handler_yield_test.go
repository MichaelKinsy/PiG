package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/handleryield"
)

// A print or JSON mode signal's disposal holds the run while session_shutdown handlers run synchronously, as Pi's one
// event loop does (print-mode.ts:50-63), and lets it go on when a handler yields. The Node runtime reports that yield
// when the handler's window closes unanswered: a handler that throws or settles through microtasks answers within its
// window and reports nothing; one that waits on a timer reports it, before its answer.
func TestPrintModeQuitHandlerReportsItsYield(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	for _, tc := range []struct {
		name, handler string
		yields        bool
	}{
		{"throws at once", `() => { throw new Error("shutdown boom"); }`, false},
		{"settles through microtasks", `async () => { await Promise.resolve(); await null; }`, false},
		{"waits on a timer", `() => new Promise((resolve) => setTimeout(resolve, 50))`, true},
	} {
		for _, mode := range []string{"print", "json"} {
			for _, isolation := range []string{"strict", "shared-ok"} {
				t.Run(tc.name+"/"+mode+"/"+isolation, func(t *testing.T) {
					t.Parallel()
					source := filepath.Join(t.TempDir(), "quit.mjs")
					if err := os.WriteFile(source, []byte(`export default function (pi) { pi.on("session_shutdown", `+tc.handler+`); }`), 0o600); err != nil {
						t.Fatal(err)
					}
					h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					h.SetMode(mode)
					t.Cleanup(func() { h.Shutdown("test complete") })
					loaded, failures := h.LoadAll(t.Context(), []ExtConfig{{Name: "quit", Source: source, Enabled: true, Isolation: isolation}})
					if len(failures) != 0 || len(loaded) != 1 {
						t.Fatalf("load: %v %v", loaded, failures)
					}
					runner := inproc.NewRunner(loaded, t.TempDir())
					var yields atomic.Int32
					ctx := handleryield.With(context.Background(), func() { yields.Add(1) })
					_, _ = runner.Emit(ctx, extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "quit"})
					if got := yields.Load() != 0; got != tc.yields {
						t.Fatalf("yield reported = %v, want %v", got, tc.yields)
					}
				})
			}
		}
	}
}
