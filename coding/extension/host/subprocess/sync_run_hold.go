package subprocess

import (
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi's pi.sendMessage with triggerTurn awaits _startRun, which sets _isAgentRunActive before its first await (agent-session.ts:2404, 1895), and
// Pi's one event loop runs nothing else until the handler that sent the message awaits or returns. Until then ctx.isIdle() is false in every
// extension, whatever the run the message started would do. The host runs that run concurrently with the extensions, so a run that settles
// while a read is in flight would answer idle. The host therefore answers isIdle busy, for every extension, from the triggering send until the
// handler that sent it yields: it returns, or makes a call whose upstream API returns a Promise (startsAsync), or its runtime reports that the
// code awaited (NotifyHandlerYielded, from a runtime with an event loop). A handler of a thread-based SDK that blocks on something other than
// such a call does not yield; it waits for the run with ctx.waitForIdle().

// syncRunHolds are the handlers whose triggering send has not yet been followed by a yield.
type syncRunHolds struct {
	mu   sync.Mutex
	held map[syncRunKey]struct{}
}

type syncRunKey struct {
	conn    *Conn
	request string
}

func (s *syncRunHolds) hold(conn *Conn, request string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = make(map[syncRunKey]struct{})
	}
	s.held[syncRunKey{conn, request}] = struct{}{}
}

func (s *syncRunHolds) end(conn *Conn, request string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, syncRunKey{conn, request})
}

// busy reports whether a handler that sent a triggering message still runs its synchronous part. A hold ends with its request.
func (s *syncRunHolds) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.held {
		if _, inFlight := key.conn.requestEvent(key.request); inFlight {
			return true
		}
		delete(s.held, key)
	}
	return false
}

// holdAfterSend holds the Session busy for the handler that made the sendMessage call. A message sent while the agent_settled handlers run is
// held back by the Session (agent-session.ts:2344-2349), which stays idle, so the Session's own answer stands there; so does a message that
// does not start or join a run.
func (h *Host) holdAfterSend(conn *Conn, call *CallPayload) {
	if call.ParentRequestID == "" {
		return
	}
	var args struct {
		Options SendMessageOptions `json:"options"`
	}
	if json.Unmarshal(call.Args, &args) != nil {
		return
	}
	if args.Options.TriggerTurn == nil || !*args.Options.TriggerTurn || args.Options.DeliverAs == string(extension.DeliverAsNextTurn) {
		return
	}
	if event, inFlight := conn.requestEvent(call.ParentRequestID); !inFlight || event == "agent_settled" {
		return
	}
	h.syncRuns.hold(conn, call.ParentRequestID)
}

// endSyncRunHold ends the hold of the request that made call when call awaits in Pi.
func (h *Host) endSyncRunHold(conn *Conn, call *CallPayload) {
	if call.ParentRequestID != "" && startsAsync(call.Method) {
		h.syncRuns.end(conn, call.ParentRequestID)
	}
}

// endSyncRunHoldOf ends the hold of the request a runtime's NotifyHandlerYielded names.
func (h *Host) endSyncRunHoldOf(conn *Conn, args json.RawMessage) {
	var payload HandlerYieldedPayload
	if json.Unmarshal(args, &payload) == nil {
		h.syncRuns.end(conn, payload.RequestID)
	}
}

// busyIsIdleResult is the isIdle answer while a handler's synchronous part holds the Session busy.
func busyIsIdleResult() *CallResultPayload {
	result, _ := json.Marshal(map[string]any{"idle": false})
	return &CallResultPayload{Result: result}
}
