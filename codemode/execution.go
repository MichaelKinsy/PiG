package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// terminationGrace is how long the interrupt flag has to stop a VM before the instance is halted. The flag stops
// any script that reaches QuickJS's interrupt check; the halt traps the instance at its next loop iteration, which
// stops a call that does not.
const terminationGrace = 500 * time.Millisecond

type pendingCall struct {
	record    *Call
	startedAt time.Time
	cancel    context.CancelFunc
}

type callResult struct {
	id      int
	ok      bool
	payload string
	defined bool
}

// execution is one script run in its own VM. It ends exactly once, in finish: the script's done message, a VM
// failure, the deadline, the caller's context, or Close. The goroutine that called run owns the VM; the timer, the
// context watcher and Close only record why the execution ended and raise the interrupt flag.
//
// Ports packages/codemode/src/runtime/host.ts (Execution).
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

	interrupt atomic.Bool
	vmExited  atomic.Bool
	// machine is the running VM, and halting records that the grace period ran out: whichever of the two is set
	// second halts the VM.
	machine atomic.Pointer[vm]
	halting atomic.Bool
	results chan callResult
	done    chan struct{}
	exited  chan struct{}
	calls   sync.WaitGroup

	mu       sync.Mutex
	finished bool
	timer    *time.Timer
	grace    *time.Timer
	stopCtx  func() bool
	output   []OutputItem
	records  []*Call
	pending  map[int]*pendingCall
	result   Result
}

func newExecution(s *Sandbox, code string, timeoutMs float64, store map[string]json.RawMessage, tools, globals []Tool) *execution {
	e := &execution{
		sandbox: s, code: code, timeoutMs: timeoutMs, store: store,
		tools: map[string]Tool{}, toolOrder: tools, globals: map[string]Tool{}, globalOrd: globals,
		results: make(chan callResult), done: make(chan struct{}), exited: make(chan struct{}),
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
	e.finish(&Error{Kind: ErrorAborted, Message: message}, nil, "")
	<-e.exited
}

func (e *execution) run(ctx context.Context) Result {
	defer close(e.exited)
	e.callCtx = context.WithoutCancel(ctx)

	if ctx.Err() != nil {
		e.finish(&Error{Kind: ErrorAborted, Message: abortMessage(ctx)}, nil, "")
	} else {
		stop := context.AfterFunc(ctx, func() {
			e.finish(&Error{Kind: ErrorAborted, Message: abortMessage(ctx)}, nil, "")
		})
		e.mu.Lock()
		e.stopCtx = stop
		e.mu.Unlock()
	}

	if !e.isFinished() {
		if eng, err := e.sandbox.engine(ctx); err != nil {
			e.finish(loadError(err), nil, "")
		} else if !e.isFinished() {
			e.startDeadline()
			e.drive(eng)
		}
	}
	// A VM that returned without a verdict (it cannot: the loop ends only on finish) still ends here.
	e.finish(&Error{Kind: ErrorSandbox, Message: "The VM exited before the script settled"}, nil, "")
	e.calls.Wait()
	e.mu.Lock()
	if e.grace != nil {
		e.grace.Stop()
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.mu.Unlock()
	return e.result
}

// startDeadline arms the timeout once the module is ready. Compiling the module happens once per process and is not
// the script's time, so it does not count against the deadline (upstream's deadline also spans its much shorter
// wasm load, host.ts:110-114).
func (e *execution) startDeadline() {
	if !math.IsInf(e.timeoutMs, 1) {
		timeout := time.Duration(min(e.timeoutMs*float64(time.Millisecond), float64(math.MaxInt64)))
		e.mu.Lock()
		e.timer = time.AfterFunc(timeout, func() {
			e.finish(&Error{Kind: ErrorTimeout, Message: timeoutMessage(e.timeoutMs)}, nil, "")
		})
		e.mu.Unlock()
	}
}

// drive runs the VM until the execution finishes, then closes the instance.
func (e *execution) drive(eng *engine) {
	var machine *vm
	defer func() {
		if machine != nil {
			machine.close()
		}
		e.vmExited.Store(true)
	}()
	defer func() {
		if r := recover(); r != nil {
			trap, ok := r.(vmTrap)
			if !ok {
				panic(r)
			}
			e.finish(&Error{Kind: ErrorSandbox, Message: trap.Error()}, nil, "")
		}
	}()
	var err error
	machine, err = eng.newVM(e.callCtx, e.sandbox.options.MemoryLimitBytes, &e.interrupt)
	if err != nil {
		e.finish(&Error{Kind: ErrorSandbox, Message: "Failed to start the VM: " + err.Error()}, nil, "")
		return
	}
	e.machine.Store(machine)
	if e.halting.Load() {
		machine.halt()
	}
	w := &worker{e: e, vm: machine}
	w.start()
	for !e.isFinished() {
		select {
		case r := <-e.results:
			w.settle(r)
		case <-e.done:
		}
	}
}

// finish records the outcome; only the first call counts. It cancels every pending nested call and stops the VM.
//
// Ports packages/codemode/src/runtime/host.ts (Execution.finish).
func (e *execution) finish(failure *Error, value json.RawMessage, writes string) {
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
	calls := make([]Call, len(e.records))
	for i, r := range e.records {
		calls[i] = *r
	}
	output := append([]OutputItem{}, e.output...)
	if failure != nil {
		e.result = Result{Error: failure, Output: output, Calls: calls}
	} else {
		storeWrites, err := parseStoreWrites(writes)
		if err != nil {
			e.result = Result{Error: &Error{Kind: ErrorSandbox, Message: err.Error()}, Output: output, Calls: calls}
		} else {
			e.result = Result{OK: true, Value: value, Output: output, Calls: calls, StoreWrites: storeWrites}
		}
	}
	e.mu.Unlock()
	if stopCtx != nil {
		stopCtx()
	}
	e.interrupt.Store(true)
	// Escalate if the script does not reach an interrupt check.
	e.mu.Lock()
	e.grace = time.AfterFunc(terminationGrace, func() {
		if !e.vmExited.Load() {
			hardStops.Add(1)
			e.halting.Store(true)
			if machine := e.machine.Load(); machine != nil {
				machine.halt()
			}
		}
	})
	e.mu.Unlock()
	close(e.done)
}

func millisSince(start, now time.Time) float64 {
	return float64(now.Sub(start)) / float64(time.Millisecond)
}

// parseStoreWrites decodes the prelude's list of [key, jsonText?] pairs.
func parseStoreWrites(writes string) (StoreWrites, error) {
	result := StoreWrites{Set: map[string]json.RawMessage{}}
	if writes == "" {
		return result, nil
	}
	var pairs [][]json.RawMessage
	if err := json.Unmarshal([]byte(writes), &pairs); err != nil {
		return StoreWrites{}, fmt.Errorf("invalid store writes: %w", err)
	}
	for _, pair := range pairs {
		var key string
		if len(pair) == 0 || json.Unmarshal(pair[0], &key) != nil {
			return StoreWrites{}, errors.New("invalid store write")
		}
		if len(pair) < 2 {
			result.Delete = append(result.Delete, key)
			continue
		}
		var text string
		if err := json.Unmarshal(pair[1], &text); err != nil {
			return StoreWrites{}, errors.New("invalid store write value")
		}
		result.Set[key] = json.RawMessage(text)
	}
	return result, nil
}

// worker drives the VM the way packages/codemode/src/runtime/worker.ts does: it evaluates the prelude, starts the
// script, settles nested calls and drains the job queue.
type worker struct {
	e                    *execution
	vm                   *vm
	api                  uint32
	settleFn, runFn, stl uint32
}

type describedError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Stack   string `json:"stack"`
}

func (w *worker) crash(message string) {
	w.e.finish(&Error{Kind: ErrorSandbox, Message: message}, nil, "")
}

// failed reports whether an exception should end the execution as a crash. An exception after finish is the
// interrupt, not a failure.
func (w *worker) failed(x *jsException) bool {
	if x == nil {
		return false
	}
	if !w.e.isFinished() {
		w.crash(x.String())
	}
	return true
}

func marshalNoEscape(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

func (w *worker) start() {
	e, vm := w.e, w.vm
	type toolJSON struct {
		Name        string `json:"name"`
		JSName      string `json:"jsName"`
		Description string `json:"description"`
	}
	type globalJSON struct {
		Name   string `json:"name"`
		Spread bool   `json:"spread"`
	}
	tools := make([]toolJSON, len(e.toolOrder))
	for i, t := range e.toolOrder {
		tools[i] = toolJSON{t.Name, ToCodemodeIdentifier(t.Name), t.Description}
	}
	globals := make([]globalJSON, len(e.globalOrd))
	for i, g := range e.globalOrd {
		globals[i] = globalJSON{g.Name, g.Spread}
	}
	store := map[string]string{}
	for k, v := range e.store {
		if len(v) > 0 {
			store[k] = string(v)
		}
	}

	bridge := vm.newFunction("bridge", func(_ uint32, a []uint32) uint32 {
		w.handleBridge(a)
		return vm.undefined
	})
	prelude, x := vm.eval(PreludeSource, "codemode-prelude.js")
	if w.failed(x) {
		return
	}
	toolsArg, globalsArg, storeArg := vm.newString(marshalNoEscape(tools)), vm.newString(marshalNoEscape(globals)), vm.newString(marshalNoEscape(store))
	api, x := vm.callFunction(prelude, vm.undefined, bridge, toolsArg, globalsArg, storeArg)
	for _, h := range []uint32{prelude, toolsArg, globalsArg, storeArg} {
		vm.free(h)
	}
	if w.failed(x) {
		return
	}
	w.api = api
	w.settleFn, w.runFn, w.stl = vm.getProp(api, "settle"), vm.getProp(api, "run"), vm.getProp(api, "stalled")

	// The prefix shares the first line with the script so reported line numbers match the script as written.
	fn, x := vm.eval("(async (tools, console) => {"+e.code+"\n})", "codemode.js")
	if x != nil {
		if !e.isFinished() {
			w.finishScriptError(x)
		}
		return
	}
	result, x := vm.callFunction(w.runFn, api, fn)
	if x == nil {
		vm.free(result)
	}
	vm.free(fn)
	if w.failed(x) {
		return
	}
	w.drain()
}

// finishScriptError reports a script that failed to parse: describeException in worker.ts.
func (w *worker) finishScriptError(x *jsException) {
	head := x.name
	if x.message != "" {
		head = x.name + ": " + x.message
	}
	stack := jsstring.TrimEnd(x.stack)
	if stack != "" {
		stack = head + "\n" + stack
	} else {
		stack = head
	}
	w.e.finish(&Error{Kind: ErrorScript, Name: x.name, Message: x.message, Stack: stack}, nil, "")
}

// drain runs queued jobs, then fails a script that waits on nothing that can ever resume it.
func (w *worker) drain() {
	if x := w.vm.executePendingJobs(); x != nil {
		w.failed(x)
		return
	}
	result, x := w.vm.callFunction(w.stl, w.api)
	if x == nil {
		w.vm.free(result)
	}
	w.failed(x)
}

func (w *worker) settle(r callResult) {
	vm := w.vm
	id := vm.newNumber(float64(r.id))
	ok := uint32(vm.call("qjs_get_false"))
	if r.ok {
		ok = uint32(vm.call("qjs_get_true"))
	}
	payload := vm.undefined
	if r.defined {
		payload = vm.newString(r.payload)
	}
	result, x := vm.callFunction(w.settleFn, w.api, id, ok, payload)
	if x == nil {
		vm.free(result)
	}
	vm.free(id)
	if r.defined {
		vm.free(payload)
	}
	if w.failed(x) {
		return
	}
	w.drain()
}

// handleBridge receives the prelude's messages: call, global, output and done.
func (w *worker) handleBridge(a []uint32) {
	e, vm := w.e, w.vm
	if e.isFinished() {
		return
	}
	arg := func(i int) (string, bool) {
		if i >= len(a) || vm.isUndefined(a[i]) {
			return "", false
		}
		return vm.toString(a[i]), true
	}
	kind, _ := arg(0)
	switch kind {
	case "call", "global":
		id := int(vm.toNumber(a[1]))
		name, _ := arg(2)
		args, defined := arg(3)
		e.startCall(id, kind == "call", name, args, defined)
	case "output":
		typ, _ := arg(1)
		b, _ := arg(2)
		c, _ := arg(3)
		item := OutputItem{Type: "text", Text: b}
		if typ == "image" {
			item = OutputItem{Type: "image", Data: b, MimeType: c}
		}
		e.mu.Lock()
		e.output = append(e.output, item)
		e.mu.Unlock()
	case "done":
		if vm.toBool(a[1]) {
			value, defined := arg(2)
			writes, _ := arg(3)
			var raw json.RawMessage
			if defined {
				raw = json.RawMessage(value)
			}
			e.finish(nil, raw, writes)
			return
		}
		text, _ := arg(2)
		var parsed describedError
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			e.finish(&Error{Kind: ErrorSandbox, Message: "invalid script error: " + err.Error()}, nil, "")
			return
		}
		e.finish(&Error{Kind: ErrorScript, Name: parsed.Name, Message: parsed.Message, Stack: parsed.Stack}, nil, "")
	}
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
		select {
		case e.results <- reply:
		case <-e.done:
		}
	})
	if awaitInitiation {
		select {
		case <-initiated:
		case <-ctx.Done():
		}
	}
}
