package subprocess

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

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
	case "ui.select", "ui.confirm", "ui.input", "ui.editor", CallUICustom,
		"setModel", "exec", "complete", "modelStream", "getModelAuth", "compact",
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
}

func newCallLanes() *callLanes {
	return &callLanes{lanes: make(map[string]*callLane)}
}

// push queues run behind every earlier call of the same lane.
func (l *callLanes) push(lane string, run func()) {
	l.mu.Lock()
	current := l.lanes[lane]
	if current != nil {
		current.queue = append(current.queue, run)
		l.mu.Unlock()
		return
	}
	current = &callLane{queue: []func(){run}}
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

// queueCall orders one extension→host call. A Promise-shaped call runs on its
// own goroutine, and the lane advances once the call has applied its
// synchronous part (it marks initiation) or has finished, whichever is first.
// The UI bridge marks initiation; a bare call handler has no initiation point,
// so its call finishes before the lane advances.
func (h *Host) queueCall(me *managedExt, lanes *callLanes, callID string, call *CallPayload) {
	lanes.push(call.ParentRequestID, func() {
		if !startsAsync(call.Method) {
			h.runCall(me, callID, call, nil)
			return
		}
		initiated := make(chan struct{})
		var once sync.Once
		mark := func() { once.Do(func() { close(initiated) }) }
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer mark()
			h.runCall(me, callID, call, mark)
		}()
		select {
		case <-initiated:
		case <-finished:
		}
	})
}

// runCall executes one extension→host call and sends its result. mark, when
// non-nil, is the call's initiation mark.
func (h *Host) runCall(me *managedExt, callID string, call *CallPayload, mark func()) {
	callCtx, releaseCall := me.conn.hostCallContext(call.ParentRequestID, callID)
	defer releaseCall()
	if mark != nil {
		callCtx = extension.WithCallInitiation(callCtx, mark)
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
				_ = me.conn.Send(&Envelope{
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
	case call.Method == "event.subscribe" || call.Method == "event.unsubscribe":
		result, err = h.handleEventSubscription(me, call)
	case strings.HasPrefix(call.Method, "oauth.cb."):
		result, err = h.handleOAuthCallback(callCtx, me.config.Name, call)
	case h.uiBridge != nil:
		result, err = h.uiBridge.handleCall(callCtx, me.config.Name, me.conn, call)
	case h.onCall != nil:
		result, err = h.onCall(me.config.Name, call)
	}
	if callID == "" {
		return
	}
	if err != nil {
		_ = me.conn.Send(&Envelope{
			Type:       MsgCallResult,
			ID:         callID,
			CallResult: &CallResultPayload{Error: &ErrorInfo{Message: err.Error()}},
		})
		return
	}
	if result == nil {
		return
	}
	sendErr := me.conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: result})
	if tooLarge, ok := errors.AsType[*FrameTooLargeError](sendErr); ok {
		// No compliant extension can read a frame above MaxFrameSize; return a
		// small error result instead of writing an undeliverable frame that
		// would silently kill the extension.
		fmt.Fprintf(os.Stderr, "extension %s call %s: result of %d bytes exceeds the %d byte IPC frame limit; returning an error\n",
			me.config.Name, call.Method, tooLarge.Size, tooLarge.Max)
		_ = me.conn.Send(&Envelope{
			Type: MsgCallResult,
			ID:   callID,
			CallResult: &CallResultPayload{Error: &ErrorInfo{
				Code:    "result_too_large",
				Message: fmt.Sprintf("result of %d bytes exceeds the %d byte IPC frame limit", tooLarge.Size, tooLarge.Max),
			}},
		})
	}
}
