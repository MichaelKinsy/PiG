package codingagent

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// editPreviewGate replaces computeEditsDiff with a stub that counts calls per path, reports each start on started and blocks until that path's gate opens. After
// holdStarts a compute also waits, before it reports its start, for that path's enter gate, so a test fixes the order in which two computes start.
type editPreviewGate struct {
	started chan string
	enter   map[string]chan struct{}
	gates   map[string]chan struct{}
	mu      sync.Mutex
	calls   map[string]int
}

func gateEditPreviews(t *testing.T, paths ...string) *editPreviewGate {
	t.Helper()
	g := &editPreviewGate{started: make(chan string, 16), enter: map[string]chan struct{}{}, gates: map[string]chan struct{}{}, calls: map[string]int{}}
	for _, path := range paths {
		g.gates[path] = make(chan struct{})
	}
	previous := computeEditsDiff
	computeEditsDiff = func(path string, _ []tools.EditReplacement, _ string) tools.EditDiffOutcome {
		g.mu.Lock()
		g.calls[path]++
		enter := g.enter[path]
		g.mu.Unlock()
		if enter != nil {
			<-enter
		}
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

// holdStarts shuts an enter gate for every path. A test calls it before the first compute starts.
func (g *editPreviewGate) holdStarts() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for path := range g.gates {
		g.enter[path] = make(chan struct{})
	}
}

// release opens every gate that is still shut, so no compute stays held.
func (g *editPreviewGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, gates := range []map[string]chan struct{}{g.enter, g.gates} {
		for _, gate := range gates {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	}
}

func editArgsOf(path string) json.RawMessage {
	return json.RawMessage(`{"path":"` + path + `","edits":[{"oldText":"x","newText":"y"}]}`)
}

// renderers/edit.ts renderCall (:189) starts `void computeEditsDiff(...).then(preview => ...)` once per call, drops a preview whose args key changed while it was
// computed, and only a preview of the current args is stored and followed by one invalidate. Two previews in flight have no order: upstream's are promises that
// settle in either order, PiG's are goroutines that the scheduler runs in either order. Each case fixes the order in which the two computes start and the order in
// which they land, and reads the card's state after each step, once synctest.Wait reports every compute goroutine held at a gate or ended.
func TestEditRenderCallPreviewIsStartedOnceAndStalePreviewIsDiscarded(t *testing.T) {
	const stale, current = "a.txt", "b.txt"
	for _, tc := range []struct {
		name          string
		starts, lands [2]string
	}{
		{"the stale compute starts first and lands first", [2]string{stale, current}, [2]string{stale, current}},
		{"the stale compute starts first and lands last", [2]string{stale, current}, [2]string{current, stale}},
		{"the current compute starts first and lands last", [2]string{current, stale}, [2]string{stale, current}},
		{"the current compute starts first and lands first", [2]string{current, stale}, [2]string{current, stale}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			synctest.Test(t, func(t *testing.T) {
				g := gateEditPreviews(t, stale, current)
				g.holdStarts()
				var invalidated atomic.Int32
				card := map[string]any{}
				render := func(path string) {
					args := editArgsOf(path)
					editRenderCall(args, nil, extension.ToolRenderContext{
						Args: args, State: card, Cwd: cwd, ArgsComplete: true,
						Invalidate: func() { invalidated.Add(1) },
					})
				}
				state := builtInState(extension.ToolRenderContext{State: card})
				defer func() { // a case that stops early still joins its computes before the stub is restored
					g.release()
					state.tasks.Wait()
				}()

				render(stale)
				render(stale)   // the preview of these args is pending: no second compute starts
				render(current) // the args changed: a compute for the new args starts
				synctest.Wait() // every compute these calls started is held at its enter gate
				if a, b := g.callsFor(stale), g.callsFor(current); a != 1 || b != 1 {
					t.Fatalf("computes: a.txt %d, b.txt %d; want one each (a pending preview starts no second compute)", a, b)
				}
				for _, path := range tc.starts {
					close(g.enter[path])
					if got := <-g.started; got != path {
						t.Fatalf("the compute of %q started, want the one whose enter gate opened: %q", got, path)
					}
				}
				var want tools.EditDiffOutcome
				wantInvalidated := int32(0)
				for _, path := range tc.lands {
					close(g.gates[path])
					synctest.Wait() // that compute's goroutine stored or dropped its preview and ended
					if path == current {
						want, wantInvalidated = tools.EditDiffResult{Diff: "diff of " + current}, 1
					}
					state.mu.Lock()
					preview := state.editPreview
					state.mu.Unlock()
					if preview != want {
						t.Fatalf("after the compute of %s landed, preview = %+v, want %+v (only a preview of the current args is stored)", path, preview, want)
					}
					if n := invalidated.Load(); n != wantInvalidated {
						t.Fatalf("after the compute of %s landed, invalidate ran %d times, want %d (once, for the current preview only)", path, n, wantInvalidated)
					}
				}
			})
		})
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
