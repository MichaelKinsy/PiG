package codingagent

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// Pi reads stdin only while its event loop is idle. A key that arrives while an awaited step of /reload is pending is read by the focused reload box,
// which has no handleInput, and dropped (packages/tui/src/tui.ts:1153-1158; the box: interactive-mode.ts:6630-6647). What follows the last awaited step is
// one synchronous stretch (interactive-mode.ts:6671-6698: no await, from the settled session.reload() to the box's dismissal), so a key that arrives once
// that step has completed is read after the stretch, and the focused editor receives it (:6698).
//
// reloadTailRound runs a /reload of steps awaited steps on an owner loop. A key is routed while every step is pending (dropped, as in Pi). The owner is held
// inside a task while a step completes, so that, when it returns to the wait, the step's completion and the keys routed after it are both ready: the test
// knows the step is complete before it routes them, and an owner that takes a key first drops it. Between two steps a key is dropped too (Pi's awaits
// between the steps read it), and after the last one the keys must reach the editor, in order, once.
func reloadTailRound(t *testing.T, steps int, between, after string) string {
	t.Helper()
	var text string
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newCustomEditorDispatchMode(t)
		t.Cleanup(m.abortFn)
		m.runCtx = ctx
		m.isIdle = true
		m.endStartupSubmitWindow()
		m.inputReadCh = make(chan inputChunk)
		m.inputErrCh = make(chan error, 1)
		loop := make(chan error, 1)
		go func() { loop <- m.inputLoop(ctx, strings.NewReader("")) }()

		// The terminal reader's routing: one sequence at a time, each held until the owner loop settles a chunk it took.
		var readers sync.WaitGroup
		route := func(keys string) {
			readers.Go(func() {
				for _, key := range []byte(keys) {
					if ticket := m.routeInputSequence(ctx, []byte{key}, false, nil, m.inputReadCh); ticket != nil {
						<-ticket.done
					}
				}
			})
		}
		release := make([]chan struct{}, steps)
		for i := range release {
			release[i] = make(chan struct{})
		}
		reloaded := make(chan error, 1)
		if err := m.postToMain(ctx, func() {
			for i := range steps {
				if err := m.awaitReloadStep(ctx, func(context.Context) error { <-release[i]; return nil }); err != nil {
					reloaded <- err
					return
				}
			}
			reloaded <- nil
		}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		for i := range steps {
			// A key typed while the step is pending reaches the reload box.
			route("x")
			synctest.Wait()
			parked, resume := make(chan struct{}), make(chan struct{})
			if err := m.postToMain(ctx, func() { close(parked); <-resume }); err != nil {
				t.Fatal(err)
			}
			<-parked
			close(release[i])
			keys := between
			if i == steps-1 {
				keys = after
			}
			route(keys)
			// The step's work has returned and the keys wait on the route: the owner takes the step's completion or a key.
			synctest.Wait()
			close(resume)
			synctest.Wait()
		}
		if err := <-reloaded; err != nil {
			t.Fatalf("reload: %v", err)
		}
		readers.Wait()
		text = m.editor.Text()
		m.inputErrCh <- io.EOF
		if err := <-loop; !errors.Is(err, io.EOF) {
			t.Fatalf("input loop: %v", err)
		}
	})
	return text
}

func TestReloadKeysAfterTheLastStepReachTheEditor(t *testing.T) {
	// The owner's wait picks among the ready cases at random, so each round is a fresh draw: a loop that takes a key before it takes the completion loses
	// it in half of the rounds, and 40 rounds make a pass by chance 2^-40.
	for round := range 40 {
		if got, want := reloadTailRound(t, 1, "", "hello"), "hello"; got != want {
			t.Fatalf("round %d: one step: the editor holds %q after the reload, want the keys that arrived after the step completed: %q", round, got, want)
		}
		if got, want := reloadTailRound(t, 2, "BETWEEN", "hello"), "hello"; got != want {
			t.Fatalf("round %d: two steps: the editor holds %q after the reload, want only the keys that arrived after the last step completed: %q", round, got, want)
		}
	}
}
