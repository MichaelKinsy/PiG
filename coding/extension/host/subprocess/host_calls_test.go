package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// callOrderFixture runs handleIncoming over a real Conn and lets a test write
// raw call frames as the extension would.
type callOrderFixture struct {
	t      *testing.T
	conn   *Conn
	ext    net.Conn
	writeM sync.Mutex

	resultMu sync.Mutex
	results  map[string]*CallResultPayload
	resultCh chan struct{}
}

func newCallOrderFixture(t *testing.T, handle func(call *CallPayload) (*CallResultPayload, error)) *callOrderFixture {
	t.Helper()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) { return handle(call) })
	return startCallOrderFixture(t, h)
}

// newBridgeCallOrderFixture routes calls through the production UI bridge path.
func newBridgeCallOrderFixture(t *testing.T, bridge *UIBridge) *callOrderFixture {
	t.Helper()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(bridge)
	return startCallOrderFixture(t, h)
}

func startCallOrderFixture(t *testing.T, h *Host) *callOrderFixture {
	t.Helper()
	hostSide, extSide := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	conn := NewConn("order", hostSide)
	conn.Start(ctx)
	me := withConn(&managedExt{config: ExtConfig{Name: "order"}, host: h}, conn)
	me.shuttingDown.Store(true)
	go h.handleIncoming(me, me.connection())
	f := &callOrderFixture{t: t, ext: extSide, conn: conn, results: map[string]*CallResultPayload{}, resultCh: make(chan struct{}, 1)}
	go f.readFrames()
	t.Cleanup(func() {
		cancel()
		_ = extSide.Close()
		<-conn.Done()
	})
	return f
}

// readFrames records the call results the host sends to the extension and drops every other frame.
func (f *callOrderFixture) readFrames() {
	for {
		var length [4]byte
		if _, err := io.ReadFull(f.ext, length[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(f.ext, data); err != nil {
			return
		}
		var envelope Envelope
		if json.Unmarshal(data, &envelope) != nil || envelope.Type != MsgCallResult || envelope.CallResult == nil {
			continue
		}
		f.resultMu.Lock()
		f.results[envelope.ID] = envelope.CallResult
		f.resultMu.Unlock()
		select {
		case f.resultCh <- struct{}{}:
		default:
		}
	}
}

// result waits for the host's result for call id.
func (f *callOrderFixture) result(id string) *CallResultPayload {
	f.t.Helper()
	deadline := time.After(testbudget.Wait(f.t))
	for {
		f.resultMu.Lock()
		got := f.results[id]
		f.resultMu.Unlock()
		if got != nil {
			return got
		}
		select {
		case <-f.resultCh:
		case <-deadline:
			f.t.Fatalf("timed out waiting for the result of call %s", id)
		}
	}
}

func (f *callOrderFixture) call(id, method, parent string, args any) {
	f.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		f.t.Fatal(err)
	}
	f.send(Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: method, Args: raw, ParentRequestID: parent}})
}

func (f *callOrderFixture) send(env Envelope) {
	f.t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		f.t.Fatal(err)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	f.writeM.Lock()
	defer f.writeM.Unlock()
	if _, err := f.ext.Write(length[:]); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.ext.Write(data); err != nil {
		f.t.Fatal(err)
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// Upstream ui.setStatus, appendEntry, sendMessage, and setActiveTools are
// synchronous in-process calls, so a burst an extension does not await applies
// in program order and the last call sent is the last applied.
func TestExtensionCallsApplyInSendOrder(t *testing.T) {
	const n = 2000
	var mu sync.Mutex
	var got []int
	done := make(chan struct{})
	f := newCallOrderFixture(t, func(call *CallPayload) (*CallResultPayload, error) {
		var args struct{ Seq int }
		_ = json.Unmarshal(call.Args, &args)
		mu.Lock()
		got = append(got, args.Seq)
		if len(got) == n {
			close(done)
		}
		mu.Unlock()
		return &CallResultPayload{}, nil
	})
	for i := range n {
		f.call(fmt.Sprintf("c%d", i), "ui.setStatus", "", map[string]int{"Seq": i})
	}
	waitSignal(t, done, "all calls")
	mu.Lock()
	defer mu.Unlock()
	for i, seq := range got {
		if seq != i {
			t.Fatalf("call %d applied at position %d; want send order (first inversion)", seq, i)
		}
	}
}

// A call the host is still running may wait for a request it sent the same
// extension, whose handler makes nested calls. Those calls carry the request as
// their parent and must run while the earlier call waits.
func TestNestedCallsRunWhileAnEarlierCallWaits(t *testing.T) {
	nested := make(chan struct{})
	outerDone := make(chan struct{})
	f := newCallOrderFixture(t, func(call *CallPayload) (*CallResultPayload, error) {
		switch call.Method {
		case "setActiveTools":
			select {
			case <-nested:
			case <-time.After(testbudget.Wait(t)):
			}
			close(outerDone)
		case "ui.notify":
			close(nested)
		}
		return &CallResultPayload{}, nil
	})
	f.call("c1", "setActiveTools", "", map[string]any{})
	// The nested call's parent is an outstanding host request, not a completed or fabricated generation.
	f.conn.pendingMu.Lock()
	f.conn.pending["r7"] = make(chan *Envelope, 1)
	f.conn.pendingMu.Unlock()
	f.call("c2", "ui.notify", "r7", map[string]any{})
	waitSignal(t, nested, "nested call")
	waitSignal(t, outerDone, "outer call")
}

// A Promise-returning upstream API completes on its own, so a later
// synchronous call does not wait for a process or a user.
func TestAsyncCallsDoNotBlockLaterCalls(t *testing.T) {
	release := make(chan struct{})
	execStarted := make(chan struct{})
	statusApplied := make(chan struct{})
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(&testUIContext{UIContext: extension.NoopUIContext, onStatus: func(string, string) { close(statusApplied) }})
	bridge.SetActions(&HostCallbacks{ExecContext: func(ctx context.Context, _ string, _ []string, _ *extension.ExecOptions) (extension.ExecResult, error) {
		close(execStarted)
		extension.CallInitiated(ctx)
		<-release
		return extension.ExecResult{}, nil
	}})
	h.SetUIBridge(bridge)
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("async", hostEnd)
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
	defer close(release)
	me := withConn(&managedExt{config: ExtConfig{Name: "async"}, host: h}, conn)
	lanes := newCallLanes()
	h.queueCall(me, me.connection(), lanes, "c1", &CallPayload{Method: "exec", Args: json.RawMessage(`{"command":"x"}`)})
	h.queueCall(me, me.connection(), lanes, "c2", &CallPayload{Method: "ui.setStatus", Args: json.RawMessage(`{"key":"k","text":"t"}`)})
	waitSignal(t, execStarted, "exec start")
	waitSignal(t, statusApplied, "status after a pending exec")
}

// orderedSlotUI records header, footer and login applications in the order they happen. Its first Notify blocks until released, so a test can hold one call lane while later lanes run; its second Notify reports through after. A rejected login is the one whose name is in rejectLogins.
type orderedSlotUI struct {
	*mockUIContext
	entered, release, after chan struct{}
	rejectLogin             string

	mu       sync.Mutex
	notifies int
	events   []string
}

func newOrderedSlotUI() *orderedSlotUI {
	return &orderedSlotUI{mockUIContext: &mockUIContext{}, entered: make(chan struct{}), release: make(chan struct{}), after: make(chan struct{})}
}

func (u *orderedSlotUI) record(event string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, event)
}

func (u *orderedSlotUI) applied() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.events)
}

func (u *orderedSlotUI) Notify(string, string) {
	u.mu.Lock()
	u.notifies++
	n := u.notifies
	u.mu.Unlock()
	if n == 1 {
		close(u.entered)
		<-u.release
		return
	}
	close(u.after)
}

func (u *orderedSlotUI) SetHeader(factory any) {
	lines, _ := factory.([]string)
	u.record("header:" + strings.Join(lines, ","))
}

func (u *orderedSlotUI) SetFooter(factory any) {
	lines, _ := factory.([]string)
	u.record("footer:" + strings.Join(lines, ","))
}

func (u *orderedSlotUI) SetLogin(definition extension.LoginDefinition) error {
	u.record("login:" + definition.Name)
	if definition.Name == u.rejectLogin {
		return fmt.Errorf("login %s rejected", definition.Name)
	}
	return nil
}

// slotSend is one header, footer or login call an extension sends.
type slotSend struct {
	id, method, parent string
	args               any
	event              string
}

func headerSend(id, parent, text string) slotSend {
	return slotSend{id, "ui.setHeader", parent, map[string]any{"lines": []string{text}}, "header:" + text}
}

func footerSend(id, parent, text string) slotSend {
	return slotSend{id, "ui.setFooter", parent, map[string]any{"lines": []string{text}}, "footer:" + text}
}

func loginSend(t *testing.T, id, parent, name string) slotSend {
	return slotSend{id, CallUISetLogin, parent, json.RawMessage(validLoginJSON(t, name)), "login:" + name}
}

// runSlotOrder sends a blocking notify in the empty lane, then sends every call in order and waits for the host's result to each. The calls in the empty lane wait behind the notify, so those results arrive only if a later call in another lane ran them. It returns the applications seen while the notify was still blocked and after it was released.
func runSlotOrder(t *testing.T, ui *orderedSlotUI, sends ...slotSend) (blocked, released []string, f *callOrderFixture) {
	t.Helper()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetWidgetRequestFunc(func(_ string, _ string, lines []string, _ extension.ExtensionWidgetOptions) {
		ui.record("widget:" + strings.Join(lines, ","))
	})
	f = newBridgeCallOrderFixture(t, bridge)
	f.conn.pendingMu.Lock()
	f.conn.pending["r9"] = make(chan *Envelope, 1)
	f.conn.pendingMu.Unlock()
	f.call("gate", "ui.notify", "", map[string]any{"message": "gate"})
	waitSignal(t, ui.entered, "gating notify")
	for _, send := range sends {
		f.call(send.id, send.method, send.parent, send.args)
	}
	// The last call is in a lane that is not blocked. Its result means the host ran it, and every earlier call it depends on.
	last := sends[len(sends)-1]
	f.result(last.id)
	blocked = ui.applied()
	close(ui.release)
	f.call("after", "ui.notify", "", map[string]any{"message": "after"})
	waitSignal(t, ui.after, "the blocked lane to drain")
	return blocked, ui.applied(), f
}

// Pi's setExtensionHeader and setExtensionFooter run each call once, in program order (interactive-mode.ts:2427-2488). The extension sends the older call in a lane that is blocked and the newer call in another lane. The newer call must not wait for the blocked lane, and the older call must still take effect first and exactly once (53-extension-header-spacers).
func TestReadLoopRunsSlotCallsInSendOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sends func(*testing.T) []slotSend
	}{
		{"header", func(*testing.T) []slotSend {
			return []slotSend{headerSend("c1", "", "older"), headerSend("c2", "r9", "newer")}
		}},
		{"footer", func(*testing.T) []slotSend {
			return []slotSend{footerSend("c1", "", "older"), footerSend("c2", "r9", "newer")}
		}},
		{"widget", func(*testing.T) []slotSend {
			return []slotSend{
				{"c1", "ui.setWidget", "", map[string]any{"key": "list", "lines": []string{"older"}, "width": 20}, "widget:older"},
				{"c2", "ui.setWidget", "r9", map[string]any{"key": "list", "lines": []string{"newer"}, "width": 40}, "widget:newer"},
			}
		}},
		{"header then login", func(t *testing.T) []slotSend {
			return []slotSend{headerSend("c1", "", "older"), loginSend(t, "c2", "r9", "newer")}
		}},
		{"login then header", func(t *testing.T) []slotSend {
			return []slotSend{loginSend(t, "c1", "", "older"), headerSend("c2", "r9", "newer")}
		}},
		{"three calls", func(*testing.T) []slotSend {
			return []slotSend{headerSend("c1", "", "first"), headerSend("c2", "", "second"), headerSend("c3", "r9", "third")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sends := tc.sends(t)
			want := make([]string, len(sends))
			for i, send := range sends {
				want[i] = send.event
			}
			blocked, released, f := runSlotOrder(t, newOrderedSlotUI(), sends...)
			if !slices.Equal(blocked, want) {
				t.Fatalf("applications while the older lane was blocked = %v, want each call once in send order %v", blocked, want)
			}
			if !slices.Equal(released, want) {
				t.Fatalf("applications after the older lane drained = %v, want %v (a call ran twice)", released, want)
			}
			for _, send := range sends {
				if result := f.result(send.id); result.Error != nil {
					t.Fatalf("call %s result = %+v, want success", send.id, result)
				}
			}
		})
	}
}

// A footer call does not order against header calls: they are separate slots.
func TestReadLoopDoesNotOrderFooterAgainstHeader(t *testing.T) {
	blocked, released, _ := runSlotOrder(t, newOrderedSlotUI(), headerSend("c1", "", "older"), footerSend("c2", "r9", "newer"))
	if want := []string{"footer:newer"}; !slices.Equal(blocked, want) || !slices.Equal(released, []string{"footer:newer", "header:older"}) {
		t.Fatalf("applications = %v then %v, want the footer alone while the header lane was blocked", blocked, released)
	}
}

// Pi's setExtensionWidget replaces synchronously, regardless of whether an SDK sends content as a host call or a no-reply push.
func TestReadLoopWidgetPushOrdersAgainstQueuedSlotCalls(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprintf("clear=%t", clear), func(t *testing.T) {
			ui := newOrderedSlotUI()
			bridge := newTestBridge(ui)
			bridge.SetWidgetSyncFunc(func(widgets map[string]*PushProxy) {
				if proxy := widgets["order:list"]; proxy != nil {
					ui.record("widget:" + strings.Join(proxy.Lines(), ","))
				} else {
					ui.record("widget:cleared")
				}
			})
			f := newBridgeCallOrderFixture(t, bridge)
			f.conn.pendingMu.Lock()
			f.conn.pending["r9"] = make(chan *Envelope, 1)
			f.conn.pendingMu.Unlock()
			f.call("seed", "ui.setWidget", "r9", map[string]any{"key": "list", "content": []string{"seed"}})
			f.result("seed")
			f.call("gate", "ui.notify", "", map[string]any{"message": "gate"})
			waitSignal(t, ui.entered, "gating notify")
			var release sync.Once
			defer release.Do(func() { close(ui.release) })
			var content []string
			if !clear {
				content = []string{"older"}
			}
			f.call("older", "ui.setWidget", "", map[string]any{"key": "list", "content": content})
			f.send(Envelope{Type: MsgWidgetPush, WidgetPush: &WidgetPushPayload{Key: "list", Lines: []string{"newer"}}})
			f.result("older")
			// A later slot call drains all earlier replacements, even while their original lane is blocked.
			f.call("barrier", "ui.setWidget", "r9", map[string]any{"key": "barrier", "content": []string{}})
			f.result("barrier")
			want := []string{"widget:seed", "widget:older", "widget:newer", "widget:newer"}
			if clear {
				want[1] = "widget:cleared"
			}
			if got := ui.applied(); !slices.Equal(got, want) {
				t.Fatalf("widget applications = %v, want %v", got, want)
			}
			if proxy := bridge.GetWidget("order", "list"); proxy == nil || !slices.Equal(proxy.Lines(), []string{"newer"}) {
				t.Fatal("queued host call overwrote the later SDK push")
			}
			f.call("clear", "ui.setWidget", "r9", map[string]any{"key": "list", "content": nil})
			f.result("clear")
			if bridge.GetWidget("order", "list") != nil {
				t.Fatal("an earlier SDK push survived the later clear")
			}
			release.Do(func() { close(ui.release) })
			f.call("after", "ui.notify", "", map[string]any{"message": "after"})
			waitSignal(t, ui.after, "the original lane to drain")
			if got := ui.applied(); !slices.Equal(got, append(want, "widget:cleared")) {
				t.Fatalf("draining the original lane reapplied a widget: %v", got)
			}
		})
	}
}

// A login the UI accepts stays the header slot when a later login is rejected and an earlier header is delayed: the delayed header applies first, the accepted login replaces it, and the rejected login reports ui_error and installs nothing (rev-sol-ci-rc2-final-r P2: the earlier header must not overwrite an accepted login).
func TestReadLoopRejectedLoginDoesNotLetDelayedHeaderOverwriteAcceptedLogin(t *testing.T) {
	ui := newOrderedSlotUI()
	ui.rejectLogin = "rejected"
	blocked, released, f := runSlotOrder(t, ui,
		headerSend("c1", "", "delayed"),
		loginSend(t, "c2", "r9", "accepted"),
		loginSend(t, "c3", "r9", "rejected"),
	)
	want := []string{"header:delayed", "login:accepted", "login:rejected"}
	if !slices.Equal(blocked, want) || !slices.Equal(released, want) {
		t.Fatalf("applications = %v then %v, want %v", blocked, released, want)
	}
	if result := f.result("c2"); result.Error != nil {
		t.Fatalf("accepted login result = %+v", result)
	}
	if result := f.result("c3"); result.Error == nil || result.Error.Code != "ui_error" {
		t.Fatalf("rejected login result = %+v, want ui_error", result)
	}
}

// holdingHeaderUI holds the header call for "older" inside SetHeader.
type holdingHeaderUI struct {
	*orderedSlotUI
	holding, hold chan struct{}
}

func (u *holdingHeaderUI) SetHeader(factory any) {
	if lines, _ := factory.([]string); len(lines) == 1 && lines[0] == "older" {
		close(u.holding)
		<-u.hold
	}
	u.orderedSlotUI.SetHeader(factory)
}

// A later call that runs earlier calls of its slot holds the slot until they are applied. Another lane's call sent after them must not apply between them (rev-sol-ci-rc2-final-r P2 ordering; interactive-mode.ts:2454-2488).
func TestReadLoopSlotCallWaitsForEarlierCallsRunByAnotherLane(t *testing.T) {
	ui := &holdingHeaderUI{orderedSlotUI: newOrderedSlotUI(), holding: make(chan struct{}), hold: make(chan struct{})}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	f := newBridgeCallOrderFixture(t, bridge)
	f.conn.pendingMu.Lock()
	f.conn.pending["r8"] = make(chan *Envelope, 1)
	f.conn.pending["r9"] = make(chan *Envelope, 1)
	f.conn.pendingMu.Unlock()
	f.call("gate", "ui.notify", "", map[string]any{"message": "gate"})
	waitSignal(t, ui.entered, "gating notify")
	for _, send := range []slotSend{headerSend("c1", "", "older"), headerSend("c2", "r9", "newer")} {
		f.call(send.id, send.method, send.parent, send.args)
	}
	waitSignal(t, ui.holding, "the older call to be running in the newer call's lane")
	third := headerSend("c3", "r8", "third")
	f.call(third.id, third.method, third.parent, third.args)
	// The third call must wait for the slot, not enter the bridge and race the newer call for the header lock.
	if parked := waitSlotCallDoneOrParked(t, f.resultArrived(third.id)); parked == "" {
		t.Fatal("the third call applied while an earlier call of its slot was still running")
	} else if strings.Contains(parked, "(*UIBridge)") {
		t.Fatalf("the third call entered the UI bridge while an earlier call of its slot was still running:\n%s", parked)
	}
	close(ui.hold)
	f.result("c3")
	close(ui.release)
	if got, want := ui.applied(), []string{"header:older", "header:newer", "header:third"}; !slices.Equal(got, want) {
		t.Fatalf("applications = %v, want %v", got, want)
	}
}

// A header call held in the UI does not delay a footer call: each slot has its own application lock.
func TestReadLoopFooterDoesNotWaitForRunningHeader(t *testing.T) {
	ui := &holdingHeaderUI{orderedSlotUI: newOrderedSlotUI(), holding: make(chan struct{}), hold: make(chan struct{})}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	f := newBridgeCallOrderFixture(t, bridge)
	f.conn.pendingMu.Lock()
	f.conn.pending["r8"] = make(chan *Envelope, 1)
	f.conn.pending["r9"] = make(chan *Envelope, 1)
	f.conn.pendingMu.Unlock()
	older := headerSend("c1", "r8", "older")
	f.call(older.id, older.method, older.parent, older.args)
	waitSignal(t, ui.holding, "the header call to be running")
	footer := footerSend("c2", "r9", "footer")
	f.call(footer.id, footer.method, footer.parent, footer.args)
	if parked := waitSlotCallDoneOrParked(t, f.resultArrived(footer.id)); parked != "" {
		t.Fatalf("the footer call waited for the running header call:\n%s", parked)
	}
	close(ui.hold)
	f.result(older.id)
	if got, want := ui.applied(), []string{"footer:footer", "header:older"}; !slices.Equal(got, want) {
		t.Fatalf("applications = %v, want %v", got, want)
	}
}

// resultArrived returns a channel that closes once the host has sent the result for call id. Its poller stops when the test ends.
func (f *callOrderFixture) resultArrived(id string) <-chan struct{} {
	done := make(chan struct{})
	stop := make(chan struct{})
	f.t.Cleanup(func() { close(stop) })
	go func() {
		for {
			f.resultMu.Lock()
			got := f.results[id]
			f.resultMu.Unlock()
			if got != nil {
				close(done)
				return
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return done
}

// waitSlotCallDoneOrParked returns "" once done closes, or the stack of a goroutine parked on a mutex below runSlotCall.
func waitSlotCallDoneOrParked(t *testing.T, done <-chan struct{}) string {
	t.Helper()
	deadline := time.After(testbudget.Wait(t))
	buf := make([]byte, 1<<20)
	for {
		select {
		case <-done:
			return ""
		case <-deadline:
			t.Fatal("slot call neither completed nor parked in runSlotCall")
		default:
		}
		for g := range strings.SplitSeq(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			header, _, _ := strings.Cut(g, "\n")
			if strings.Contains(header, "sync.Mutex.Lock") && strings.Contains(g, "runSlotCall") {
				return g
			}
		}
		time.Sleep(time.Millisecond)
	}
}

// A runtime reports a command suspended after it sent the calls the command
// made, and Pi applies those calls' synchronous parts (for example writing a
// dialog request) before the process can exit. The host therefore counts the
// command as suspended only once the calls the connection received before the
// report applied their synchronous parts.
func TestSuspensionAppliesAfterEarlierCallsOfTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		entered := make(chan struct{})
		release := make(chan struct{})
		h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			close(entered)
			<-release
			return &CallResultPayload{}, nil
		})
		hostSide, extSide := net.Pipe()
		conn := NewConn("suspend-order", hostSide)
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "suspend-order"}, host: h}, conn)
		me.shuttingDown.Store(true)
		go h.handleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			_ = conn.Close("test done")
			_ = extSide.Close()
		}()
		dispatched := make(chan struct{})
		conn.onDispatch = func() { close(dispatched) }
		suspended := make(chan struct{})
		ctx := withRequestSuspension(t.Context(), func(bool) { close(suspended) }, nil)
		go func() {
			_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
		}()
		<-dispatched
		call, _ := json.Marshal(Envelope{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: "exec", ParentRequestID: "r1"}})
		writeFrame := func(data []byte) {
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			_, _ = extSide.Write(append(length[:], data...))
		}
		writeFrame(call)
		state, _ := json.Marshal(Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: "r1", State: "suspended"}})
		writeFrame(state)
		<-entered
		synctest.Wait()
		select {
		case <-suspended:
			t.Error("the command counted as suspended before an earlier call of its connection applied")
		default:
		}
		close(release)
		synctest.Wait()
		select {
		case <-suspended:
		default:
			t.Error("the suspension was never applied after the earlier call")
		}
	})
}
