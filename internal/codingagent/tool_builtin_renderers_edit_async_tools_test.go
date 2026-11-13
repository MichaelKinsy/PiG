package codingagent

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// editPreviewGate replaces computeEditsDiff with a stub that reports each start on started, counts calls per path and blocks until that path's gate opens.
type editPreviewGate struct {
	started chan string
	gates   map[string]chan struct{}
	mu      sync.Mutex
	calls   map[string]int
}

func gateEditPreviews(t *testing.T, paths ...string) *editPreviewGate {
	t.Helper()
	g := &editPreviewGate{started: make(chan string, 16), gates: map[string]chan struct{}{}, calls: map[string]int{}}
	for _, path := range paths {
		g.gates[path] = make(chan struct{})
	}
	previous := computeEditsDiff
	computeEditsDiff = func(path string, _ []tools.EditReplacement, _ string) tools.EditDiffOutcome {
		g.mu.Lock()
		g.calls[path]++
		g.mu.Unlock()
		g.started <- path
		<-g.gates[path]
		return tools.EditDiffResult{Diff: "diff of " + path}
	}
	t.Cleanup(func() { computeEditsDiff = previous })
	return g
}

func (g *editPreviewGate) callsFor(path string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[path]
}

func editArgsOf(path string) json.RawMessage {
	return json.RawMessage(`{"path":"` + path + `","edits":[{"oldText":"x","newText":"y"}]}`)
}

// renderers/edit.ts renderCall (:189) starts `void computeEditsDiff(...).then(preview => ...)` once per call, drops a preview whose args key changed while it was
// computed, and only a preview of the current args is stored and followed by one invalidate. Each case here releases the compute gates only after every renderCall,
// joins the compute goroutines and then reads the card's state, so the outcome does not depend on goroutine scheduling.
func TestEditRenderCallPreviewIsStartedOnceAndStalePreviewIsDiscarded(t *testing.T) {
	g := gateEditPreviews(t, "a.txt", "b.txt")
	var invalidated atomic.Int32
	card := map[string]any{}
	render := func(path string) {
		args := editArgsOf(path)
		editRenderCall(args, nil, extension.ToolRenderContext{
			Args: args, State: card, Cwd: t.TempDir(), ArgsComplete: true,
			Invalidate: func() { invalidated.Add(1) },
		})
	}
	state := builtInState(extension.ToolRenderContext{State: card})

	render("a.txt")
	render("a.txt") // the preview of these args is pending: no second compute starts
	render("b.txt") // the args changed: a compute for the new args starts
	if got := <-g.started; got != "a.txt" {
		t.Fatalf("first compute is for %q, want a.txt", got)
	}
	close(g.gates["a.txt"]) // the preview of the args that are no longer current lands
	close(g.gates["b.txt"])
	state.tasks.Wait()

	if a, b := g.callsFor("a.txt"), g.callsFor("b.txt"); a != 1 || b != 1 {
		t.Fatalf("computes: a.txt %d, b.txt %d; want one each (a pending preview starts no second compute)", a, b)
	}
	state.mu.Lock()
	preview := state.editPreview
	state.mu.Unlock()
	if want := (tools.EditDiffResult{Diff: "diff of b.txt"}); preview != want {
		t.Fatalf("preview = %+v, want the one computed for the current args %+v", preview, want)
	}
	if n := invalidated.Load(); n != 1 {
		t.Fatalf("invalidate ran %d times, want once for the current preview only (a stale preview does not invalidate)", n)
	}
}

// A card disposed (state.closed) or whose lifetime context ended drops the preview that lands afterwards: nothing is stored and nothing is invalidated, and a
// card disposed before renderCall starts no compute.
func TestEditRenderCallDiscardsAPreviewThatLandsAfterDisposal(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(state *builtInRenderState, cancel context.CancelFunc)
	}{
		{"closed", func(state *builtInRenderState, _ context.CancelFunc) {
			state.mu.Lock()
			state.closed = true
			state.mu.Unlock()
		}},
		{"lifetime cancelled", func(_ *builtInRenderState, cancel context.CancelFunc) { cancel() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := gateEditPreviews(t, "a.txt")
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			state := &builtInRenderState{lifetime: lifetime}
			card := map[string]any{builtInRenderStateKey: state}
			var invalidated atomic.Int32
			args := editArgsOf("a.txt")
			editRenderCall(args, nil, extension.ToolRenderContext{
				Args: args, State: card, Cwd: t.TempDir(), ArgsComplete: true,
				Invalidate: func() { invalidated.Add(1) },
			})
			<-g.started // the compute is running
			tc.end(state, cancel)
			close(g.gates["a.txt"])
			state.tasks.Wait()
			state.mu.Lock()
			preview := state.editPreview
			state.mu.Unlock()
			if preview != nil || invalidated.Load() != 0 {
				t.Fatalf("a preview that lands after disposal was stored (%+v) or invalidated (%d)", preview, invalidated.Load())
			}
		})
	}
	t.Run("disposed before renderCall", func(t *testing.T) {
		g := gateEditPreviews(t, "a.txt")
		close(g.gates["a.txt"]) // a compute that did start would not block
		state := &builtInRenderState{closed: true}
		card := map[string]any{builtInRenderStateKey: state}
		args := editArgsOf("a.txt")
		editRenderCall(args, nil, extension.ToolRenderContext{Args: args, State: card, Cwd: t.TempDir(), ArgsComplete: true})
		state.tasks.Wait()
		if n := g.callsFor("a.txt"); n != 0 {
			t.Fatalf("a disposed card started %d computes", n)
		}
	})
}
