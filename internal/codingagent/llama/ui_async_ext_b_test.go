package llama

// pi: packages/coding-agent/src/extensions/llama/ui.ts

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// scriptedProgressUI is a LlamaUi that only answers the progress screen and the stop confirmation.
type scriptedProgressUI struct {
	progress func() <-chan struct{}
	confirm  func() bool
}

func (u *scriptedProgressUI) ShowModels(string, []LlamaModelInfo) LlamaManagerAction {
	return LlamaManagerAction{Type: LlamaManagerActionClose}
}
func (u *scriptedProgressUI) Select(string, []string) (string, bool)            { return "", false }
func (u *scriptedProgressUI) Confirm(string, string) bool                       { return u.confirm() }
func (u *scriptedProgressUI) ConnectionError(string, string) string             { return "close" }
func (u *scriptedProgressUI) SearchModels(HuggingFaceSearchFunc) (string, bool) { return "", false }
func (u *scriptedProgressUI) ShowStatus(string, string)                         {}
func (u *scriptedProgressUI) Progress(ProgressState) <-chan struct{}            { return u.progress() }
func (u *scriptedProgressUI) UpdateProgress(ProgressState)                      {}

// ui.ts:549-565 runWithProgress: after a confirmed stop it awaits options.cancel() while the run is still live, then aborts the
// run's signal with "Cancelled" and awaits the settled run before it returns; a rejection of cancel() is rethrown (Go returns it
// after the same join of the run goroutine; Pi's `finally` only aborts and skips `await settled` on that path).
func TestRunWithProgressCancelsBeforeAbortingAndJoinsTheRun(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cancelErr error
	}{{"cancel succeeds", nil}, {"cancel fails", errors.New("unload request failed")}} {
		t.Run(tc.name, func(t *testing.T) {
			runCtx := make(chan context.Context, 1)
			sawAbort := make(chan struct{})
			hold := make(chan struct{})
			var finished atomic.Bool
			var cancelSawLiveRun atomic.Bool
			var ctxOfRun context.Context
			ui := &scriptedProgressUI{
				progress: func() <-chan struct{} {
					ctxOfRun = <-runCtx
					stop := make(chan struct{})
					close(stop)
					return stop
				},
				confirm: func() bool { return true },
			}
			type outcome struct {
				cancelled bool
				err       error
			}
			outcomes := make(chan outcome, 1)
			go func() {
				_, cancelled, err := RunWithProgress(ui, RunWithProgressOptions[int]{
					Run: func(ctx context.Context, _ func(LlamaProgress)) (int, error) {
						runCtx <- ctx
						<-ctx.Done()
						if context.Cause(ctx).Error() != "Cancelled" {
							t.Errorf("abort reason = %v, want Cancelled", context.Cause(ctx))
						}
						close(sawAbort)
						<-hold
						finished.Store(true)
						return 0, context.Cause(ctx)
					},
					Cancel: func() error {
						cancelSawLiveRun.Store(ctxOfRun.Err() == nil)
						return tc.cancelErr
					},
				})
				outcomes <- outcome{cancelled, err}
			}()
			<-sawAbort
			// The run has seen the abort but has not returned: the call must still be waiting for it.
			select {
			case got := <-outcomes:
				t.Fatalf("RunWithProgress returned %+v before the run settled", got)
			case <-time.After(100 * time.Millisecond):
			}
			close(hold)
			got := result(t, outcomes)
			if !finished.Load() {
				t.Error("RunWithProgress returned before the run finished")
			}
			if !cancelSawLiveRun.Load() {
				t.Error("the run was aborted before options.Cancel was called")
			}
			if tc.cancelErr == nil && (!got.cancelled || got.err != nil) {
				t.Errorf("outcome = %+v, want cancelled", got)
			}
			if tc.cancelErr != nil && (got.cancelled || !errors.Is(got.err, tc.cancelErr)) {
				t.Errorf("outcome = %+v, want the cancel error", got)
			}
		})
	}
}

// ui.ts:549-554 runWithProgress ignores a stop that is confirmed after the run has settled (`if (!stop || completed) continue`):
// options.cancel() is not called and the run's value is returned. The confirmation completes only after the run goroutine is gone.
func TestRunWithProgressIgnoresAStopConfirmedAfterTheRunSettled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		proceed := make(chan struct{})
		var cancelled atomic.Bool
		ui := &scriptedProgressUI{
			progress: func() <-chan struct{} {
				stop := make(chan struct{})
				close(stop)
				return stop
			},
			confirm: func() bool {
				close(proceed)
				synctest.Wait()
				return true
			},
		}
		value, stopped, err := RunWithProgress(ui, RunWithProgressOptions[int]{
			Run: func(context.Context, func(LlamaProgress)) (int, error) {
				<-proceed
				return 7, nil
			},
			Cancel: func() error {
				cancelled.Store(true)
				return nil
			},
		})
		if value != 7 || stopped || err != nil || cancelled.Load() {
			t.Fatalf("RunWithProgress = %d, %v, %v (cancel called %v), want the settled value and no cancel", value, stopped, err, cancelled.Load())
		}
	})
}

// ui.ts:511-523 showLlamaUi starts the flow without awaiting it inside the custom factory (`void run(view).then(done, ...)`): the
// view is mounted first, a flow error is notified before the view ends, and the view ends when the flow does.
func TestShowLlamaUiRunsTheFlowAfterMountingAndNotifiesBeforeEnding(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want []string
	}{{"flow succeeds", nil, []string{"done"}}, {"flow fails", errors.New("flow exploded"), []string{"notify flow exploded error", "done"}}} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var log []string
			record := func(entry string) {
				mu.Lock()
				defer mu.Unlock()
				log = append(log, entry)
			}
			mounted := make(chan struct{})
			ended := make(chan struct{})
			ctx := CommandContext{
				Ctx:    context.Background(),
				Notify: func(message, kind string) { record("notify " + message + " " + kind) },
				Custom: func(factory func(requestRender, done func()) CustomComponent) {
					component := factory(func() {}, func() {
						record("done")
						close(ended)
					})
					if component == nil {
						t.Error("the factory returned no component")
					}
					close(mounted)
					<-ended
				},
			}
			ShowLlamaUi(ctx, func(LlamaUi) error {
				select {
				case <-mounted:
				case <-time.After(5 * time.Second):
					t.Error("the flow ran before the view was mounted")
				}
				return tc.err
			})
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(log, tc.want) {
				t.Fatalf("calls = %v, want %v", log, tc.want)
			}
		})
	}
}
