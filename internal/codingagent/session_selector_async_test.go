package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSessionSelectorLoadCancellationAndFailure(t *testing.T) {
	t.Run("cancel drops stale progress and results", func(t *testing.T) {
		current := []SessionInfo{scopeSession("current")}
		var old *sessionLoad
		var report SessionListProgress
		deferred := make(chan sessionLoadResult, 1)
		s := newSessionSelectorWithLoaders(resolvedSessionLoader(func() ([]SessionInfo, error) { return current, nil }), func(load *sessionLoad, p SessionListProgress) <-chan sessionLoadResult {
			old = load
			report = p
			return deferred
		}, nil, nil, "", sessionSelectorInputBindings(t))
		defer s.close()
		s.drainLoadUpdates()
		s.HandleInput("\t")
		report(1, 2, []SessionInfo{scopeSession("partial")})
		s.HandleInput("\t")
		s.refreshCurrentScope()
		if !errors.Is(old.ctx.Err(), context.Canceled) {
			t.Fatal("refresh did not abort old load")
		}
		report(2, 2, []SessionInfo{scopeSession("stale")})
		deferred <- sessionLoadResult{sessions: []SessionInfo{scopeSession("stale")}}
		s.drainLoadUpdates()
		if s.all != nil || len(s.current) != 1 || s.current[0].ID != "current" {
			t.Fatalf("stale results retained: current=%v all=%v", s.current, s.all)
		}
	})
	t.Run("foreground rejection is surfaced and empty completion is cached", func(t *testing.T) {
		calls := 0
		s := newSessionSelectorWithLoaders(resolvedSessionLoader(func() ([]SessionInfo, error) { return nil, errors.New("fixture failure") }), resolvedSessionLoader(func() ([]SessionInfo, error) { calls++; return nil, nil }), nil, nil, "", sessionSelectorInputBindings(t))
		defer s.close()
		s.drainLoadUpdates()
		if s.loading || s.statusState.message != "Failed to load sessions: fixture failure" || !s.statusState.error || len(s.filtered) != 0 {
			t.Fatalf("failed load state: loading=%v status=%q rows=%v", s.loading, s.statusState.message, s.filtered)
		}
		s.HandleInput("\t")
		s.drainLoadUpdates()
		s.HandleInput("\t")
		s.drainLoadUpdates()
		s.HandleInput("\t")
		s.drainLoadUpdates()
		if calls != 1 {
			t.Fatalf("empty All scope loaded %d times", calls)
		}
	})
	t.Run("close cancels and joins blocked progress delivery", func(t *testing.T) {
		entered, exited := make(chan struct{}), make(chan struct{})
		s := newSessionSelectorWithLoaders(func(load *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
			return load.run(func(ctx context.Context) ([]SessionInfo, error) {
				defer close(exited)
				close(entered)
				load.post(func() { t.Error("cancelled UI update delivered") })
				return nil, ctx.Err()
			})
		}, nil, nil, nil, "", sessionSelectorInputBindings(t))
		<-entered
		s.close()
		select {
		case <-exited:
		default:
			t.Fatal("close did not join its worker")
		}
	})
}

func TestUpstreamSessionSelectorAsyncScopes(t *testing.T) {
	observed := []any{}
	for _, repeat := range []bool{false, true} {
		name := "does not switch scope back to All when All load resolves after toggling back to Current" // packages/coding-agent/test/session-selector-path-delete.test.ts:187
		if repeat {
			name = "does not start redundant All loads when toggling scopes while All is already loading"
		} // packages/coding-agent/test/session-selector-path-delete.test.ts:219
		t.Run(name, func(t *testing.T) {
			current := []SessionInfo{scopeSession("current")}
			all := []SessionInfo{scopeSession("all")}
			deferred := make(chan sessionLoadResult, 1)
			calls := 0
			s := newSessionSelectorWithLoaders(resolvedSessionLoader(func() ([]SessionInfo, error) { return current, nil }), func(_ *sessionLoad, onProgress SessionListProgress) <-chan sessionLoadResult {
				calls++
				if repeat {
					onProgress(1, 2, all)
				}
				return deferred
			}, nil, nil, "", sessionSelectorInputBindings(t))
			t.Cleanup(s.close)
			s.drainLoadUpdates() // flushPromises after construction.
			s.HandleInput("\t")
			s.HandleInput("\t")
			if repeat {
				s.HandleInput("\t")
				if calls != 1 {
					t.Fatalf("allLoadCalls=%d want1", calls)
				}
				if len(s.filtered) == 0 || s.filtered[s.selected].Session.Path != all[0].Path {
					t.Fatalf("selected session=%v want %s", s.filtered, all[0].Path)
				}
				if output := strings.Join(s.Render(120), "\n"); !strings.Contains(output, "Loading") {
					t.Fatalf("missing Loading: %s", output)
				}
			}
			deferred <- sessionLoadResult{sessions: all}
			s.drainLoadUpdates() // flushPromises after resolution.
			if !repeat {
				if calls != 1 {
					t.Fatalf("allLoadCalls=%d want1", calls)
				}
				output := strings.Join(s.Render(120), "\n")
				if !strings.Contains(output, "Resume Session (Current Folder)") || strings.Contains(output, "Resume Session (All)") {
					t.Fatalf("late All completion changed visible scope: %s", output)
				}
			}
			observed = append(observed, []any{calls, s.filtered[s.selected].Session.Path})
		})
	}
	if !t.Failed() {
		data, err := json.Marshal(observed)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("SESSION_PATH_DELETE_ASYNC %s\n", data)
	}
}
