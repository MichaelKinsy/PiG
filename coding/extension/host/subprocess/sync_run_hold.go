package subprocess

import (
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi's pi.sendMessage with triggerTurn awaits _startRun, which sets _isAgentRunActive before its first await (agent-session.ts:2404, 1895), and
// Pi's one event loop runs nothing else until the code that sent the message awaits or returns. Until then ctx.isIdle() is false in every
// extension, whatever the run the message started would do; Pi throws before it sets busy for every error the host can answer to a send (the
// runtime not bound, the Session not created, the runner replaced by a reload), so a send the host fails holds nothing, and a run that fails
// after the Session is busy holds like any other. The host runs the run concurrently with the extensions, so a run that settles while a read
// is in flight would answer idle. The host therefore answers isIdle busy, for every extension, from the triggering send until the code that
// sent it yields:
//   - A runtime with an event loop (Node) reports it (NotifyHandlerYielded): its first microtask checkpoint after the send, for a handler and
//     for a timer's or an I/O callback alike. The send carries the token the report names and the host sets the hold before it answers the send,
//     which the runtime waits for, so the report can only follow the hold.
//   - A thread-based SDK's handler yields when it returns, makes a call whose upstream API returns a Promise, or calls the SDK's yield helper
//     (ctx.Yield, and ctx.Sleep, which yields first), which stands for the `await` Pi's code writes there (returnsPromiseInPi). A handler that
//     blocks on something else, such as a raw sleep, has not yielded in Pi's terms either: it keeps the hold until it returns, as a synchronous
//     wait does in Pi. The thread-based SDKs have no report for a send from a thread a handler started, which holds nothing.

// syncRunHolds are the triggering sends whose code has not yet yielded.
type syncRunHolds struct {
	mu   sync.Mutex
	held map[syncRunKey]struct{}
}

// syncRunKey names one hold: a thread-based SDK's by the request whose handler sent the message, a runtime's with an event loop by the token the
// send carried.
type syncRunKey struct {
	conn    *Conn
	request string
	token   string
}

func (s *syncRunHolds) hold(key syncRunKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = make(map[syncRunKey]struct{})
	}
	s.held[key] = struct{}{}
}

// endRequest ends the holds of the handler of request.
func (s *syncRunHolds) endRequest(conn *Conn, request string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, syncRunKey{conn: conn, request: request})
}

// endToken ends the hold of the send that carried token.
func (s *syncRunHolds) endToken(conn *Conn, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, syncRunKey{conn: conn, token: token})
}

// closed drops the holds of a connection that ended: nothing yields on it any more.
func (s *syncRunHolds) closed(conn *Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.held {
		if key.conn == conn {
			delete(s.held, key)
		}
	}
}

// busy reports whether a send's code still runs its synchronous part. A request's hold ends with the request.
func (s *syncRunHolds) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.held {
		if key.token != "" {
			return true
		}
		if _, inFlight := key.conn.requestEvent(key.request); inFlight {
			return true
		}
		delete(s.held, key)
	}
	return false
}

// pig divergence (D225): a thread-based SDK's handler that waits for the run with a raw sleep and no yield helper keeps the hold until it returns,
// and a send from a thread it started after its request returned holds nothing.
//
// holdAfterSend holds the Session busy for the code that made the sendMessage call. A message sent while the agent_settled handlers run is
// held back by the Session (agent-session.ts:2344-2349), which stays idle, so the Session's own answer stands there; so does a message that
// does not start or join a run.
func (h *Host) holdAfterSend(conn *Conn, call *CallPayload) {
	var args struct {
		Options    SendMessageOptions `json:"options"`
		YieldToken string             `json:"yield_token"`
	}
	if json.Unmarshal(call.Args, &args) != nil {
		return
	}
	if args.Options.TriggerTurn == nil || !*args.Options.TriggerTurn || args.Options.DeliverAs == string(extension.DeliverAsNextTurn) {
		return
	}
	var event string
	var inFlight bool
	if call.ParentRequestID != "" {
		event, inFlight = conn.requestEvent(call.ParentRequestID)
	}
	switch {
	case event == "agent_settled":
		// The Session holds the message back and stays idle.
	case args.YieldToken != "":
		h.syncRuns.hold(syncRunKey{conn: conn, token: args.YieldToken})
	case inFlight:
		h.syncRuns.hold(syncRunKey{conn: conn, request: call.ParentRequestID})
	}
}

// returnsPromiseInPi reports whether the code that calls method awaits in Pi: the upstream API behind it returns a Promise, or it is the explicit yield of a thread-based SDK (CallYield). Most calls that run
// off their lane (startsAsync) do; registerProvider and addAutocompleteProvider return nothing there and only run so because the host's work
// behind them can wait for the session or a callback.
func returnsPromiseInPi(method string) bool {
	switch method {
	case CallYield:
		return true
	case "registerProvider", "ui.addAutocompleteProvider":
		return false
	}
	return startsAsync(method)
}

// pig divergence (D225): only a call Pi awaits (returnsPromiseInPi) or the SDK's explicit yield ends a thread-based handler's hold; a raw sleep does not.
//
// endSyncRunHold ends the hold of the thread-based SDK's handler that made call when call awaits in Pi. A runtime with an event loop reports its
// own yields.
func (h *Host) endSyncRunHold(conn *Conn, call *CallPayload) {
	if call.ParentRequestID != "" && returnsPromiseInPi(call.Method) {
		h.syncRuns.endRequest(conn, call.ParentRequestID)
	}
}

// endSyncRunHoldOf ends the hold of the send whose token a runtime's NotifyHandlerYielded names.
func (h *Host) endSyncRunHoldOf(conn *Conn, args json.RawMessage) {
	var payload HandlerYieldedPayload
	if json.Unmarshal(args, &payload) == nil && payload.Token != "" {
		h.syncRuns.endToken(conn, payload.Token)
	}
}

// busyIsIdleResult is the isIdle answer while the code that sent a triggering message has not yielded.
func busyIsIdleResult() *CallResultPayload {
	return &CallResultPayload{Result: json.RawMessage(`{"idle":false}`)}
}
