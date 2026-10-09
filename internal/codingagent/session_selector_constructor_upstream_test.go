package codingagent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type selectorProbe struct {
	selected   []string
	cancelled  int
	exited     int
	renders    atomic.Int32
	loadCtx    atomic.Pointer[context.Context]
	selector   *SessionSelectorComponent
	sessions   []SessionInfo
	renameCall atomic.Int32
}

// newProbe builds the selector through session-selector.ts:757's constructor shape. Both loaders publish their sessions through onProgress and then wait for the abort signal, like a long listing.
func newProbe(t *testing.T, options *SessionSelectorOptions) *selectorProbe {
	t.Helper()
	p := &selectorProbe{sessions: []SessionInfo{
		{Path: "/tmp/a.jsonl", Name: "Alpha", FirstMessage: "alpha", AllMessagesText: "alpha", Modified: time.Now()},
		{Path: "/tmp/b.jsonl", Name: "Beta", FirstMessage: "beta", AllMessagesText: "beta", Modified: time.Now().Add(-time.Hour)},
	}}
	loader := func(ctx context.Context, onProgress SessionListProgress) ([]SessionInfo, error) {
		p.loadCtx.Store(&ctx)
		onProgress(len(p.sessions), len(p.sessions), p.sessions)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p.selector = NewSessionSelectorComponent(loader, loader,
		func(path string) { p.selected = append(p.selected, path) },
		func() { p.cancelled++ },
		func() { p.exited++ },
		func() { p.renders.Add(1) },
		options, "/tmp/a.jsonl")
	t.Cleanup(p.selector.close)
	waitFor(t, func() bool { p.selector.drainLoadUpdates(); return len(p.selector.filtered) == 2 })
	return p
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
	}
}

// upstream: session-selector.ts:342-347,800-804,631-633 Enter (and the search input's submit) call onSelect with the highlighted session's path and cancel the loads (signal aborted).
func TestSessionSelectorConstructorEnterCallsOnSelectAndAbortsLoads(t *testing.T) {
	sessionSelectorInputBindings(t)
	p := newProbe(t, nil)
	p.selector.HandleInput("\r")
	if len(p.selected) != 1 || p.selected[0] != "/tmp/a.jsonl" || p.cancelled != 0 || p.exited != 0 {
		t.Fatalf("selected=%v cancelled=%d exited=%d", p.selected, p.cancelled, p.exited)
	}
	ctx := *p.loadCtx.Load()
	waitFor(t, func() bool { return ctx.Err() != nil })
}

// upstream: session-selector.ts:635-640,805-809 the cancel key calls onCancel (never onSelect) after aborting the loads.
func TestSessionSelectorConstructorEscapeCallsOnCancel(t *testing.T) {
	sessionSelectorInputBindings(t)
	p := newProbe(t, nil)
	p.selector.HandleInput("\x1b")
	if p.cancelled != 1 || len(p.selected) != 0 || p.exited != 0 {
		t.Fatalf("selected=%v cancelled=%d exited=%d", p.selected, p.cancelled, p.exited)
	}
	ctx := *p.loadCtx.Load()
	waitFor(t, func() bool { return ctx.Err() != nil })
}

// upstream: session-selector.ts:772-781 `showRenameHint ?? canRename`: the header shows the rename hint only when renaming is possible unless the option says otherwise.
func TestSessionSelectorConstructorRenameHintDefaultsToCanRename(t *testing.T) {
	sessionSelectorInputBindings(t)
	rename := func(string, string) error { return nil }
	for _, tc := range []struct {
		name    string
		options *SessionSelectorOptions
		want    bool
	}{
		{"no options", nil, false},
		{"rename operation", &SessionSelectorOptions{RenameSession: rename}, true},
		{"rename operation, hint off", &SessionSelectorOptions{RenameSession: rename, ShowRenameHint: new(false)}, false},
		{"hint on without rename", &SessionSelectorOptions{ShowRenameHint: new(true)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProbe(t, tc.options)
			if got := strings.Contains(stripANSI(strings.Join(p.selector.Render(120), "\n")), "rename"); got != tc.want {
				t.Errorf("rename hint shown=%v, want %v", got, tc.want)
			}
		})
	}
}

// upstream: session-selector.ts:649,905-1038 a loader's progress reaches the list and every load or status change calls requestRender.
func TestSessionSelectorConstructorRequestsRenderAfterLoadAndStatus(t *testing.T) {
	sessionSelectorInputBindings(t)
	p := newProbe(t, nil)
	if p.renders.Load() == 0 {
		t.Fatal("loading sessions never requested a render")
	}
	before := p.renders.Load()
	p.selector.setStatusMessage("hello", false, 0)
	if p.renders.Load() == before {
		t.Fatal("a status message never requested a render")
	}
}
