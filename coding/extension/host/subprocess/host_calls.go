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
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// Upstream extensions call the host in process, so every synchronous host API
// applies in program order and every Promise-returning API starts in program
// order. The host keeps that contract for extension→host calls: calls apply in
// arrival order within one ordering lane, the calls of different lanes start
// in arrival order (callLanes), and a call whose upstream API returns a
// Promise starts in order but completes on its own goroutine, so a later call
// never waits for a user, a process, or a model.
//
// A lane is keyed by the call's parent request (Conn.callLane), and the calls
// of every tool_call request share one lane. Calls an extension makes while
// it handles another host request belong to that request's lane. The host may
// be waiting for that request inside an earlier call of another lane, for
// example a setting change that emits an event to the same extension, so a
// lane waits for another lane's calls to start, never to finish.

// startsAsync reports whether the upstream API behind method returns a
// Promise. Such a call starts in lane order and then runs on its own goroutine.
func startsAsync(method string) bool {
	switch method {
	case "facet.host":
		return true
	case CallFileMutationEnter:
		// The call takes its place in the queue and then waits for the earlier mutations of the file: the lane advances once it is registered (CallInitiated), so a call made after it is not held behind another extension's long mutation.
		return true
	case "ui.addAutocompleteProvider", "ui.autocomplete.invoke":
		// The public call remains synchronous in its SDK. A reverse factory/provider callback can reenter the host, so release its call lane at callback initiation.
		return true
	case "ui.select", "ui.confirm", "ui.input", "ui.editor", CallUICustom,
		"setModel", "registerProvider", "exec", CallLoadingExec, "complete", "modelStream", CallProviderObject, "getModelAuth", "getProviderAuth", "getAvailableOfType", "classify", "generateImages", "refreshModelRegistry", "compact",
		"waitForIdle", "newSession", "fork", "navigateTree", "switchSession", "reload", CallExecuteTool, CallBuiltinToolExecute, CallBuiltinToolLocalExec, CallSessionGraphAsync,
		CallChildSessionCreate, CallChildSessionPrompt, CallChildSessionQueue, CallChildSessionAbort, CallChildSessionBindExtensions,
		CallOAuthOnPrompt, CallOAuthOnSelect, CallOAuthOnManualCodeInput:
		return true
	default:
		return false
	}
}

// holdsForLoopTurn reports whether a call the runtime marks Timer can wait for the loop's next turn: its upstream API returns nothing, so no code waits for its answer. A call whose answer the extension waits for (a dialog, a getter) runs at once.
func holdsForLoopTurn(method string) bool {
	switch method {
	case "ui.notify", "ui.setStatus", "ui.setWorkingMessage", "ui.setWorkingIndicator", "ui.setWorkingVisible", "ui.setHiddenThinkingLabel",
		"ui.setWidget", "ui.setFooter", "ui.setHeader", "ui.setTitle", "ui.pasteToEditor", "ui.setEditorText", "ui.setToolsExpanded",
		"appendEntry", "sendMessage", "sendUserMessage", "setThinkingLevel", "setActiveTools", "abort":
		return true
	default:
		return false
	}
}

// callLanes runs extension→host calls in arrival order per lane. Each lane
// drains on a goroutine that exits when the lane is empty.
//
// Calls of different lanes start in the order the connection read them:
// Pi runs the calls of concurrent requests on one thread, so the call an
// extension sent first applies first, and the scheduler's choice among the
// lane goroutines must not reorder them (jsloop.Lane). Each lane's call that
// is next to run holds a place in starts, taken in read order, and hands it on
// when the call starts, not when it finishes: a call may wait for a request
// whose own calls sit in another lane, so a lane never waits for another
// lane's call to end. A call queued behind an earlier call of its own lane
// takes its place when it reaches the head of the lane, which is why every
// tool_call request shares one lane: the calls of a parallel batch's tools
// then apply in the order they were read, the second call of one tool before
// the first of the next tool's.
type callLanes struct {
	mu     sync.Mutex
	lanes  map[string]*callLane
	starts jsloop.Lane
}

type callLane struct {
	queue []queuedCall
	// initiated is closed once the dialog call that created the lane applied its synchronous part; nil when that call is not a dialog.
	initiated <-chan struct{}
}

type queuedCall struct {
	run func()
	// place is the call's place in starts, taken when the call was read if it started a lane; nil for a call that waited behind another call of its lane, which takes its place when it reaches the head.
	place *jsloop.Place
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
		current.queue = append(current.queue, queuedCall{run: run})
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
	current = &callLane{queue: []queuedCall{{run: first, place: l.starts.Reserve()}}, initiated: initiated}
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
		next := current.queue[0]
		current.queue[0] = queuedCall{}
		current.queue = current.queue[1:]
		if next.place == nil {
			next.place = l.starts.Reserve()
		}
		l.mu.Unlock()
		next.place.Wait()
		next.place.Release()
		next.run()
	}
}

// barrier returns once every call queued before it has run. A Promise-shaped
// call has run once it applied its synchronous part.
func (l *callLanes) barrier() {
	l.barrierMark()()
}

// barrierMark marks the calls queued so far, on the caller's goroutine, and returns the function that waits until they have run. A caller that reads frames in
// order marks before it reads the next one, so a call the extension sent after the frame the caller marks for is not waited for.
func (l *callLanes) barrierMark() func() {
	var reached sync.WaitGroup
	l.mu.Lock()
	for _, lane := range l.lanes {
		reached.Add(1)
		lane.queue = append(lane.queue, queuedCall{run: reached.Done})
	}
	l.mu.Unlock()
	return reached.Wait
}

// queueCall orders one extension→host call. A Promise-shaped call runs on its
// own goroutine, and the lane advances once the call has applied its
// synchronous part (it marks initiation) or has finished, whichever is first.
// The UI bridge marks initiation; a bare call handler has no initiation point,
// so its call finishes before the lane advances.
func (h *Host) queueCall(me *managedExt, conn *Conn, lanes *callLanes, callID string, call *CallPayload) {
	h.queueCallDone(me, conn, lanes, callID, call, nil)
}

// queueCallDone is queueCall that calls done, when it is not nil, once the call ran or started: its effect is written. The request that made the call learns
// that it applied (ackHold). A call that can wait for the session or the user (startsAsync) does not hold the acknowledgment of the handler awaiting it: the
// handler is suspended on it, which is what the acknowledgment reports.
func (h *Host) queueCallDone(me *managedExt, conn *Conn, lanes *callLanes, callID string, call *CallPayload, done func()) {
	if startsAsync(call.Method) {
		conn.hostCallApplied(call.ParentRequestID)
	} else {
		applied := done
		done = func() {
			if applied != nil {
				applied()
			}
			conn.hostCallApplied(call.ParentRequestID)
		}
	}
	if done == nil {
		done = func() {}
	}
	if slot := replaceOnlySlot(call.Method); slot != "" {
		// The caller is the connection's read loop, so registration follows the order the extension sent the calls.
		pending := h.slotCalls.register(slot, func() { h.runCall(me, conn, callID, call, nil) })
		lanes.push(conn.callLane(call.ParentRequestID), func() { h.runSlotCall(slot, pending); done() })
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
	// The read loop reserves a nested call before it reads the frame that may cancel it.
	release := h.reserveNestedCall(conn, call)
	lanes.pushCall(conn.callLane(call.ParentRequestID), ordering, func() {
		if initiated == nil {
			defer release()
			h.runCall(me, conn, callID, call, nil)
			done()
			return
		}
		var once sync.Once
		mark := func() { once.Do(func() { close(initiated) }) }
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer mark()
			defer release()
			h.runCall(me, conn, callID, call, mark)
		}()
		select {
		case <-initiated:
		case <-finished:
		}
		done()
		// The call returns a Promise in Pi, so its caller is past its first await: when the caller is a tool_call request that holds a start window, the call
		// has applied its synchronous part, such as taking a place in the file mutation queue, and the calls behind the tool in its batch may register.
		conn.closeRequestWindow(call.ParentRequestID)
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
	apply func()
	run   *sync.Mutex
}

// pendingSlotCalls keeps UI replacements in arrival order across request lanes. Each slot has its own application lock.
type pendingSlotCalls struct {
	mu      sync.Mutex
	pending map[string][]*slotCall
	running map[string]*sync.Mutex
}

func (p *pendingSlotCalls) register(slot string, apply func()) *slotCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string][]*slotCall{}
		p.running = map[string]*sync.Mutex{}
	}
	if p.running[slot] == nil {
		p.running[slot] = &sync.Mutex{}
	}
	call := &slotCall{apply: apply, run: p.running[slot]}
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

// runSlotCall drains earlier replacements in arrival order without waiting for their potentially blocked lanes. Each replacement runs once under the slot's application lock.
func (h *Host) runSlotCall(slot string, through *slotCall) {
	through.run.Lock()
	defer through.run.Unlock()
	for _, next := range h.slotCalls.takeThrough(slot, through) {
		next.apply()
	}
}

// outputOrderedCalls are the fire-and-forget calls whose effect an RPC client reads from the output stream, where its place among the command responses is part of the protocol.
var outputOrderedCalls = map[string]bool{"ui.notify": true, "ui.setStatus": true, "ui.setWidget": true, "ui.setTitle": true, "ui.setEditorText": true}

// awaitBackgroundGate holds a call that no request started while the embedder's gate is closed. The Node runtime sends such a call from a timer or I/O callback, which on Pi's one event loop cannot run in the middle of a chain of awaited handlers, only after the chain ends. The wait ends when the gate opens or the call's context ends.
func (h *Host) awaitBackgroundGate(ctx context.Context, call *CallPayload) {
	if call.ParentRequestID != "" || !outputOrderedCalls[call.Method] {
		return
	}
	gate := h.backgroundGate.Load()
	if gate == nil {
		return
	}
	if open := (*gate)(); open != nil {
		select {
		case <-open:
		case <-ctx.Done():
		}
	}
}

// runCall executes one extension→host call and sends its result on conn, the connection that delivered the call, even after an adoption replaces the
// member's current connection. mark, when non-nil, is the call's initiation mark.
func (h *Host) runCall(me *managedExt, conn *Conn, callID string, call *CallPayload, mark func()) {
	// A call of a with_session request acts on the replacement Session, which the outgoing Session's invalidation does not reach.
	if route := conn.withSessionRoute(call.ParentRequestID); route != nil {
		route.run(conn, callID, call, mark)
		return
	}
	callCtx, releaseCall := conn.hostCallContext(call.ParentRequestID, callID)
	callCtx = withGraphResolver(callCtx, graphResolver{host: h, conn: conn})
	if h.uiBridge != nil {
		defer h.uiBridge.releaseAutocompleteCall(conn, call)
	}
	defer releaseCall()
	h.awaitBackgroundGate(callCtx, call)
	// A dropped or refused call delivers none of the cross-process references it carried.
	refuseReferences := func() {
		if call.Method == "registerProvider" || call.Method == callProviderConfigRef {
			h.xref.hold(conn, callID, providerConfigRefArg(call.Args))
		}
		h.xref.unhold(conn, callID)
	}
	if callCtx.Err() != nil {
		refuseReferences()
		return
	}
	// A reload invalidates the replaced runner, so its captured pi and ctx throw Pi's stale message (runner.ts:721-734, agent-session.ts:3580). The generation still runs while its command returns from `await ctx.reload()` (retireGeneration), so answer each call with that error: a dropped call would block the command, and the stop that waits for it, forever.
	if me.replaced.Load() {
		refuseReferences()
		if callID != "" {
			_ = conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: &CallResultPayload{Error: &ErrorInfo{Message: (&inproc.StaleError{}).Error()}}})
		}
		return
	}
	if !h.acceptsNodeGeneration(me) {
		refuseReferences()
		return
	}
	// Pi's loop stays alive for a call that starts I/O, a child process or a session change, and does not for a dialog that stdin can no longer answer. The count runs from the call's frame, and the drain notification that follows it waits for the call's lane (queueCall), so the drain sees it.
	if takesInteractiveFocus(call.Method) {
		defer conn.countHostCall(call.ParentRequestID, true)()
	} else if startsAsync(call.Method) {
		defer conn.countHostCall(call.ParentRequestID, false)()
	}
	// A call that awaits real work (a dialog, a child process, a model request) is a stretch in which Pi's loop turns (jsloop.Turns).
	if startsAsync(call.Method) {
		defer h.timerTurns.Load().Wait()()
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

	// upstream: a dialog's promise resolves on the line that answers it, so dialogs answered back to back continue, and the extension
	// receives their results, in the order of the answers. A UI mode that reads the answers records that order through the settle
	// function (extension.WithDialogSettle); the replies of the connection's dialogs are sent in it.
	var dialogSlot *jsloop.SettleSlot
	if callID != "" && takesInteractiveFocus(call.Method) {
		dialogSlot = conn.dialogOrder.NewSlot()
		defer dialogSlot.Close()
		callCtx = extension.WithDialogSettle(callCtx, dialogSlot.Settle)
	}

	// An exec call the extension owns the signal of ends when the extension reports the abort.
	callCtx, endExecSignal := h.execSignal(callCtx, conn, call)
	defer endExecSignal()

	var result *CallResultPayload
	var err error
	switch {
	case me.facetBridge != nil && (call.Method == "facet.host" || call.Method == "facet.host.sync"):
		result, err = me.facetBridge.handleCall(callCtx, call)
	case call.Method == CallRegisterTool:
		result, err = h.handleToolRegistration(callCtx, me, conn, call)
	case call.Method == CallRegisterDeclaration:
		result, err = h.handleDeclarationRegistration(callCtx, me, call)
	case call.Method == callXrefHello || call.Method == callXrefOp:
		result, err = h.handleXrefCall(callCtx, me, conn, callID, call)
	case call.Method == callEventsOn || call.Method == callEventsOff || call.Method == callEventsEmit || call.Method == callEventsSettle:
		result, err = h.handleEventBusCall(callCtx, me, conn, callID, call)
	case call.Method == "event.subscribe" || call.Method == "event.unsubscribe":
		result, err = h.handleEventSubscription(me, call)
	case call.Method == CallGetMcpServers:
		result, err = h.handleMcpServersRead()
	case call.Method == CallCheckMcpServer:
		result, err = h.handleMcpServerCheck(me, call)
	case call.Method == CallParseFrontmatter:
		result, err = h.handleParseFrontmatter(call)
	case call.Method == CallParseDate:
		result, err = h.handleParseDate(call)
	case call.Method == CallGenerateDiffString:
		result, err = h.handleGenerateDiffString(call)
	case call.Method == CallGenerateUnifiedPatch:
		result, err = h.handleGenerateUnifiedPatch(call)
	case call.Method == CallCopyToClipboard:
		result, err = h.handleCopyToClipboard(call)
	case call.Method == CallConvertToPng:
		result, err = h.handleConvertToPng(call)
	case call.Method == CallResizeImage:
		result, err = h.handleResizeImage(call)
	case call.Method == CallBuiltinToolDefinition:
		result, err = h.handleBuiltinToolDefinition(call)
	case call.Method == CallBuiltinToolExecute:
		result, err = h.handleBuiltinToolExecute(callCtx, conn, call)
	case call.Method == CallBuiltinToolLocalExec:
		result, err = h.handleBuiltinToolLocalExec(callCtx, conn, call)
	case call.Method == CallHighlightCode:
		result, err = h.handleHighlightCode(call)
	case call.Method == CallLoadTheme:
		result, err = h.handleLoadTheme(call)
	case call.Method == CallFileMutationEnter:
		result, err = h.handleFileMutationEnter(callCtx, conn, call)
	case call.Method == CallFileMutationExit:
		result, err = h.handleFileMutationExit(conn, call)
	case call.Method == CallLoadingExec:
		result, err = h.handleLoadingExec(callCtx, call)
	case call.Method == CallExecCancel:
		result, err = h.handleExecCancel(conn, call)
	case call.Method == CallRegisterMcpServer || call.Method == CallUnregisterMcpServer:
		result, err = h.handleMcpServerCall(callCtx, me, call)
	case call.Method == CallRegisterVirtualModel || call.Method == CallUnregisterVirtualModel:
		result, err = h.handleVirtualModelCall(callCtx, me, call)
	case call.Method == CallExecuteTool || call.Method == CallExecuteToolCancel:
		result, err = h.handleExecuteToolCall(callCtx, conn, call)
	case strings.HasPrefix(call.Method, "sessionGraph."):
		result, err = h.handleSessionGraphCall(callCtx, conn, call)
	case strings.HasPrefix(call.Method, "childSession."):
		result, err = h.handleChildSessionCall(callCtx, conn, call)
	case call.Method == CallUICancelDialog:
		var cancel UICancelDialog
		if err = json.Unmarshal(call.Args, &cancel); err != nil {
			err = fmt.Errorf("decode %s: %w", call.Method, err)
			break
		}
		h.nested.cancel(conn, dialogSignalKey(cancel.DialogID))
		result = &CallResultPayload{}
	case call.Method == "provider.retain" || call.Method == "provider.release":
		result, err = h.handleProviderReference(conn, call)
	case call.Method == CallProviderObject:
		result, err = h.handleProviderObject(callCtx, conn, call)
	case call.Method == CallProviderCallback:
		result, err = h.handleProviderPublication(callCtx, conn, call)
	case call.Method == "registerProvider" || call.Method == "unregisterProvider":
		result, err = h.handleProviderRegistrationCall(callCtx, me, conn, callID, call)
	case call.Method == callProviderConfigRef || call.Method == callProviderConfig:
		result, err = h.handleProviderConfigCall(callCtx, me, conn, callID, call)
	case strings.HasPrefix(call.Method, "oauth.cb."):
		result, err = h.handleOAuthCallback(callCtx, me.config.Name, call)
	case call.Method == "exec" && (h.uiBridge == nil && h.onCall == nil || h.uiBridge != nil && !h.uiBridge.hasExecAction()):
		result, err = h.handleUnboundExec(callCtx, call)
	case call.Method == "renderSystemPrompt" && h.uiBridge == nil:
		// The render is a pure function of the options the call carries, so a Host with no UI bridge (an SDK session) answers it too.
		result, err = renderSystemPromptCall(call.Args)
	case h.uiBridge != nil:
		dialogCtx := callCtx
		// The extension's options.signal dismisses its own dialog, and only that one.
		if id := dialogIDOf(call); id != "" {
			if signal := h.nested.signal(conn, dialogSignalKey(id)); signal != nil {
				var cancel context.CancelFunc
				dialogCtx, cancel = context.WithCancel(callCtx)
				defer cancel()
				defer context.AfterFunc(signal, cancel)()
			}
		}
		result, err = h.uiBridge.handleCall(dialogCtx, me.config.Name, conn, call)
	case h.onCall != nil:
		result, err = h.onCall(me.config.Name, call)
	case isEarlyModelStreamCall(call.Method) && (earlyModelStream.Load() != nil || h.sessionGraph() != nil):
		// A Host with no UI bridge (an SDK session, the validate and login loaders) still runs the pi-ai calls a module makes while it loads.
		result, err = h.earlyModelBridge().handleCall(callCtx, me.config.Name, conn, call)
	default:
		// Every call is answered: an extension waits for its result, so a call no handler takes is an error, not silence.
		err = fmt.Errorf("the host has no handler for %s", call.Method)
	}
	if dialogSlot != nil {
		dialogSlot.Run(func() { h.replyToCall(me, conn, callID, call, callCtx, result, err) })
		return
	}
	h.replyToCall(me, conn, callID, call, callCtx, result, err)
}

// publishesStateBeforeResult reports whether the host sends the caller its state before the result of method. Pi's Session writes are
// synchronous, so the caller's replicated read after the call must see them: a model change, an appended entry, a label, a session name and a
// custom message (agent-session.ts setModel, sessionManager.appendLabelChange, setSessionName, sendCustomMessage). A dialog that takes the
// focus exposes its state the same way. The Go SDK's statePublishingCalls lists the Session writes (extensions/sdk/context.go).
func publishesStateBeforeResult(method string) bool {
	switch method {
	case "setModel", "appendEntry", "setLabel", "setSessionName", "sendMessage":
		return true
	}
	return takesInteractiveFocus(method)
}

// replyToCall sends the result of a call its handler returned.
func (h *Host) replyToCall(me *managedExt, conn *Conn, callID string, call *CallPayload, callCtx context.Context, result *CallResultPayload, err error) {
	if publishesStateBeforeResult(call.Method) && err == nil {
		// Pi's model changes and dialogs expose current state before their Promise settles, including cancellation and dialog errors returned in the payload.
		// upstream: agent-session.ts:3406-3411 (appendEntry): the entry is in the session log when appendEntry returns, so the caller's subscribed session mirror receives it before the call result.
		err = h.pushStateTo(callCtx, me, conn)
	}
	if call.Method == "abort" && err == nil {
		// upstream: agent.ts:340-342 aborts the active run's controller before abort() returns, so a handler that holds ctx.signal sees it aborted once its abort call returns. The run's cancellation also starts the watcher that forwards the abort (runSignals.frame), and that frame can reach the connection after this reply; the frame sent here precedes it on the connection's one ordered stream, and the watcher's frame then finds the state sent.
		_ = h.syncRunSignal(conn)
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
	// The call carries the declaration the register payload does: the operations (streamSimple, images, classifiers) run in the extension and are named, never sent.
	var request ProviderDecl
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
	// Pi's registry keys a provider by its exact name (a Map), so only the empty name is refused, as lastRegistrationWins does for the register payload.
	if request.Name == "" {
		h.xref.unhold(conn, callID)
		return nil, errors.New("provider name must not be empty")
	}
	if call.Method == "unregisterProvider" {
		h.mu.Lock()
		if me.shuttingDown.Load() {
			h.mu.Unlock()
			return nil, errors.New("extension was replaced")
		}
		released, retired := h.retireNativeProviderLocked(me, conn, request.Name)
		// Pi's unregisterProvider removes the one effective registration whoever registered it (model-runtime.ts:791-797), so the caller takes every claim, including a bridged OAuth login, before releasing them.
		notifySuperseded := h.transferProviderOwnershipLocked(me, conn, request.Name, retired...)
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
	h.attachProviderOperations(me, request, &config)
	if err := h.providerRuntime.RegisterProvider(request.Name, config, extConfigOrigin(me.config)); err != nil {
		h.xref.unhold(conn, callID)
		return nil, err
	}
	h.mu.Lock()
	released, retired := h.retireNativeProviderLocked(me, conn, request.Name)
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	h.mu.Lock()
	if me.shuttingDown.Load() {
		h.mu.Unlock()
		h.xref.unhold(conn, callID)
		return nil, errors.New("extension was replaced")
	}
	registeredOAuth := slices.Contains(me.oauthProviderNames, request.Name)
	notifySuperseded := h.transferProviderOwnershipLocked(me, conn, request.Name, retired...)
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

// handleUnboundExec runs pi.exec for a call that arrives before a session binds the exec action, which is while a factory loads. Pi's pi.exec needs no session: it runs the command in the loader's working directory (core/extensions/loader.ts createExtensionAPI exec).
func (h *Host) handleUnboundExec(ctx context.Context, call *CallPayload) (*CallResultPayload, error) {
	var p ExecArgs
	if err := json.Unmarshal(call.Args, &p); err != nil {
		return nil, fmt.Errorf("parse exec args: %w", err)
	}
	// ExecCommand reports the call initiated once its spawn was attempted, as the bound exec action does.
	res, err := extension.ExecCommand(ctx, h.cwd, p.Command, p.Args, p.Options)
	if err != nil {
		return nil, err
	}
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}
