package coding

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// ownerLoopUI is a UI with an owner loop, as the interactive TUI's ExtUIContext is: OnOwnerLoop says whether the caller runs that loop.
type ownerLoopUI struct {
	extension.UIContext
	onLoop atomic.Bool
}

func (u *ownerLoopUI) OnOwnerLoop() bool { return u.onLoop.Load() }

// unawaitedEventCase is one Session call that Pi follows with `void this._extensionRunner.emit(...)`.
type unawaitedEventCase struct {
	event string
	// start builds a Session with ext loaded and returns it with the call that emits event.
	start func(t *testing.T, ext extension.Extension) (*Session, func() error)
}

func unawaitedEventCases() []unawaitedEventCase {
	return []unawaitedEventCase{
		// upstream: packages/coding-agent/src/core/agent-session.ts:setSessionName
		{event: "session_info_changed", start: func(t *testing.T, ext extension.Extension) (*Session, func() error) {
			s := newRecoveryHarness(t, harnessOptions{extension: ext}).session
			return s, func() error { return s.SetSessionName("first") }
		}},
		// upstream: packages/coding-agent/src/core/agent-session.ts:setThinkingLevel
		{event: "thinking_level_select", start: func(t *testing.T, ext extension.Extension) (*Session, func() error) {
			s := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0)).session
			model := *s.Model()
			model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
			if err := s.SetModel(&model); err != nil {
				t.Fatal(err)
			}
			level := ai.ThinkingHigh
			if s.ThinkingLevel() == level {
				level = ai.ThinkingLow
			}
			return s, func() error { return s.SetThinkingLevel(level) }
		}},
	}
}

// The interactive owner loop takes input and renders, so it waits for no extension (AGENTS.md: no blocking extension IPC on the TUI input loop). The
// first handler here stands for the event proxy of a subprocess extension whose runtime reports no first suspension (Go, Rust, Python, Lisp:
// subprocess ack_hold.go): nothing acknowledges the dispatch before the handler returns. Pi's `void emit` returns to its event loop as soon as the
// handler awaits (agent-session.ts:setSessionName, setThinkingLevel), so `/name` typed while such a handler sleeps leaves Pi's terminal live.
func TestUnawaitedEventDoesNotHoldTheOwnerLoop(t *testing.T) {
	for _, tc := range unawaitedEventCases() {
		t.Run(tc.event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				s, call := tc.start(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{
					tc.event: {func(...any) (any, error) {
						close(entered)
						<-release
						return nil, nil
					}},
				}})
				ui := &ownerLoopUI{UIContext: extension.NoopUIContext}
				ui.onLoop.Store(true)
				s.currentRunner().SetUIContext(ui, extension.ModeTUI)
				done := make(chan error, 1)
				go func() { done <- call() }()
				synctest.Wait()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("the call that emits %s waits on the owner loop for a handler that has not reported its first suspension", tc.event)
				}
				select {
				case <-entered:
				default:
					t.Errorf("the %s handler did not start", tc.event)
				}
				close(release)
			})
		})
	}
}

// Off the owner loop the call keeps Pi's order with the same UI bound: the first handler's synchronous prefix runs before the call returns
// (runner.ts emit runs it on the caller's stack), and the call does not wait for the handler once it is suspended.
func TestUnawaitedEventKeepsTheFirstPrefixOffTheOwnerLoop(t *testing.T) {
	for _, tc := range unawaitedEventCases() {
		t.Run(tc.event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				proceed, release := make(chan struct{}), make(chan struct{})
				var prefix atomic.Bool
				s, call := tc.start(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{
					tc.event: {func(args ...any) (any, error) {
						<-proceed
						prefix.Store(true)
						invocation.Acknowledge(args[1].(context.Context))
						<-release
						return nil, nil
					}},
				}})
				s.currentRunner().SetUIContext(&ownerLoopUI{UIContext: extension.NoopUIContext}, extension.ModeTUI)
				type outcome struct {
					prefix bool
					err    error
				}
				done := make(chan outcome, 1)
				go func() {
					err := call()
					done <- outcome{prefix: prefix.Load(), err: err}
				}()
				synctest.Wait()
				select {
				case <-done:
					t.Errorf("the call that emits %s returned before the first handler's synchronous prefix", tc.event)
				default:
				}
				close(proceed)
				synctest.Wait()
				select {
				case got := <-done:
					if got.err != nil || !got.prefix {
						t.Errorf("the call returned with prefix run = %t, error = %v; want it after the prefix", got.prefix, got.err)
					}
				default:
					t.Errorf("the call that emits %s waited for a suspended handler", tc.event)
				}
				close(release)
			})
		})
	}
}
