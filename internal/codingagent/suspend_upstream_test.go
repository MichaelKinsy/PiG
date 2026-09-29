package codingagent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestInteractiveSuspendUpstream(t *testing.T) {
	// packages/coding-agent/test/interactive-mode-suspend.test.ts:31.
	t.Run("shows a status message and skips suspend on Windows", func(t *testing.T) {
		var status []string
		if err := suspendTerminal(t.Context(), "windows", func(text string) { status = append(status, text) }, suspendOperations{}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(status, []string{"Suspend to background is not supported on Windows"}) {
			t.Fatalf("status=%q", status)
		}
	})
	for _, failed := range []bool{false, true} {
		name := "keeps the process alive while suspended and restores the TUI on SIGCONT"
		if failed {
			name = "cleans up the temporary handlers if suspension fails"
		}
		// packages/coding-agent/test/interactive-mode-suspend.test.ts:65,115. Call the handler synchronously, inspect its returned state, then invoke the registered continuation exactly as Pi's test does.
		t.Run(name, func(t *testing.T) {
			var calls []string
			var onContinue func() error
			boom := errors.New("suspend failed")
			ops := suspendOperations{
				keepAlive: func(interval time.Duration) func() {
					if interval != (1<<30)*time.Millisecond {
						t.Errorf("keep-alive interval=%v", interval)
					}
					calls = append(calls, "interval")
					return func() { calls = append(calls, "clear-interval") }
				},
				ignoreInterrupt: func() func() {
					calls = append(calls, "on-SIGINT")
					return func() { calls = append(calls, "remove-SIGINT") }
				},
				continued: func(continued func() error, _ func()) func() {
					calls = append(calls, "once-SIGCONT")
					onContinue = continued
					return func() { calls = append(calls, "remove-SIGCONT") }
				},
				stop:          func() { calls = append(calls, "stop") },
				start:         func() error { calls = append(calls, "start"); return nil },
				requestRender: func() { calls = append(calls, "render-force") },
				kill: func(pid int) error {
					calls = append(calls, fmt.Sprintf("kill:%d:SIGTSTP", pid))
					if failed {
						return boom
					}
					return nil
				},
			}
			err := suspendTerminal(t.Context(), "linux", nil, ops)
			want := []string{"interval", "on-SIGINT", "once-SIGCONT", "stop", "kill:0:SIGTSTP"}
			if onContinue == nil {
				t.Fatal("continuation was not registered")
			}
			if failed {
				if !errors.Is(err, boom) {
					t.Fatalf("lost suspension error: %v", err)
				}
			} else {
				if err != nil || !reflect.DeepEqual(calls, want) {
					t.Fatalf("returned before continuation: calls=%q error=%v", calls, err)
				}
				if err := onContinue(); err != nil {
					t.Fatal(err)
				}
			}
			want = append(want, "clear-interval", "remove-SIGINT", "remove-SIGCONT")
			if !failed {
				want = append(want, "start", "render-force")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%q want=%q", calls, want)
			}
		})
	}
}

func TestSuspendCancellationCleansUpWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stops, cleanups, starts int
	var onContinue func() error
	var onCancel func()
	ops := suspendOperations{
		keepAlive:       func(time.Duration) func() { return func() { cleanups++ } },
		ignoreInterrupt: func() func() { return func() { cleanups++ } },
		continued: func(continued func() error, canceled func()) func() {
			onContinue, onCancel = continued, canceled
			return func() { cleanups++ }
		},
		stop: func() { stops++ }, start: func() error { starts++; return nil }, requestRender: func() { t.Error("render after cancellation") }, kill: func(int) error { return nil },
	}
	if err := suspendTerminal(ctx, "linux", nil, ops); err != nil {
		t.Fatal(err)
	}
	cancel()
	onCancel()
	if err := onContinue(); err != nil {
		t.Fatal(err)
	}
	if stops != 1 || cleanups != 3 || starts != 0 {
		t.Fatalf("stop=%d cleanup=%d start=%d", stops, cleanups, starts)
	}
}
