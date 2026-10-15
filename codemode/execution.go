//go:build !tinygo

package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/ecmascript"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type pendingCall struct {
	record    *Call
	startedAt time.Time
	cancel    context.CancelFunc
}

// execution is the host side of one script run: the tool table, call records, output, the deadline, the caller's
// context and the decoding of what the VM reports. Where the VM runs is the launcher's business. It ends exactly once, in
// finish: the script's done message, a VM failure, the deadline, the caller's context, or Close. The goroutine that
// called run owns the execution; the timer, the context watcher and Close only record why it ended.
//
// Ports packages/codemode/src/runtime/execution.ts (Execution).
type execution struct {
	sandbox   *Sandbox
	code      string
	timeoutMs float64
	store     map[string]json.RawMessage
	tools     map[string]Tool
	toolOrder []Tool
	globals   map[string]Tool
	globalOrd []Tool
	// callCtx carries the caller's values to the tools; each call gets a child that finish cancels.
	callCtx context.Context

	done   chan struct{}
	exited chan struct{}
	calls  sync.WaitGroup
	// turn is vmEvents.turn.
	turn sync.Mutex
	// deadline arms the deadline once.
	deadline sync.Once

	mu       sync.Mutex
	finished bool
	timer    *time.Timer
	stopCtx  func() bool
	// channel is the launcher's handle, and unsent holds the replies produced before the launcher returned it. flushing
	// reports that those replies are still being sent, so a later reply queues behind them.
	channel  vmChannel
	unsent   []callResult
	flushing bool
	output   []OutputItem
	records  []*Call
	pending  map[int]*pendingCall
	result   Result

	// A Start run: the VM is deterministic and every call goes to control, whose OnCall the caller answers with deliver. start
	// lists the tools and globals the script sees; the tables above stay empty.
	deterministic *VmDeterministic
	control       *RunOptions
	startTools    []VmStartTool
	startGlobals  []VmStartGlobal
	// delivered holds the result numbers deliver used; during is what the VM reported when the script finished.
	delivered map[int]bool
	during    *During
}

func newExecution(s *Sandbox, code string, timeoutMs float64, store map[string]json.RawMessage, tools, globals []Tool) *execution {
	e := &execution{
		sandbox: s, code: code, timeoutMs: timeoutMs, store: store,
		tools: map[string]Tool{}, toolOrder: tools, globals: map[string]Tool{}, globalOrd: globals,
		done: make(chan struct{}), exited: make(chan struct{}),
		pending: map[int]*pendingCall{},
	}
	for _, t := range tools {
		e.tools[t.Name] = t
	}
	for _, g := range globals {
		e.globals[g.Name] = g
	}
	return e
}

func (e *execution) isFinished() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.finished
}

// abort ends the execution as aborted and waits for it to exit.
func (e *execution) abort(message string) {
	e.finishBetweenTurns(&Error{Kind: ErrorAborted, Message: message})
	<-e.exited
}

// finishBetweenTurns ends the execution for a reason that does not come from the VM: the deadline, the caller's context or
// Close. It waits until an in-thread VM's turn is over.
func (e *execution) finishBetweenTurns(failure *Error) {
	e.turn.Lock()
	defer e.turn.Unlock()
	e.finish(failure, nil, StoreWrites{})
}

func (e *execution) run(ctx context.Context) Result {
	defer close(e.exited)
	e.callCtx = context.WithoutCancel(ctx)

	if ctx.Err() != nil {
		e.finish(&Error{Kind: ErrorAborted, Message: abortMessage(ctx)}, nil, StoreWrites{})
	} else {
		stop := context.AfterFunc(ctx, func() {
			e.finishBetweenTurns(&Error{Kind: ErrorAborted, Message: abortMessage(ctx)})
		})
		e.mu.Lock()
		e.stopCtx = stop
		e.mu.Unlock()
	}

	if !e.isFinished() {
		e.launch()
	}
	// An execution that ended before it had a channel still has none to stop.
	e.mu.Lock()
	channel := e.channel
	e.mu.Unlock()
	if channel != nil {
		<-e.done
		channel.stop()
	}
	// A VM that returned without a verdict (it cannot: the channel stops only after finish) still ends here.
	e.finish(&Error{Kind: ErrorSandbox, Message: "The VM exited before the script settled"}, nil, StoreWrites{})
	e.calls.Wait()
	e.mu.Lock()
	if e.timer != nil {
		e.timer.Stop()
	}
	e.mu.Unlock()
	return e.result
}

// launch asks the launcher for a VM and arms the deadline once it is ready. Compiling the module happens once per process and is
// not the script's time, so it does not count against the deadline (upstream's deadline also spans its much shorter wasm
// load, host.ts:147-151).
func (e *execution) launch() {
	data := VmStartData{
		Code:             e.code,
		Tools:            make([]VmStartTool, len(e.toolOrder)),
		Globals:          make([]VmStartGlobal, len(e.globalOrd)),
		MemoryLimitBytes: e.sandbox.options.MemoryLimitBytes,
		Store:            serializeStore(e.store),
	}
	for i, t := range e.toolOrder {
		data.Tools[i] = VmStartTool{t.Name, ToCodemodeIdentifier(t.Name), t.Description}
	}
	for i, g := range e.globalOrd {
		data.Globals[i] = VmStartGlobal{g.Name, g.Spread}
	}
	if e.control != nil {
		data.Tools, data.Globals, data.Deterministic = e.startTools, e.startGlobals, e.deterministic
	}
	channel, err := e.sandbox.launcher(e.callCtx, data, vmEvents{message: e.receive, ready: func() { e.deadline.Do(e.startDeadline) }, turn: &e.turn, failure: func(failure *Error) {
		e.finish(failure, nil, StoreWrites{})
	}})
	if err != nil {
		e.finish(&Error{Kind: ErrorSandbox, Message: err.Error()}, nil, StoreWrites{})
		return
	}
	e.mu.Lock()
	e.channel = channel
	e.flushing = len(e.unsent) > 0
	flushing := e.flushing
	finished := e.finished
	e.mu.Unlock()
	if !finished {
		e.deadline.Do(e.startDeadline)
	}
	// The channel starts before any reply is sent: a worker takes replies only once it runs.
	channel.start()
	// execution.ts sends the queued replies at once, before any later reply. A post here may wait until the VM takes the
	// reply, so they are sent off the goroutine that stops the channel: a VM that never takes them still meets its deadline.
	if flushing {
		e.calls.Go(e.flush)
	}
}

// flush sends the replies queued before the channel arrived, in order, and then those queued while it was sending.
func (e *execution) flush() {
	for {
		e.mu.Lock()
		unsent, channel := e.unsent, e.channel
		e.unsent = nil
		if len(unsent) == 0 {
			e.flushing = false
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		for _, reply := range unsent {
			channel.post(reply)
		}
	}
}

// post sends a nested call's result to the VM. Until the launcher has returned its channel and the replies queued
// before it are sent, the reply queues behind them.
func (e *execution) post(reply callResult) {
	e.mu.Lock()
	channel := e.channel
	queued := !e.finished && (channel == nil || e.flushing)
	if queued {
		e.unsent = append(e.unsent, reply)
	}
	e.mu.Unlock()
	if !queued && channel != nil {
		channel.post(reply)
	}
}

// startDeadline arms the timeout once the module is ready. As `Number.isFinite(options.timeoutMs)` gates it upstream, NaN and both infinities arm no
// deadline.
func (e *execution) startDeadline() {
	if !math.IsInf(e.timeoutMs, 0) && !math.IsNaN(e.timeoutMs) {
		timeout := time.Duration(min(e.timeoutMs*float64(time.Millisecond), float64(math.MaxInt64)))
		e.mu.Lock()
		e.timer = time.AfterFunc(timeout, func() {
			e.finishBetweenTurns(&Error{Kind: ErrorTimeout, Message: timeoutMessage(e.timeoutMs)})
		})
		e.mu.Unlock()
	}
}

// finish records the outcome; only the first call counts. It cancels every pending nested call and stops the VM.
//
// Ports packages/codemode/src/runtime/host.ts (Execution.finish).
func (e *execution) finish(failure *Error, value json.RawMessage, writes StoreWrites) {
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	e.finished = true
	if e.timer != nil {
		e.timer.Stop()
	}
	stopCtx := e.stopCtx
	now := time.Now()
	for _, p := range e.pending {
		if p.record != nil {
			p.record.DurationMs = millisSince(p.startedAt, now)
		}
		p.cancel()
	}
	e.pending = map[int]*pendingCall{}
	e.unsent = nil
	calls := make([]Call, len(e.records))
	for i, r := range e.records {
		calls[i] = *r
	}
	output := append([]OutputItem{}, e.output...)
	var during *During
	if e.during != nil {
		d := *e.during
		during = &d
	}
	if failure != nil {
		e.result = Result{Error: failure, Output: output, Calls: calls, During: during}
	} else {
		if writes.Set == nil {
			writes.Set = map[string]json.RawMessage{}
		}
		e.result = Result{OK: true, Value: value, Output: output, Calls: calls, StoreWrites: writes, During: during}
	}
	e.mu.Unlock()
	if stopCtx != nil {
		stopCtx()
	}
	close(e.done)
}

func millisSince(start, now time.Time) float64 {
	return float64(now.Sub(start)) / float64(time.Millisecond)
}

// bridgeError is a payload from the prelude that does not decode. The prelude runs in the same VM as the script, so a
// script that patches built-ins (for example Array.prototype.toJSON) could make it send malformed data; the execution
// then fails as a sandbox error.
//
// Ports packages/codemode/src/runtime/host.ts (BridgeError).
type bridgeError string

func (b bridgeError) Error() string { return string(b) }

// bridgeBroken is the sandbox error for a payload that does not decode.
func bridgeBroken(reason string) *Error {
	return &Error{Kind: ErrorSandbox, Message: "Sandbox bridge broken: " + reason + ". The script may have modified built-ins such as a prototype's toJSON."}
}

// parseBridgeJSON validates a JSON payload.
//
// Ports packages/codemode/src/runtime/host.ts (parseBridgeJson).
func parseBridgeJSON(text, what string) (json.RawMessage, error) {
	if !json.Valid([]byte(text)) {
		return nil, bridgeError(what + " is not valid JSON")
	}
	return json.RawMessage(text), nil
}

// isJSONString reports whether raw is a JSON string.
func isJSONString(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '"'
}

// parseStoreWrites decodes the prelude's list of [key, jsonText?] pairs and checks every entry.
//
// Ports packages/codemode/src/runtime/host.ts (parseStoreWrites).
func parseStoreWrites(writes string) (StoreWrites, error) {
	result := StoreWrites{Set: map[string]json.RawMessage{}}
	parsed, err := parseBridgeJSON(writes, "store writes")
	if err != nil {
		return StoreWrites{}, err
	}
	if parsed[0] != '[' {
		return StoreWrites{}, bridgeError("store writes are not an array")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(parsed, &entries); err != nil {
		return StoreWrites{}, bridgeError("store writes are not an array")
	}
	malformed := bridgeError("store writes contain a malformed entry")
	for _, raw := range entries {
		var entry []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &entry) != nil || len(entry) == 0 || !isJSONString(entry[0]) ||
			(len(entry) != 1 && (len(entry) != 2 || !isJSONString(entry[1]))) {
			return StoreWrites{}, malformed
		}
		// A key keeps its UTF-16 units: an unpaired surrogate stays WTF-8 in the Go string rather than becoming U+FFFD.
		var jsKey ecmascript.String
		if err := jsKey.UnmarshalJSON(entry[0]); err != nil {
			return StoreWrites{}, malformed
		}
		key := string(jsKey)
		if len(entry) == 1 {
			result.Delete = append(result.Delete, key)
			continue
		}
		var text string
		if err := json.Unmarshal(entry[1], &text); err != nil {
			return StoreWrites{}, malformed
		}
		value, err := parseBridgeJSON(text, "store value for "+string(jsstring.AppendString(nil, key)))
		if err != nil {
			return StoreWrites{}, err
		}
		// host.ts assigns `writes.set[key] = value`, which sets the prototype of `set` for `__proto__` and creates no property.
		if key != "__proto__" {
			if _, ok := result.Set[key]; !ok {
				result.setOrder = append(result.setOrder, key)
			}
			result.Set[key] = value
		}
	}
	return result, nil
}

// parseScriptError decodes the prelude's description of a thrown error.
//
// Ports packages/codemode/src/runtime/host.ts (parseScriptError).
func parseScriptError(text string) (*Error, error) {
	parsed, err := parseBridgeJSON(text, "script error")
	if err != nil {
		return nil, err
	}
	// typeof null and typeof a primitive are not "object"; an array is, and has no message.
	if parsed[0] != '{' && parsed[0] != '[' {
		return nil, bridgeError("script error is not an object")
	}
	var fields struct {
		Name    json.RawMessage `json:"name"`
		Message json.RawMessage `json:"message"`
		Stack   json.RawMessage `json:"stack"`
	}
	malformed := bridgeError("script error is malformed")
	if parsed[0] == '[' || json.Unmarshal(parsed, &fields) != nil || !isJSONString(fields.Message) ||
		(fields.Name != nil && !isJSONString(fields.Name)) || (fields.Stack != nil && !isJSONString(fields.Stack)) {
		return nil, malformed
	}
	// The fields keep their UTF-16 units: JSON.parse keeps an unpaired surrogate, and so does the WTF-8 string of ecmascript.String.
	decode := func(raw json.RawMessage) string {
		var text ecmascript.String
		_ = text.UnmarshalJSON(raw)
		return string(text)
	}
	failure := &Error{Kind: ErrorScript, Message: decode(fields.Message)}
	if fields.Name != nil {
		failure.Name = decode(fields.Name)
	}
	if fields.Stack != nil {
		failure.Stack = decode(fields.Stack)
	}
	return failure, nil
}

// serializeStore is host.ts serializeStore: each value becomes JSON.stringify of itself. host.ts assigns
// `serialized[key] = json`, which never creates an own `__proto__` property, so that key does not reach the script.
func serializeStore(store map[string]json.RawMessage) map[string]string {
	serialized := make(map[string]string, len(store))
	for key, value := range store {
		text, err := jsonstringify.Canonicalize(value)
		if key == "__proto__" || err != nil {
			continue
		}
		serialized[key] = string(text)
	}
	return serialized
}

// receive handles a message from the VM. A launcher may deliver from any goroutine; messages are handled in the order
// they arrive.
func (e *execution) receive(message vmMessage) {
	if e.isFinished() {
		return
	}
	switch message.kind {
	case vmMessageCall:
		if e.control != nil {
			e.forwardCall(message)
		} else {
			e.startCall(message.id, message.isTool, message.name, message.args, message.argsDefined)
		}
	case vmMessageStarted:
		if e.control != nil && e.control.OnStarted != nil {
			e.control.OnStarted(message.fingerprint)
		}
	case vmMessageIdle:
		if e.control != nil && e.control.OnIdle != nil {
			e.control.OnIdle(message.seq, message.issued)
		}
	case vmMessageOutput:
		e.mu.Lock()
		e.output = append(e.output, message.item)
		e.mu.Unlock()
	case vmMessageDone:
		e.handleDone(message)
	case vmMessageCrash:
		e.finish(&Error{Kind: ErrorSandbox, Message: message.message}, nil, StoreWrites{})
	default:
		e.finish(bridgeBroken("unknown message from the worker"), nil, StoreWrites{})
	}
}

// handleDone decodes everything before finish, which must not fail once it starts.
//
// Ports packages/codemode/src/runtime/execution.ts (Execution.handleDone).
func (e *execution) handleDone(message vmMessage) {
	e.mu.Lock()
	e.during = message.during
	e.mu.Unlock()
	if !message.ok {
		failure, err := parseScriptError(message.errorText)
		if err != nil {
			e.finish(bridgeBroken(err.Error()), nil, StoreWrites{})
			return
		}
		e.finish(failure, nil, StoreWrites{})
		return
	}
	if !message.writesDefined {
		e.finish(bridgeBroken("unknown message from the worker"), nil, StoreWrites{})
		return
	}
	var raw json.RawMessage
	if message.valueDefined {
		var err error
		if raw, err = parseBridgeJSON(message.value, "return value"); err != nil {
			e.finish(bridgeBroken(err.Error()), nil, StoreWrites{})
			return
		}
	}
	storeWrites, err := parseStoreWrites(message.writes)
	if err != nil {
		e.finish(bridgeBroken(err.Error()), nil, StoreWrites{})
		return
	}
	e.finish(nil, raw, storeWrites)
}

// startCall runs a nested tool call in its own goroutine, started in message order, and sends its result back to
// the goroutine that owns the VM.
//
// Ports packages/codemode/src/runtime/host.ts (Execution.handleCall).
func (e *execution) startCall(id int, isTool bool, name, args string, argsDefined bool) {
	var record *Call
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	if _, duplicate := e.pending[id]; duplicate {
		e.mu.Unlock()
		e.finish(bridgeBroken("duplicate call id "+strconv.Itoa(id)), nil, StoreWrites{})
		return
	}
	if isTool {
		record = &Call{Name: name, Status: CallCancelled}
		e.records = append(e.records, record)
	}
	ctx, cancel := context.WithCancel(e.callCtx)
	pending := &pendingCall{record: record, startedAt: time.Now(), cancel: cancel}
	e.pending[id] = pending
	e.mu.Unlock()

	initiated := make(chan struct{})
	var initiate sync.Once
	mark := func() { initiate.Do(func() { close(initiated) }) }
	ctx = context.WithValue(ctx, callInitiationKey{}, mark)
	awaitInitiation := false
	if isTool {
		table := e.tools
		awaitInitiation = table[name].AwaitsInitiation
	}
	e.calls.Go(func() {
		defer cancel()
		defer mark()
		reply := callResult{id: id}
		status := CallOK
		table := e.globals
		if isTool {
			table = e.tools
		}
		tool, found := table[name]
		var value json.RawMessage
		var err error
		switch {
		case !found:
			kind := "global"
			if isTool {
				kind = "tool"
			}
			err = fmt.Errorf("Unknown %s %q", kind, name)
		case tool.Execute == nil:
			err = fmt.Errorf("Tool %q has no Execute function", name)
		default:
			var raw json.RawMessage
			if argsDefined {
				raw = json.RawMessage(args)
			}
			value, err = tool.Execute(ctx, raw)
			if err == nil && len(value) > 0 && !json.Valid(value) {
				err = fmt.Errorf("Tool %q returned invalid JSON", name)
			}
		}
		if err != nil {
			reply.payload, reply.defined = err.Error(), true
			status = CallError
		} else {
			reply.ok = true
			reply.payload, reply.defined = string(value), len(value) > 0
		}

		// Already cancelled by finish: the record keeps "cancelled" and the VM is gone or going.
		e.mu.Lock()
		p, still := e.pending[id]
		if still {
			delete(e.pending, id)
			if record != nil {
				record.Status = status
				record.DurationMs = millisSince(p.startedAt, time.Now())
			}
		}
		e.mu.Unlock()
		if !still {
			return
		}
		e.post(reply)
	})
	if awaitInitiation {
		select {
		case <-initiated:
		case <-ctx.Done():
		}
	}
}

// forwardCall records a call of a Start run and hands it to the caller, who answers with deliver.
//
// Ports packages/codemode/src/runtime/execution.ts (Execution.forwardCall).
func (e *execution) forwardCall(message vmMessage) {
	var args json.RawMessage
	if message.argsDefined {
		raw, err := parseBridgeJSON(message.args, "call arguments")
		if err != nil {
			e.finish(bridgeBroken(err.Error()), nil, StoreWrites{})
			return
		}
		args = raw
	}
	var record *Call
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	if message.isTool {
		record = &Call{Name: message.name, Status: CallCancelled}
		e.records = append(e.records, record)
	}
	_, cancel := context.WithCancel(e.callCtx)
	e.pending[message.id] = &pendingCall{record: record, startedAt: time.Now(), cancel: cancel}
	e.mu.Unlock()
	target := "global"
	if message.isTool {
		target = "tool"
	}
	e.control.OnCall(HostCall{ID: message.id, Target: target, Name: message.name, Args: args})
}

// deliver answers call id of a Start run as result number seq.
//
// Ports packages/codemode/src/runtime/execution.ts (Execution.deliver).
func (e *execution) deliver(seq int, now float64, id int, outcome Outcome) error {
	if e.isFinished() {
		return nil
	}
	if math.IsNaN(now) || math.IsInf(now, 0) {
		return fmt.Errorf("The clock value of result %d is not a finite number", seq)
	}
	e.mu.Lock()
	if seq < 1 || e.delivered[seq] {
		e.mu.Unlock()
		return fmt.Errorf("Result number %d is not a positive integer used once", seq)
	}
	pending, waiting := e.pending[id]
	if !waiting {
		e.mu.Unlock()
		return fmt.Errorf("Call %d is not waiting for a result", id)
	}
	e.mu.Unlock()
	// execution.ts deliver(): rejection properties must be an object, not null, an array or a primitive.
	if !outcome.OK && outcome.Properties != nil {
		if trimmed := bytes.TrimLeft(outcome.Properties, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
			return fmt.Errorf("The properties of result %d are not an object", seq)
		}
	}
	// Serialized before anything is recorded, so a value JSON cannot hold leaves the call waiting for another answer.
	reply := callResult{id: id, ok: outcome.OK, seq: seq, now: now, hasNow: true}
	if outcome.OK {
		if len(outcome.Value) > 0 {
			if !json.Valid(outcome.Value) {
				return fmt.Errorf("The value of result %d is not valid JSON", seq)
			}
			reply.payload, reply.defined = string(outcome.Value), true
		}
	} else {
		reply.payload, reply.defined = outcome.Message, true
		if outcome.Properties != nil {
			if !json.Valid(outcome.Properties) {
				return fmt.Errorf("The properties of result %d are not valid JSON", seq)
			}
			reply.properties, reply.propertiesDefined = string(outcome.Properties), true
		}
	}
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return nil
	}
	e.delivered[seq] = true
	delete(e.pending, id)
	if pending.record != nil {
		if outcome.OK {
			pending.record.Status = CallOK
		} else {
			pending.record.Status = CallError
		}
		pending.record.DurationMs = millisSince(pending.startedAt, time.Now())
	}
	e.mu.Unlock()
	pending.cancel()
	e.post(reply)
	return nil
}
