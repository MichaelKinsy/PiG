package subprocess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream extensions call the host in process, so every synchronous host API
// applies in program order and every Promise-returning API starts in program
// order. The host keeps that contract for extension→host calls: calls apply in
// arrival order within one ordering lane, and a call whose upstream API returns
// a Promise starts in order but completes on its own goroutine, so a later call
// never waits for a user, a process, or a model.
//
// A lane is keyed by the call's parent request. Calls an extension makes while
// it handles a host request belong to that request's lane. The host may be
// waiting for that request inside an earlier call of another lane, for example
// a setting change that emits an event to the same extension, so lanes never
// wait for one another.

// startsAsync reports whether the upstream API behind method returns a
// Promise. Such a call starts in lane order and then runs on its own goroutine.
func startsAsync(method string) bool {
	switch method {
	case "facet.host":
		return true
	case "ui.addAutocompleteProvider", "ui.autocomplete.invoke":
		// The public call remains synchronous in its SDK. A reverse factory/provider callback can reenter the host, so release its call lane at callback initiation.
		return true
	case "ui.select", "ui.confirm", "ui.input", "ui.editor", CallUICustom,
		"setModel", "registerProvider", "exec", "complete", "modelStream", CallProviderObject, "getModelAuth", "getProviderAuth", "refreshModelRegistry", "compact",
		"waitForIdle", "newSession", "fork", "navigateTree", "switchSession", "reload",
		CallOAuthOnPrompt, CallOAuthOnSelect, CallOAuthOnManualCodeInput:
		return true
	default:
		return false
	}
}

// callLanes runs extension→host calls in arrival order per lane. Each lane
// drains on a goroutine that exits when the lane is empty.
type callLanes struct {
	mu    sync.Mutex
	lanes map[string]*callLane
}

type callLane struct {
	queue []func()
	// initiated is closed once the dialog call that created the lane applied its synchronous part; nil when that call is not a dialog.
	initiated <-chan struct{}
}

func newCallLanes() *callLanes {
	return &callLanes{lanes: make(map[string]*callLane)}
}

// push queues run behind every earlier call of the same lane.
func (l *callLanes) push(lane string, run func()) { l.pushCall(lane, nil, run) }

// pushCall is push for an extension call. A call that starts a lane also
// waits for the initiation of each dialog call that started another lane
// before it: Pi runs the extension's calls in program order, so a dialog
// request an earlier request opened reaches stdout before a notification a
// later request sends. A dialog initiates when its request is published, which
// depends on no session state, so the wait cannot cycle. Other Promise-shaped
// calls are not waited for: waitForIdle, exec, compact and the auth calls
// initiate only when the session or a subprocess allows, and an event handler
// the awaited work needs may make a host call in between. initiated is the
// call's initiation channel, or nil; only a dialog's is recorded on the lane.
func (l *callLanes) pushCall(lane string, initiated <-chan struct{}, run func()) {
	l.mu.Lock()
	current := l.lanes[lane]
	if current != nil {
		current.queue = append(current.queue, run)
		l.mu.Unlock()
		return
	}
	var earlier []<-chan struct{}
	for _, other := range l.lanes {
		if other.initiated != nil {
			earlier = append(earlier, other.initiated)
		}
	}
	first := run
	if len(earlier) > 0 {
		first = func() {
			for _, done := range earlier {
				<-done
			}
			run()
		}
	}
	current = &callLane{queue: []func(){first}, initiated: initiated}
	l.lanes[lane] = current
	l.mu.Unlock()
	go l.drain(lane, current)
}

func (l *callLanes) drain(lane string, current *callLane) {
	for {
		l.mu.Lock()
		if len(current.queue) == 0 {
			delete(l.lanes, lane)
			l.mu.Unlock()
			return
		}
		run := current.queue[0]
		current.queue[0] = nil
		current.queue = current.queue[1:]
		l.mu.Unlock()
		run()
	}
}

// barrier returns once every call queued before it has run. A Promise-shaped
// call has run once it applied its synchronous part.
func (l *callLanes) barrier() {
	var reached sync.WaitGroup
	l.mu.Lock()
	for _, lane := range l.lanes {
		reached.Add(1)
		lane.queue = append(lane.queue, reached.Done)
	}
	l.mu.Unlock()
	reached.Wait()
}

// queueCall orders one extension→host call. A Promise-shaped call runs on its
// own goroutine, and the lane advances once the call has applied its
// synchronous part (it marks initiation) or has finished, whichever is first.
// The UI bridge marks initiation; a bare call handler has no initiation point,
// so its call finishes before the lane advances.
func (h *Host) queueCall(me *managedExt, conn *Conn, lanes *callLanes, callID string, call *CallPayload) {
	if slot := replaceOnlySlot(call.Method); slot != "" {
		// The caller is the connection's read loop, so registration follows the order the extension sent the calls.
		pending := h.slotCalls.register(slot, &slotCall{me: me, conn: conn, callID: callID, call: call})
		lanes.push(call.ParentRequestID, func() { h.runSlotCall(slot, pending) })
		return
	}
	var initiated chan struct{}
	if startsAsync(call.Method) {
		initiated = make(chan struct{})
	}
	var ordering <-chan struct{}
	if gatesOtherLanes(call.Method) {
		ordering = initiated
	}
	lanes.pushCall(call.ParentRequestID, ordering, func() {
		if initiated == nil {
			h.runCall(me, conn, callID, call, nil)
			return
		}
		var once sync.Once
		mark := func() { once.Do(func() { close(initiated) }) }
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer mark()
			h.runCall(me, conn, callID, call, mark)
		}()
		select {
		case <-initiated:
		case <-finished:
		}
	})
}

// replaceOnlySlot returns the UI slot a call replaces, or "" for any other call. Login shares the header slot (D60).
func replaceOnlySlot(method string) string {
	switch method {
	case "ui.setHeader", CallUISetLogin:
		return "header"
	case "ui.setFooter":
		return "footer"
	case "ui.setWidget":
		return "widgets"
	default:
		return ""
	}
}

// slotCall is a UI replacement read from the connection but not yet applied. run is the slot's application lock.
type slotCall struct {
	me     *managedExt
	conn   *Conn
	callID string
	call   *CallPayload
	run    *sync.Mutex
}

// pendingSlotCalls keeps UI replacements in arrival order across request lanes. Each slot has its own application lock.
type pendingSlotCalls struct {
	mu      sync.Mutex
	pending map[string][]*slotCall
	running map[string]*sync.Mutex
}

func (p *pendingSlotCalls) register(slot string, call *slotCall) *slotCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string][]*slotCall{}
		p.running = map[string]*sync.Mutex{}
	}
	if p.running[slot] == nil {
		p.running[slot] = &sync.Mutex{}
	}
	call.run = p.running[slot]
	p.pending[slot] = append(p.pending[slot], call)
	return call
}

// takeThrough removes and returns the slot's pending calls up to and including through, in read order. It returns nothing when through already ran.
func (p *pendingSlotCalls) takeThrough(slot string, through *slotCall) []*slotCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	queue := p.pending[slot]
	end := slices.Index(queue, through)
	if end < 0 {
		return nil
	}
	taken := slices.Clone(queue[:end+1])
	p.pending[slot] = slices.Delete(queue, 0, end+1)
	return taken
}

// runSlotCall runs through and every call the extension sent to the same slot before it that its own lane has not reached. Pi's setExtensionHeader and setExtensionFooter run each call once, in program order (interactive-mode.ts:2427-2488), while lanes let a later call reach the host first. The later call must not wait for the earlier lane, which may be blocked, so it runs the earlier calls itself, in send order, and the earlier lane finds them already run. The slot's application lock makes the whole sequence one step, so no call of the slot runs between an earlier call and the call that took it.
func (h *Host) runSlotCall(slot string, through *slotCall) {
	through.run.Lock()
	defer through.run.Unlock()
	for _, next := range h.slotCalls.takeThrough(slot, through) {
		h.runCall(next.me, next.conn, next.callID, next.call, nil)
	}
}

// runCall executes one extension→host call and sends its result on conn, the connection that delivered the call, even after an adoption replaces the
// member's current connection. mark, when non-nil, is the call's initiation mark.
func (h *Host) runCall(me *managedExt, conn *Conn, callID string, call *CallPayload, mark func()) {
	callCtx, releaseCall := conn.hostCallContext(call.ParentRequestID, callID)
	if h.uiBridge != nil {
		defer h.uiBridge.releaseAutocompleteCall(conn, call)
	}
	defer releaseCall()
	if callCtx.Err() != nil || !h.acceptsNodeGeneration(me) {
		// A dropped call delivers none of the cross-process references it carried.
		if call.Method == "registerProvider" || call.Method == callProviderConfigRef {
			h.xref.hold(conn, callID, providerConfigRefArg(call.Args))
		}
		h.xref.unhold(conn, callID)
		return
	}
	if mark != nil {
		callCtx = extension.WithCallInitiation(callCtx, mark)
	}
	if message := h.staleMessage.Load(); message != nil {
		if callID != "" {
			_ = conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: &CallResultPayload{Error: &ErrorInfo{Message: *message}}})
		}
		return
	}
	if call.Method == "watchSessionLog" {
		// Keep session responses ahead of incremental state pushes.
		me.entryCursorMu.Lock()
		defer me.entryCursorMu.Unlock()
	}
	// An extension must never crash the host. Any nil-pointer or state
	// inconsistency (especially during /reload transitions) becomes an error
	// result for this call.
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("panic in HandleCall for %s.%s: %v", me.config.Name, call.Method, r)
			fmt.Fprintf(os.Stderr, "%s\n", errMsg)
			if callID != "" {
				_ = conn.Send(&Envelope{
					Type:       MsgCallResult,
					ID:         callID,
					CallResult: &CallResultPayload{Error: &ErrorInfo{Code: "internal_panic", Message: errMsg}},
				})
			}
		}
	}()

	var result *CallResultPayload
	var err error
	switch {
	case me.facetBridge != nil && (call.Method == "facet.host" || call.Method == "facet.host.sync"):
		result, err = me.facetBridge.handleCall(callCtx, call)
	case call.Method == CallRegisterTool:
		result, err = h.handleToolRegistration(callCtx, me, conn, call)
	case call.Method == callXrefHello || call.Method == callXrefOp:
		result, err = h.handleXrefCall(callCtx, me, conn, callID, call)
	case call.Method == callEventsOn || call.Method == callEventsOff || call.Method == callEventsEmit || call.Method == callEventsSettle:
		result, err = h.handleEventBusCall(callCtx, conn, callID, call)
	case call.Method == "event.subscribe" || call.Method == "event.unsubscribe":
		result, err = h.handleEventSubscription(me, call)
	case call.Method == "provider.retain" || call.Method == "provider.release":
		result, err = h.handleProviderReference(conn, call)
	case call.Method == CallProviderObject:
		result, err = h.handleProviderObject(callCtx, conn, call)
	case call.Method == CallProviderPublish || call.Method == CallProviderCallback:
		result, err = h.handleProviderPublication(callCtx, conn, call)
	case call.Method == "registerProvider" || call.Method == "unregisterProvider":
		result, err = h.handleProviderRegistrationCall(callCtx, me, conn, callID, call)
	case call.Method == callProviderConfigRef || call.Method == callProviderConfig:
		result, err = h.handleProviderConfigCall(callCtx, me, conn, callID, call)
	case strings.HasPrefix(call.Method, "oauth.cb."):
		result, err = h.handleOAuthCallback(callCtx, me.config.Name, call)
	case h.uiBridge != nil:
		result, err = h.uiBridge.handleCall(callCtx, me.config.Name, conn, call)
	case h.onCall != nil:
		result, err = h.onCall(me.config.Name, call)
	}
	if (call.Method == "setModel" || takesInteractiveFocus(call.Method)) && err == nil {
		// Pi's model changes and dialogs expose current state before their Promise settles, including cancellation and dialog errors returned in the payload.
		err = h.pushStateTo(callCtx, me, conn)
	}
	if callID == "" {
		return
	}
	if err != nil {
		_ = conn.Send(&Envelope{
			Type:       MsgCallResult,
			ID:         callID,
			CallResult: &CallResultPayload{Error: &ErrorInfo{Message: err.Error()}},
		})
		return
	}
	if result == nil {
		return
	}
	sendErr := conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: result})
	if tooLarge, ok := errors.AsType[*FrameTooLargeError](sendErr); ok {
		// No compliant extension can read a frame above MaxFrameSize; return a
		// small error result instead of writing an undeliverable frame that
		// would silently kill the extension.
		fmt.Fprintf(os.Stderr, "extension %s call %s: result of %d bytes exceeds the %d byte IPC frame limit; returning an error\n",
			me.config.Name, call.Method, tooLarge.Size, tooLarge.Max)
		_ = conn.Send(&Envelope{
			Type: MsgCallResult,
			ID:   callID,
			CallResult: &CallResultPayload{Error: &ErrorInfo{
				Code:    "result_too_large",
				Message: fmt.Sprintf("result of %d bytes exceeds the %d byte IPC frame limit", tooLarge.Size, tooLarge.Max),
			}},
		})
	}
}

// handleProviderRegistrationCall applies a provider registration an extension
// makes after it loaded, through pi.registerProvider or
// ctx.modelRegistry.registerProvider, and the matching unregistrations.
// Upstream's bindCore makes both take effect immediately (runner.ts), in the
// one registry every extension shares.
func (h *Host) handleProviderRegistrationCall(ctx context.Context, me *managedExt, conn *Conn, callID string, call *CallPayload) (*CallResultPayload, error) {
	var request struct {
		Name   string                     `json:"name"`
		Config json.RawMessage            `json:"config"`
		Native *NativeProviderDeclaration `json:"native"`
	}
	// The call's configRef transmission is accounted however the call ends, so the author can release its export.
	configRef := providerConfigRefArg(call.Args)
	h.xref.hold(conn, callID, configRef)
	// A call from an extension that a reload has replaced must not take the live replacement's registration or cleanup claim.
	if me.shuttingDown.Load() {
		h.xref.unhold(conn, callID)
		return nil, errors.New("extension was replaced")
	}
	if err := json.Unmarshal(call.Args, &request); err != nil {
		h.xref.unhold(conn, callID)
		return nil, fmt.Errorf("parse %s args: %w", call.Method, err)
	}
	if strings.TrimSpace(request.Name) == "" {
		h.xref.unhold(conn, callID)
		return nil, errors.New("provider name must not be empty")
	}
	if call.Method == "unregisterProvider" {
		h.mu.Lock()
		if me.shuttingDown.Load() {
			h.mu.Unlock()
			return nil, errors.New("extension was replaced")
		}
		released := h.retireNativeProviderLocked(request.Name)
		// Pi's unregisterProvider removes the one effective registration whoever registered it (model-runtime.ts:791-797), so the caller takes every claim, including a bridged OAuth login, before releasing them.
		notifySuperseded := h.transferProviderOwnershipLocked(me, conn, request.Name)
		me.providerNames = slices.DeleteFunc(me.providerNames, func(name string) bool { return name == request.Name })
		oauth := slices.Contains(me.oauthProviderNames, request.Name)
		me.oauthProviderNames = slices.DeleteFunc(me.oauthProviderNames, func(name string) bool { return name == request.Name })
		h.mu.Unlock()
		notifySuperseded()
		h.releaseProviderCallbacks(released)
		h.dropProviderConfigRef(request.Name)
		h.providerRuntime.UnregisterProvider(request.Name)
		if oauth {
			ai.UnregisterOAuthProvider(request.Name)
		}
		if h.uiBridge != nil {
			h.uiBridge.ForgetProviderRegistration(request.Name)
		}
		return &CallResultPayload{}, nil
	}
	if request.Native != nil {
		if request.Native.ID != request.Name {
			return nil, errors.New("native provider id does not match registration")
		}
		if err := h.registerNativeProvider(ctx, me, conn, request.Native); err != nil {
			return nil, err
		}
		h.dropProviderConfigRef(request.Name)
		return &CallResultPayload{}, nil
	}
	var config extension.ProviderConfig
	if err := json.Unmarshal(request.Config, &config); err != nil {
		h.xref.unhold(conn, callID)
		return nil, fmt.Errorf("provider %s: decode config: %w", request.Name, err)
	}
	if err := h.providerRuntime.RegisterProvider(request.Name, config, extConfigOrigin(me.config)); err != nil {
		h.xref.unhold(conn, callID)
		return nil, err
	}
	h.mu.Lock()
	released := h.retireNativeProviderLocked(request.Name)
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	h.mu.Lock()
	if me.shuttingDown.Load() {
		h.mu.Unlock()
		h.xref.unhold(conn, callID)
		return nil, errors.New("extension was replaced")
	}
	registeredOAuth := slices.Contains(me.oauthProviderNames, request.Name)
	notifySuperseded := h.transferProviderOwnershipLocked(me, conn, request.Name)
	h.mu.Unlock()
	notifySuperseded()
	if !registeredOAuth {
		if err := h.registerOAuthProvider(me, request.Name, request.Config); err != nil {
			h.xref.unhold(conn, callID)
			return nil, err
		}
	}
	h.attachProviderConfigRef(me, conn, request.Name, callID, configRef)
	if h.uiBridge != nil {
		h.uiBridge.RecordProviderRegistration(request.Name, request.Config)
	}
	return &CallResultPayload{}, nil
}
