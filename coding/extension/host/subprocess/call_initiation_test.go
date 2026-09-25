package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream ctx.ui.select installs its dialog before it returns the pending
// Promise, so `void ctx.ui.select(...); ctx.ui.setStatus(...)` applies the
// dialog first. A Promise-shaped call must begin before the next call in its
// lane applies. One scheduler processor and a barrier make this deterministic.
func TestPromiseCallStartsBeforeNextSynchronousCall(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	host, peer := net.Pipe()
	defer func() { _ = host.Close() }()
	defer func() { _ = peer.Close() }()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	conn := NewConn("order", host)
	me := &managedExt{config: ExtConfig{Name: "order"}, host: h, conn: conn}
	var started atomic.Bool
	firstDone := make(chan struct{})
	observed := make(chan bool, 1)
	h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
		if call.Method == "ui.select" {
			started.Store(true)
			close(firstDone)
		} else {
			observed <- started.Load()
		}
		return nil, nil
	})
	lanes := newCallLanes()
	barrier := make(chan struct{})
	entered := make(chan struct{})
	lanes.push("", func() { close(entered); <-barrier })
	<-entered
	h.queueCall(me, lanes, "", &CallPayload{Method: "ui.select"})
	h.queueCall(me, lanes, "", &CallPayload{Method: "ui.setStatus"})
	close(barrier)
	sawFirst := <-observed
	<-firstDone
	if !sawFirst {
		t.Fatal("a later synchronous call applied before an earlier Promise-shaped call began")
	}
}

// initiationUI installs a dialog, reports it, and then waits for the user.
type initiationUI struct {
	extension.UIContext
	mu      sync.Mutex
	events  []string
	release chan struct{}
}

func (u *initiationUI) ReportsDialogInitiation() bool { return true }

func (u *initiationUI) record(event string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, event)
}

func (u *initiationUI) Select(ctx context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.record("dialog:" + title)
	extension.CallInitiated(ctx)
	<-u.release
	return "picked", nil
}

func (u *initiationUI) SetStatus(key, text string) { u.record("status:" + text) }

// Through the UI bridge, a dialog's installation precedes a later status
// update, and the status update does not wait for the user to answer.
func TestDialogInstallsBeforeLaterCallWithoutWaitingForUser(t *testing.T) {
	ui := &initiationUI{UIContext: extension.NoopUIContext, release: make(chan struct{})}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	h.SetUIBridge(bridge)
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("dialog", host)
	conn.Start(t.Context())
	defer func() { _ = conn.Close("test done") }()
	go func() {
		buf := make([]byte, 1<<16)
		for {
			if _, err := peer.Read(buf); err != nil {
				return
			}
		}
	}()
	me := &managedExt{config: ExtConfig{Name: "dialog"}, host: h, conn: conn}
	lanes := newCallLanes()
	selectArgs, _ := json.Marshal(map[string]any{"title": "Pick", "options": []string{"a"}})
	statusArgs, _ := json.Marshal(map[string]string{"key": "k", "text": "after"})
	statusApplied := make(chan struct{})
	h.queueCall(me, lanes, "c1", &CallPayload{Method: "ui.select", Args: selectArgs})
	h.queueCall(me, lanes, "c2", &CallPayload{Method: "ui.setStatus", Args: statusArgs})
	lanes.push("", func() { close(statusApplied) })
	<-statusApplied
	ui.mu.Lock()
	events := append([]string(nil), ui.events...)
	ui.mu.Unlock()
	close(ui.release)
	if len(events) != 2 || events[0] != "dialog:Pick" || events[1] != "status:after" {
		t.Fatalf("events = %v, want the dialog installed before the status update", events)
	}
}

// A bridge must not release the lane merely because it has selected an async
// handler. For legacy callbacks without an initiation-aware context, the only
// proven start boundary is after the callback has actually run.
func TestExecInitiationMeansCallbackActuallyStarted(t *testing.T) {
	bridge := NewUIBridge(func() {})
	started := false
	bridge.SetActions(&HostCallbacks{Exec: func(string, []string, *extension.ExecOptions) (extension.ExecResult, error) {
		started = true
		return extension.ExecResult{}, nil
	}})
	ctx := extension.WithCallInitiation(context.Background(), func() {
		if !started {
			t.Error("lane released before exec callback began")
		}
	})
	if _, err := bridge.handleCall(ctx, "exec-order", nil, &CallPayload{Method: "exec", Args: json.RawMessage(`{"command":"fake"}`)}); err != nil {
		t.Fatal(err)
	}
}

// Callback-owned initiation runs only after the callback has entered. This is
// the synchronous-prefix boundary that lets the lane advance safely.
func TestWaitForIdleReportsInitiationAfterCallbackEntry(t *testing.T) {
	bridge := NewUIBridge(func() {})
	entered := false
	bridge.SetActions(&HostCallbacks{WaitForIdle: func(ctx context.Context) error {
		entered = true
		extension.CallInitiated(ctx)
		return nil
	}})
	ctx := extension.WithCallInitiation(context.Background(), func() {
		if !entered {
			t.Error("waitForIdle lane released before callback entry")
		}
	})
	if _, err := bridge.handleCall(ctx, "wait-order", nil, &CallPayload{Method: "waitForIdle"}); err != nil {
		t.Fatal(err)
	}
}

// Promise completion is independent of initiation. A pending waitForIdle must
// not serialize a later abort in the same extension call lane.
func TestPendingWaitForIdleDoesNotBlockLaterAbort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		bridge := NewUIBridge(func() {})
		entered := make(chan struct{})
		release := make(chan struct{})
		aborted := make(chan struct{})
		var once sync.Once
		bridge.SetActions(&HostCallbacks{
			WaitForIdle: func(ctx context.Context) error {
				close(entered)
				extension.CallInitiated(ctx)
				<-release
				return nil
			},
			Abort: func() {
				close(aborted)
				once.Do(func() { close(release) })
			},
		})
		host.SetUIBridge(bridge)
		hostEnd, peer := net.Pipe()
		defer func() { _ = hostEnd.Close() }()
		defer func() { _ = peer.Close() }()
		conn := NewConn("wait-order", hostEnd)
		me := &managedExt{config: ExtConfig{Name: "wait-order"}, host: host, conn: conn}
		lanes := newCallLanes()
		host.queueCall(me, lanes, "c1", &CallPayload{Method: "waitForIdle"})
		host.queueCall(me, lanes, "c2", &CallPayload{Method: "abort"})
		<-entered
		synctest.Wait()
		select {
		case <-aborted:
		default:
			t.Error("pending Promise waitForIdle blocked later abort")
			once.Do(func() { close(release) })
		}
	})
}

func TestBindCommandActionsInitiationFollowsLegacyActionEntry(t *testing.T) {
	bridge := NewUIBridge(func() {})
	entered := false
	bridge.BindCommandActions(extension.CommandActions{WaitForIdle: func() error {
		entered = true
		return nil
	}})
	ctx := extension.WithCallInitiation(context.Background(), func() {
		if !entered {
			t.Error("command-action lane released before the real action entered")
		}
	})
	if _, err := bridge.handleCall(ctx, "command-order", nil, &CallPayload{Method: "waitForIdle"}); err != nil {
		t.Fatal(err)
	}
}

func TestInitiationAwareCommandActionDoesNotBlockLaterAbort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		bridge := NewUIBridge(func() {})
		entered := make(chan struct{})
		release := make(chan struct{})
		aborted := make(chan struct{})
		var once sync.Once
		bridge.BindCommandActions(extension.CommandActions{WaitForIdleContext: func(ctx context.Context) error {
			close(entered)
			extension.CallInitiated(ctx)
			<-release
			return nil
		}})
		bridge.SetHostAction("abort", func() {
			close(aborted)
			once.Do(func() { close(release) })
		})
		host.SetUIBridge(bridge)
		hostEnd, peer := net.Pipe()
		defer func() { _ = hostEnd.Close() }()
		defer func() { _ = peer.Close() }()
		conn := NewConn("command-order", hostEnd)
		me := &managedExt{config: ExtConfig{Name: "command-order"}, host: host, conn: conn}
		lanes := newCallLanes()
		host.queueCall(me, lanes, "c1", &CallPayload{Method: "waitForIdle"})
		host.queueCall(me, lanes, "c2", &CallPayload{Method: "abort"})
		<-entered
		synctest.Wait()
		select {
		case <-aborted:
		default:
			t.Error("initiation-aware command action blocked later abort")
			once.Do(func() { close(release) })
		}
	})
}
