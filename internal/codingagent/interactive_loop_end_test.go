package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A task posted to the owner loop from another goroutine waits for room in a queue that only the loop drains. Once the loop has ended nothing drains it, and
// the context of the post may outlive the loop for the whole process (runCtx ends with the process, not with Run), so the post is released by the end of the
// loop: it reports that the loop did not take the task, as it does when its context ends.

func fillOwnerQueue(m *InteractiveMode, ran *atomic.Int64) {
	for len(m.uiTaskCh) < cap(m.uiTaskCh) {
		m.uiTaskCh <- func() { ran.Add(1) }
	}
}

func TestPostToMainIsReleasedWhenTheOwnerLoopEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		m.runCtx = t.Context()
		var ran, posted atomic.Int64
		fillOwnerQueue(m, &ran)
		result := make(chan error, 1)
		go func() { result <- m.postToMain(m.runCtx, func() { posted.Add(1) }) }()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("a post to a full queue returned %v while the loop had not ended", err)
		default:
		}
		m.runEnded.end()
		synctest.Wait()
		select {
		case err := <-result:
			if !errors.Is(err, errOwnerLoopUnavailable) {
				t.Fatalf("post after the loop ended = %v, want errOwnerLoopUnavailable", err)
			}
		default:
			t.Fatal("a post to a full queue stayed blocked after the owner loop ended")
		}
		if posted.Load() != 0 || len(m.uiTaskCh) != cap(m.uiTaskCh) {
			t.Fatalf("the task that was not delivered ran %d times, queue holds %d of %d", posted.Load(), len(m.uiTaskCh), cap(m.uiTaskCh))
		}
	})
}

// A loop that lives delivers every post once, in the order of the posts, and no post reports the loop as ended.
func TestPostToMainDeliversEachPostOnceInOrderWhileTheLoopLives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m.runCtx = ctx
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		var seen []int
		const posts = 300
		for i := range posts {
			if err := m.postToMain(ctx, func() { seen = append(seen, i) }); err != nil {
				t.Fatalf("post %d while the loop lives = %v", i, err)
			}
		}
		synctest.Wait()
		cancel()
		<-loopDone
		if len(seen) != posts {
			t.Fatalf("%d of %d posts ran", len(seen), posts)
		}
		for i, got := range seen {
			if got != i {
				t.Fatalf("post %d ran as number %d: order lost", got, i)
			}
		}
	})
}

// A caller that waits for a posted task to run must not wait for a task the ended loop will never run, also when the queue had room for it.
func TestRunOnMainAndWaitIsReleasedWhenTheOwnerLoopEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		m.runCtx = t.Context()
		var ran atomic.Bool
		result := make(chan error, 1)
		go func() { result <- m.runOnMainAndWait(m.runCtx, func() error { ran.Store(true); return nil }) }()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("the wait returned %v while the loop had not ended", err)
		default:
		}
		m.runEnded.end()
		synctest.Wait()
		select {
		case err := <-result:
			if !errors.Is(err, errOwnerLoopUnavailable) {
				t.Fatalf("wait after the loop ended = %v, want errOwnerLoopUnavailable", err)
			}
		default:
			t.Fatal("a wait for a posted task stayed blocked after the owner loop ended")
		}
		if ran.Load() {
			t.Fatal("the task of the ended loop ran")
		}
	})
}

// The loop stops draining where the outer input loop returns, which is before Stop and before Run returns: a quit by keys starts the shutdown of the extension
// hosts from inside that loop, so a release that waited for Stop would wait with the host.
func TestOwnerLoopEndIsSignalledWhereTheInputLoopReturns(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.runCtx = t.Context()
	if err := m.inputLoop(t.Context(), strings.NewReader("")); err == nil {
		t.Fatal("the input loop of an empty source ended without its error")
	}
	select {
	case <-m.runEnded.endedCh():
	default:
		t.Fatal("the owner loop returned and its end was not signalled")
	}
}

// crashProbeHost is the extension host of a mode, with the moment a crash notice enters the mode's handler visible.
type crashProbeHost struct {
	*subprocess.Host
	entered chan struct{}
	once    sync.Once
}

func (h *crashProbeHost) SetCrashHandler(fn func(string, time.Duration, bool, string)) {
	h.Host.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		h.once.Do(func() { close(h.entered) })
		fn(name, delay, disabled, reason)
	})
}

// loopEndRig is a mode with the real crash handler wired to a real host that runs one extension of the SDK in process; the extension's side of the connection is
// returned so that a test ends the extension.
func loopEndRig(t *testing.T, register func(*sdk.Extension)) (m *InteractiveMode, host *crashProbeHost, extSide func() net.Conn, tools map[string]func(context.Context) error) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	m, _ = newExtensionDialogProbe(t)
	m.runCtx = t.Context()
	fixture := sdk.New("dying")
	if register != nil {
		register(fixture)
	}
	host = &crashProbeHost{Host: subprocess.NewHost(t.TempDir()), entered: make(chan struct{})}
	m.opts.SubprocessHost = host
	m.attachSubprocess()
	connCh := make(chan net.Conn, 1)
	remote, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "dying", Enabled: true}, func(c net.Conn) error {
		connCh <- c
		return fixture.RunWithConn(c)
	})
	if err != nil {
		t.Fatal(err)
	}
	tools = map[string]func(context.Context) error{}
	for name, tool := range remote.Tools {
		tools[name] = func(ctx context.Context) error {
			_, err := tool.Definition.Execute(ctx, "call", json.RawMessage(`{}`), nil)
			return err
		}
	}
	conn := <-connCh
	return m, host, func() net.Conn { return conn }, tools
}

// shutdownAtLoopEnd is the order of a quit by keys: the dispose runs the host's Shutdown from inside the loop's exit, and the loop ends while it runs. It returns
// how long Shutdown took.
func shutdownAtLoopEnd(m *InteractiveMode, host *crashProbeHost) time.Duration {
	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		host.Shutdown("quit")
	}()
	synctest.Wait()
	m.runEnded.end()
	<-done
	return time.Since(start)
}

// An extension process dies while the owner queue is full and its crash notice is inside the post when quit begins: nothing drains the queue again, so the
// notice is not shown and quit does not wait for it.
func TestQuitDoesNotWaitForACrashNoticeThatCannotBePosted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, host, extSide, _ := loopEndRig(t, nil)
		var ran atomic.Int64
		fillOwnerQueue(m, &ran)
		_ = extSide().Close()
		<-host.entered
		synctest.Wait()
		if took := shutdownAtLoopEnd(m, host); took > time.Second {
			t.Fatalf("quit took %s with a crash notice blocked in the post to a queue nobody drains, want it released by the end of the loop", took)
		}
		for len(m.uiTaskCh) > 0 {
			(<-m.uiTaskCh)()
		}
		if got := ran.Load(); got != int64(cap(m.uiTaskCh)) {
			t.Fatalf("%d of the queued tasks ran", got)
		}
		if text := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n")); strings.Contains(text, "dying") {
			t.Fatalf("the crash notice was shown after quit began: %q", text)
		}
	})
}

// An extension's request that waits for the owner loop's answer is in flight when quit begins, and the queue has room, so its post was accepted and will never
// run: quit does not wait for the answer. The request runs on a goroutine of its own, not on the connection's loop, and the close of the connection that Shutdown
// begins with ends its context; the test holds that, and it held before the loop-ended case existed.
func TestQuitDoesNotWaitForAnExtensionRequestThatAwaitsTheOwnerLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		asked := make(chan struct{})
		m, host, _, tools := loopEndRig(t, func(e *sdk.Extension) {
			e.Tool("ask", "ask", sdk.Schema{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
				close(asked)
				_, _, err := ctx.Select("Pick", []string{"a"})
				return nil, err
			})
		})
		bridge := subprocess.NewUIBridge(func() {})
		host.SetUIBridge(bridge)
		bridge.SetUIContext(&ExtUIContext{m: m})
		returned := make(chan struct{})
		go func() {
			defer close(returned)
			_ = tools["ask"](t.Context())
		}()
		<-asked
		synctest.Wait()
		select {
		case <-returned:
			t.Fatal("the extension's request returned before quit began: nothing waited for the owner loop")
		default:
		}
		if took := shutdownAtLoopEnd(m, host); took > time.Second {
			t.Fatalf("quit took %s with an extension request waiting for the owner loop, want it released by the end of the loop", took)
		}
		<-returned
	})
}
