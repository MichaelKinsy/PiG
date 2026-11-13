package mcpext_test

// pi: packages/coding-agent/src/extensions/mcp/ui.ts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/coding/mcpext/mcptui"
)

// showManagerUI runs the factory of Custom like the host: the call blocks until the factory's done callback is called, or returns early
// when ctx ends.
type showManagerUI struct {
	extension.UIContext
	log *orderLog
}

type orderLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *orderLog) add(entry string) {
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
}

func (l *orderLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

func (u *showManagerUI) Custom(ctx context.Context, factory extension.CustomFactory, _ *extension.CustomOptions) (any, error) {
	finished := make(chan any, 1)
	component, err := factory(nil, nil, nil, func(result any) {
		u.log.add("done")
		finished <- result
	})
	if err != nil {
		return nil, err
	}
	defer component.Dispose()
	select {
	case result := <-finished:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ui.ts:253-267: `void manage().then(done, (error) => { notify(message, "error"); done(); })`. A failing manage flow notifies the user
// first and only then ends the view.
func TestShowManagerNotifiesTheFailureBeforeEndingTheView(t *testing.T) {
	log := &orderLog{}
	events := mcpext.EventContext{Notify: func(message, level string) { log.add("notify " + level + ": " + message) }}
	err := mcptui.ShowManager(context.Background(), &showManagerUI{log: log}, events, nil, func(mcpext.McpUi) error {
		log.add("manage")
		return errors.New("boom")
	})
	if err != nil {
		t.Fatalf("ShowManager = %v, want nil: the manage error is notified, not returned", err)
	}
	want := []string{"manage", "notify error: boom", "done"}
	got := log.snapshot()
	if len(got) != len(want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %q, want %q", got, want)
		}
	}
}

// A manage flow that succeeds ends the view without a notice.
func TestShowManagerEndsTheViewWithoutANoticeWhenManageSucceeds(t *testing.T) {
	log := &orderLog{}
	events := mcpext.EventContext{Notify: func(message, level string) { log.add("notify " + message) }}
	if err := mcptui.ShowManager(context.Background(), &showManagerUI{log: log}, events, nil, func(mcpext.McpUi) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := log.snapshot(); len(got) != 1 || got[0] != "done" {
		t.Fatalf("order = %q, want [done]", got)
	}
}

// The Go call joins the manage goroutine: when the host's Custom returns early because ctx ended, ShowManager still does not return
// before manage has returned, and a late manage error is still notified.
func TestShowManagerJoinsTheManageGoroutineWhenCustomReturnsEarly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := &orderLog{}
		events := mcpext.EventContext{Notify: func(message, level string) { log.add("notify " + message) }}
		ctx, cancel := context.WithCancel(context.Background())
		release := make(chan struct{})
		releaseManage := sync.OnceFunc(func() { close(release) })
		t.Cleanup(releaseManage)
		started := make(chan struct{})
		returned := make(chan error, 1)
		go func() {
			returned <- mcptui.ShowManager(ctx, &showManagerUI{log: log}, events, nil, func(mcpext.McpUi) error {
				close(started)
				<-release
				return errors.New("late")
			})
		}()
		<-started
		cancel()
		synctest.Wait()
		select {
		case <-returned:
			t.Fatal("ShowManager returned while the manage goroutine was still running")
		default:
		}
		releaseManage()
		synctest.Wait()
		if err := <-returned; !errors.Is(err, context.Canceled) {
			t.Fatalf("ShowManager = %v, want context.Canceled from Custom", err)
		}
		if got := log.snapshot(); len(got) < 1 || got[0] != "notify late" {
			t.Fatalf("log = %q, want the late failure notified before ShowManager returned", got)
		}
	})
}
