package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Pi session-selector.ts:362-374 keeps a touched selection by path as progressively sorted snapshots arrive; untouched selections follow the first row.
func TestSessionSelectorProgressRetainsTouchedSelection(t *testing.T) {
	for _, touched := range []bool{false, true} {
		s := newSessionSelectorWithLoaders(resolvedSessionLoader(func() ([]SessionInfo, error) { return nil, nil }), nil, nil, nil, "", sessionSelectorInputBindings(t))
		t.Cleanup(s.close)
		var progress SessionListProgress
		s.allLoader = func(_ *sessionLoad, report SessionListProgress) <-chan sessionLoadResult {
			progress = report
			return make(chan sessionLoadResult)
		}
		s.HandleInput("\t")
		first, second, newest := scopeSession("first"), scopeSession("second"), scopeSession("newest")
		s.sortMode = sessionSortRecent
		progress(2, 3, []SessionInfo{first, second})
		if touched {
			s.HandleInput("\x1b[B")
		}
		progress(3, 3, []SessionInfo{newest, first, second})
		want := newest.Path
		if touched {
			want = second.Path
		}
		if got := s.filtered[s.selected].Session.Path; got != want {
			t.Errorf("touched=%v selected=%s want=%s", touched, got, want)
		}
	}
}

func TestSessionSelectorCompletedLoadSignal(t *testing.T) {
	var signal context.Context
	s := newSessionSelectorWithLoaders(func(load *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
		signal = load.ctx
		result := make(chan sessionLoadResult, 1)
		result <- sessionLoadResult{sessions: []SessionInfo{scopeSession("current")}}
		return result
	}, nil, nil, nil, "", sessionSelectorInputBindings(t))
	s.drainLoadUpdates()
	if signal.Err() != nil {
		t.Error("successful scope completion aborted its loader signal")
	}
	s.close()
	if signal.Err() != nil {
		t.Error("teardown aborted a completed load; Pi cancelLoads owns only active controllers")
	}
}

// The native listing adapter must carry the selector's cancellation and transfer progress snapshots to its input owner, not only satisfy the deferred-Promise fixture.
func TestSessionSelectorListingAdapterCancellation(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan error, 1)
	loader := func(options SessionListOptions) ([]SessionInfo, error) {
		close(started)
		partial := []SessionInfo{scopeSession("partial")}
		options.OnProgress(1, 2, partial)
		partial[0].ID = "reused"
		<-options.Context.Done()
		stopped <- options.Context.Err()
		return nil, options.Context.Err()
	}
	s := newSessionSelectorFromListers(loader, loader, nil, nil, "", sessionSelectorInputBindings(t))
	<-started
	update := <-s.work.updates
	update()
	if len(s.filtered) != 1 || s.filtered[0].Session.ID != "partial" || !strings.Contains(strings.Join(s.Render(120), "\n"), "Loading 1/2") {
		t.Errorf("progress snapshot not retained: %v", s.filtered)
	}
	s.HandleInput("\x1b")
	s.close()
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Errorf("listing context error=%v", err)
	}
}
