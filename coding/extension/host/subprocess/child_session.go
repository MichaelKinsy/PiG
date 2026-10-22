package subprocess

// Ports packages/coding-agent/src/core/sdk.ts createAgentSession for the Go, Python and Rust SDKs, with the lifetime the Node runtime gives Pi's own implementation (runtime-node/shims/independent-session.mjs, independent-session-owner.mjs): an independent Session belongs to its extension connection, not to the command that created it, and it ends with the connection.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ChildSession is a child agent session the host owns for one extension connection. It is the part of Pi's AgentSession an extension reaches over the wire; the process that builds sessions (the coding package's CreateAgentSession) implements it.
//
// Prompt, Queue, Abort and BindExtensions are awaited as Pi's Promises are: each returns when its work settled, and ctx ends it early. Subscribe's listener runs synchronously with the session's emission, in order, as AgentSession.subscribe's does.
type ChildSession interface {
	// Info is what the session shows right after creation.
	Info() ChildSessionInfo
	// Prompt is AgentSession.prompt. It returns once the run settled; a cancelled ctx aborts the run.
	Prompt(ctx context.Context, prompt ChildSessionPrompt) error
	// Queue is AgentSession.steer or followUp.
	Queue(ctx context.Context, queue ChildSessionQueue) error
	// Abort is AgentSession.abort: it aborts the run and returns once the session is idle.
	Abort(ctx context.Context) error
	// BindExtensions is AgentSession.bindExtensions({}).
	BindExtensions(ctx context.Context) error
	// State is the session's current state.
	State() ChildSessionState
	// Messages is AgentSession.messages in Pi's JSON shape.
	Messages() (json.RawMessage, error)
	// Subscribe is AgentSession.subscribe; event is one AgentSessionEvent in Pi's JSON shape, valid only during the call.
	Subscribe(listener func(event json.RawMessage)) (unsubscribe func())
	// Dispose is AgentSession.dispose: it disconnects the session from its agent and drops the listeners. As in Pi, the session still answers a prompt afterwards (the agent runs; no listener sees it). It never fails.
	Dispose()
	// Close ends the run and releases the session and everything it started. The host calls it when the connection closes, or when a session whose creation was abandoned never became live.
	Close()
}

// ChildSessionRequest is a decoded CallChildSessionCreate: the wire arguments, and the definitions of CustomTools whose execute runs in the extension.
type ChildSessionRequest struct {
	ChildSessionCreate
	// ToolDefinitions are the extension's customTools, in order. Their Execute asks the extension that created the session.
	ToolDefinitions []extension.ToolDefinition
	// SessionManager, SettingsManager, ResourceLoader and ModelRuntime are the session graph objects the create call named (nil when it named none), resolved on the creating connection.
	SessionManager, SettingsManager, ResourceLoader, ModelRuntime SessionGraphObject
}

// ChildSessionFactory builds a child session. ctx ends the creation only: the session outlives it and ends with Dispose.
type ChildSessionFactory func(ctx context.Context, request ChildSessionRequest) (ChildSession, error)

// childSessionTable holds the sessions of every connection.
type childSessionTable struct {
	mu    sync.Mutex
	conns map[*Conn]*connChildSessions
	// closing counts the connections whose sessions are being closed, so closeAll can wait for a close a connection's watcher began. Add runs under mu, when the connection leaves conns.
	closing sync.WaitGroup
}

// connChildSessions is one connection's sessions. A session being created holds its id with a nil session until the factory returns.
type connChildSessions struct {
	sessions map[string]*childSessionEntry
	closed   bool
}

// childSessionEntry is one session and the events it forwards.
type childSessionEntry struct {
	conn    *Conn
	session ChildSession
	// prefix is `{"id":<id>,"seq":`, the part of every event frame that does not change.
	prefix      []byte
	unsubscribe func()

	// eventMu orders the numbering with the write: the frame of event n is queued before the frame of event n+1.
	eventMu sync.Mutex
	seq     atomic.Uint64
	// updates is the message_update payload the extension last received (child_session_delta.go).
	updates childUpdateStream
	// disposed stops the forwarding of events once the session is disposed.
	disposed atomic.Bool
	// unhold ends the session's hold on the session graph objects it was created over (resolveChildSessionObjects); nil when it named none.
	unhold func()
}

// forward sends one event to the extension, numbered in emission order. The frame is queued before the listener returns, so it precedes every frame the host writes after the event, including the result of the prompt that ran it.
//
//perf:hot
func (e *childSessionEntry) forward(event json.RawMessage) {
	if e.disposed.Load() {
		return
	}
	e.eventMu.Lock()
	defer e.eventMu.Unlock()
	seq := e.seq.Add(1)
	var args []byte
	if bytes.HasPrefix(event, messageUpdatePrefix) {
		if base, ops, patch, ok := e.updates.frame(event, seq); ok {
			args = e.updateFrame(seq, base, ops, patch, event)
		}
	}
	if args == nil {
		args = make([]byte, 0, len(e.prefix)+20+9+len(event)+1)
		args = append(args, e.prefix...)
		args = strconv.AppendUint(args, seq, 10)
		args = append(args, `,"event":`...)
		args = append(args, event...)
		args = append(args, '}')
	}
	// The connection is gone when Send fails; its close disposes the session.
	_ = e.conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyChildSessionEvent, Args: args}})
}

// updateFrame is the notification of a message_update that travels in the stream: a keyframe carries the event, a patch the operations that turn the payload numbered base into it.
func (e *childSessionEntry) updateFrame(seq, base uint64, ops []byte, patch bool, event []byte) []byte {
	size := len(e.prefix) + 80 + len(ops)
	if !patch {
		size += len(event)
	}
	args := make([]byte, 0, size)
	args = append(args, e.prefix...)
	args = strconv.AppendUint(args, seq, 10)
	if !patch {
		args = append(args, `,"event":`...)
		args = append(args, event...)
		args = append(args, `,"stream":{"seq":`...)
	} else {
		args = append(args, `,"stream":{"seq":`...)
	}
	args = strconv.AppendUint(args, seq, 10)
	if patch {
		args = append(args, `,"base":`...)
		args = strconv.AppendUint(args, base, 10)
		args = append(args, `,"ops":[`...)
		args = append(args, ops...)
		args = append(args, ']')
	}
	return append(args, '}', '}')
}

// childSessionFactory is the host's factory, or nil when no mode bound one.
func (h *Host) childSessionFactory() ChildSessionFactory {
	if h.uiBridge == nil {
		return nil
	}
	if actions := h.uiBridge.hostCallbacks(); actions != nil {
		return actions.CreateChildSession
	}
	return nil
}

// reserve claims id for conn while its session is created, and starts the watch that disposes the connection's sessions when it closes.
func (t *childSessionTable) reserve(h *Host, conn *Conn, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	owned := t.conns[conn]
	if owned == nil {
		if conn.closed.Load() {
			return errors.New(ChildSessionClosedMessage)
		}
		if t.conns == nil {
			t.conns = map[*Conn]*connChildSessions{}
		}
		owned = &connChildSessions{sessions: map[string]*childSessionEntry{}}
		t.conns[conn] = owned
		go func() {
			<-conn.Done()
			h.childSessions.closeConn(conn)
		}()
	}
	if owned.closed {
		return errors.New(ChildSessionClosedMessage)
	}
	if _, taken := owned.sessions[id]; taken {
		return fmt.Errorf("Child session %q already exists", id)
	}
	owned.sessions[id] = nil
	return nil
}

// release drops a reservation whose session was not created.
func (t *childSessionTable) release(conn *Conn, id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if owned := t.conns[conn]; owned != nil && owned.sessions[id] == nil {
		delete(owned.sessions, id)
	}
}

// publish makes the session of a reservation live. It reports false when the connection closed meanwhile, and the caller disposes the session.
func (t *childSessionTable) publish(conn *Conn, id string, entry *childSessionEntry) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	owned := t.conns[conn]
	if owned == nil || owned.closed {
		return false
	}
	owned.sessions[id] = entry
	return true
}

// lookup returns the live session id names.
func (t *childSessionTable) lookup(conn *Conn, id string) *childSessionEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if owned := t.conns[conn]; owned != nil {
		return owned.sessions[id]
	}
	return nil
}

// remove takes the live session id names out of the table.
func (t *childSessionTable) remove(conn *Conn, id string) *childSessionEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	owned := t.conns[conn]
	if owned == nil {
		return nil
	}
	entry := owned.sessions[id]
	delete(owned.sessions, id)
	return entry
}

// closeConn disposes every session of a closed connection, as Pi's disposeIndependentSessions does: a running prompt ends and its tools are aborted.
func (t *childSessionTable) closeConn(conn *Conn) {
	t.mu.Lock()
	owned := t.conns[conn]
	delete(t.conns, conn)
	entries := t.sealLocked(owned)
	t.mu.Unlock()
	t.closeEntries(entries, owned != nil)
}

// closeAll disposes the sessions of every connection and returns when none is still closing, including a close a connection's watcher began. Host.Shutdown calls it: a session outlives the extension's own dispose, so the processes it started end only when it closes.
func (t *childSessionTable) closeAll() {
	t.mu.Lock()
	var all [][]*childSessionEntry
	for conn, owned := range t.conns {
		delete(t.conns, conn)
		all = append(all, t.sealLocked(owned))
	}
	t.mu.Unlock()
	for _, entries := range all {
		t.closeEntries(entries, true)
	}
	t.closing.Wait()
}

// sealLocked refuses any later session of owned, counts its close in t.closing, and returns the live sessions. closeEntries ends the count.
func (t *childSessionTable) sealLocked(owned *connChildSessions) []*childSessionEntry {
	if owned == nil {
		return nil
	}
	owned.closed = true
	t.closing.Add(1)
	var entries []*childSessionEntry
	for _, entry := range owned.sessions {
		if entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// closeEntries closes the sessions sealLocked returned; counted says it counted the close.
func (t *childSessionTable) closeEntries(entries []*childSessionEntry, counted bool) {
	for _, entry := range entries {
		entry.close()
	}
	if counted {
		t.closing.Done()
	}
}

// dispose ends the session: no later event is forwarded.
func (e *childSessionEntry) dispose() {
	if !e.disposed.CompareAndSwap(false, true) {
		return
	}
	e.unsubscribe()
	e.session.Dispose()
}

// close disposes the session if it was not, and releases it. The objects it was created over are let go last: the session uses them until it is closed, even after the extension released their handles.
func (e *childSessionEntry) close() {
	e.dispose()
	e.session.Close()
	if e.unhold != nil {
		e.unhold()
	}
}

// handleChildSessionCall serves the childSession.* calls.
func (h *Host) handleChildSessionCall(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	if call.Method == CallChildSessionCreate {
		return h.createChildSession(ctx, conn, call)
	}
	var ref ChildSessionRef
	if err := json.Unmarshal(call.Args, &ref); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	if call.Method == CallChildSessionDispose {
		// Disposing a session that is gone is not an error: Pi's dispose is idempotent.
		if entry := h.childSessions.lookup(conn, ref.ID); entry != nil {
			entry.dispose()
		}
		return &CallResultPayload{}, nil
	}
	entry := h.childSessions.lookup(conn, ref.ID)
	if entry == nil {
		return nil, errors.New(ChildSessionUnknownMessage)
	}
	session := entry.session
	switch call.Method {
	case CallChildSessionPrompt:
		var prompt ChildSessionPrompt
		if err := json.Unmarshal(call.Args, &prompt); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		// The call's lane advances here: an abort the extension sends next must not wait for the run.
		extension.CallInitiated(ctx)
		if err := session.Prompt(ctx, prompt); err != nil {
			return nil, err
		}
		result, err := json.Marshal(ChildSessionPromptResult{Events: entry.seq.Load()})
		return &CallResultPayload{Result: result}, err
	case CallChildSessionQueue:
		var queue ChildSessionQueue
		if err := json.Unmarshal(call.Args, &queue); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		extension.CallInitiated(ctx)
		if err := session.Queue(ctx, queue); err != nil {
			return nil, err
		}
		return &CallResultPayload{}, nil
	case CallChildSessionAbort:
		extension.CallInitiated(ctx)
		if err := session.Abort(ctx); err != nil {
			return nil, err
		}
		return &CallResultPayload{}, nil
	case CallChildSessionBindExtensions:
		extension.CallInitiated(ctx)
		if err := session.BindExtensions(ctx); err != nil {
			return nil, err
		}
		return &CallResultPayload{}, nil
	case CallChildSessionState:
		result, err := json.Marshal(session.State())
		return &CallResultPayload{Result: result}, err
	case CallChildSessionMessages:
		messages, err := session.Messages()
		if err != nil {
			return nil, err
		}
		return &CallResultPayload{Result: messages}, nil
	default:
		return nil, fmt.Errorf("unknown child session call %s", call.Method)
	}
}

// createChildSession serves CallChildSessionCreate.
func (h *Host) createChildSession(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var create ChildSessionCreate
	if err := json.Unmarshal(call.Args, &create); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	if create.ID == "" {
		return nil, errors.New("child session id is required")
	}
	factory := h.childSessionFactory()
	if factory == nil {
		return nil, errors.New("child agent sessions are not available in this host")
	}
	tools, err := h.childSessionTools(conn, create)
	if err != nil {
		return nil, err
	}
	if err := h.childSessions.reserve(h, conn, create.ID); err != nil {
		return nil, err
	}
	// Creation loads resources, which can take as long as a prompt: the call's lane advances.
	extension.CallInitiated(ctx)
	request := ChildSessionRequest{ChildSessionCreate: create, ToolDefinitions: tools}
	unhold, err := h.resolveChildSessionObjects(conn, &request)
	if err != nil {
		h.childSessions.release(conn, create.ID)
		return nil, err
	}
	session, err := factory(ctx, request)
	if err != nil {
		unhold()
		h.childSessions.release(conn, create.ID)
		return nil, err
	}
	idJSON, _ := json.Marshal(create.ID)
	entry := &childSessionEntry{conn: conn, session: session, unhold: unhold, prefix: append(append([]byte(`{"id":`), idJSON...), `,"seq":`...)}
	entry.unsubscribe = session.Subscribe(entry.forward)
	if !h.childSessions.publish(conn, create.ID, entry) {
		entry.close()
		return nil, errors.New(ChildSessionClosedWhileCreatingMessage)
	}
	result, err := json.Marshal(session.Info())
	return &CallResultPayload{Result: result}, err
}

// childSessionTools builds the definitions of a session's customTools: each one's execute is a tool_call request to the extension that names the session.
func (h *Host) childSessionTools(conn *Conn, create ChildSessionCreate) ([]extension.ToolDefinition, error) {
	if len(create.CustomTools) == 0 {
		return nil, nil
	}
	// upstream: loader.ts registerTool validates the schema, so an invalid customTool fails the creation.
	if err := validateRegisterPayload(conn.Name(), &RegisterPayload{Name: conn.Name(), Tools: create.CustomTools}); err != nil {
		return nil, err
	}
	target := toolTarget{connection: func() *Conn { return conn }, childSession: create.ID}
	definitions := make([]extension.ToolDefinition, 0, len(create.CustomTools))
	for _, tool := range create.CustomTools {
		definition := extension.ToolDefinition{
			Name:                 tool.Name,
			Label:                tool.Label,
			Description:          tool.Description,
			Parameters:           tool.Parameters,
			ValidationParameters: tool.ValidationParameters,
			ConstrainedSampling:  tool.ConstrainedSampling,
			PromptSnippet:        tool.PromptSnippet,
			PromptGuidelines:     tool.PromptGuidelines,
			ExecutionMode:        extension.ToolExecutionMode(tool.ExecutionMode),
			Execute:              h.toolExecuteFunc(tool.Name, target),
			OutputSchema:         tool.OutputSchema,
			Exposure:             extension.ToolExposure(tool.Exposure),
			Namespace:            tool.Namespace,
			Annotations:          tool.Annotations,
			DefaultActive:        tool.DefaultActive,
			ReserveCallOrder: func(json.RawMessage) *extension.CallOrder {
				place := conn.toolOrder.Reserve()
				return &extension.CallOrder{Wait: place.Wait, Release: place.Release}
			},
		}
		if tool.PreparesArguments {
			definition.PrepareArguments = childToolPrepareArguments(conn, create.ID, tool.Name)
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// childToolPrepareArguments is makeToolPrepareArguments for a child session's tool: the extension runs the hook the session's definition named.
func childToolPrepareArguments(conn *Conn, sessionID, toolName string) extension.ToolPrepareArgumentsFunc {
	return func(args json.RawMessage) (json.RawMessage, error) {
		//portlint:allow ctxbackground Pi's prepareArguments is synchronous and takes no signal; the connection's close ends the request
		resp, err := conn.Request(context.Background(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestPrepareArguments, Tool: toolName, Args: args, ChildSession: sessionID}})
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", toolName, err)
		}
		if resp.Response == nil {
			return nil, nil
		}
		if resp.Response.Error != nil {
			return nil, resp.Response.Error.ToError()
		}
		return resp.Response.Result, nil
	}
}

// resolveChildSessionObjects looks up the session graph objects a create call names. An object replaces the option that would build its own. The session holds each object it takes until it closes: the returned function ends those holds, and the caller calls it when the creation fails or the session closes. On an error nothing is held.
func (h *Host) resolveChildSessionObjects(conn *Conn, request *ChildSessionRequest) (func(), error) {
	var holds []func()
	unhold := func() {
		for _, release := range holds {
			release()
		}
	}
	create := request.ChildSessionCreate
	for _, step := range []struct {
		option, handle string
		into           *SessionGraphObject
	}{
		{"sessionManager", create.SessionManager, &request.SessionManager},
		{"settingsManager", create.SettingsManager, &request.SettingsManager},
		{"resourceLoader", create.ResourceLoader, &request.ResourceLoader},
		{"modelRuntime", create.ModelRuntime, &request.ModelRuntime},
	} {
		if step.handle == "" {
			continue
		}
		object, release, ok := h.sessionGraphObjects.retain(conn, step.handle)
		if !ok {
			unhold()
			return nil, fmt.Errorf("option %s: %s", step.option, SessionGraphUnknownMessage)
		}
		holds = append(holds, release)
		*step.into = object
	}
	if create.SessionManager != "" && create.InMemory {
		unhold()
		return nil, errors.New("options sessionManager and inMemory both name the session log")
	}
	if create.SettingsManager != "" && len(create.Settings) != 0 {
		unhold()
		return nil, errors.New("options settingsManager and settings both name the settings")
	}
	if create.ResourceLoader != "" && create.Resources != nil {
		unhold()
		return nil, errors.New("options resourceLoader and resources both name the resource loader")
	}
	return unhold, nil
}
