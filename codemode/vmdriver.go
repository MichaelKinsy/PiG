package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type vmMessageKind string

// The kinds of message a VM reports. The empty kind is a message the prelude sent with a kind the host does not know.
const (
	vmMessageCall   vmMessageKind = "call"
	vmMessageOutput vmMessageKind = "output"
	vmMessageDone   vmMessageKind = "done"
	vmMessageCrash  vmMessageKind = "crash"
	// vmMessageStarted and vmMessageIdle are reported by a deterministic VM only.
	vmMessageStarted vmMessageKind = "started"
	vmMessageIdle    vmMessageKind = "idle"
)

// vmMessage is one message from a VM to the host, with the strings exactly as the prelude sent them: the VM never builds
// structured values.
//
// Ports packages/codemode/src/runtime/protocol.ts (WorkerToHostMessage).
type vmMessage struct {
	kind vmMessageKind
	// call: a nested tool call (isTool) or a global.
	id          int
	isTool      bool
	name        string
	args        string
	argsDefined bool
	// output
	item OutputItem
	// done: a script that returned (ok, with value and writes) or threw (the error as JSON).
	ok            bool
	value         string
	valueDefined  bool
	writes        string
	writesDefined bool
	errorText     string
	// during is the result the VM was processing when the script finished, set by a deterministic VM.
	during *During
	// crash: the VM failed outside the script.
	message string
	// started: the VM's fingerprint.
	fingerprint string
	// idle: the result the VM processed and the calls it has issued in total.
	seq    int
	issued int
}

// callResult is the host's answer to a nested call.
//
// Ports packages/codemode/src/runtime/protocol.ts (HostToWorkerMessage).
type callResult struct {
	id      int
	ok      bool
	payload string
	defined bool
	// Deterministic mode: seq numbers the results from 1 in the order the VM applies them, and the VM's clock reads now (ms
	// since the epoch) while it processes this one. properties is, for a rejection, a JSON object whose properties the script's
	// Error gets.
	seq               int
	now               float64
	hasNow            bool
	properties        string
	propertiesDefined bool
}

// startVMOptions are the arguments of startVM.
//
// Ports packages/codemode/src/runtime/vm.ts (StartVmOptions).
type startVMOptions struct {
	// ctx is passed to every call into the instance.
	ctx     context.Context
	engine  *engine
	prelude string
	data    VmStartData
	// maxStackSize is vmOptions.maxStackSize.
	maxStackSize uint32
	// post receives every message for the host, synchronously from inside VM callbacks. It must not re-enter the VM.
	post func(vmMessage)
	// shouldInterrupt is polled by QuickJS while the script runs, and between batches of promise jobs.
	shouldInterrupt func() bool
	// created, when set, receives the VM as soon as it exists, so a caller can halt it while the script starts.
	created func(*vm)
	// interruptBudget is the number of interrupt polls the launcher allows, if it counts them; part of a deterministic VM's
	// fingerprint.
	interruptBudget *int
}

// vmSession is one VM running one script. The VM never decides on its own when it is finished: after done or crash it
// stays alive until the caller disposes it.
//
// Ports packages/codemode/src/runtime/vm.ts (VmSession).
type vmSession struct {
	vm                   *vm
	post                 func(vmMessage)
	shouldInterrupt      func() bool
	api                  uint32
	settleFn, runFn, stl uint32
	// ready reports that the prelude and the script were started; a VM that failed earlier has no api to settle.
	ready bool

	// Deterministic mode (clock set): issued counts the calls the script has made, nextSeq is the next result to apply, early
	// holds results that arrived before it, and current is the result being processed (0 during the script's first run).
	clock   *virtualClock
	issued  int
	nextSeq int
	current int
	early   map[int]callResult
}

// crash reports a failure of the VM outside the script. vmTrap is a failed call into the instance.
func (s *vmSession) crash(message string) {
	s.post(vmMessage{kind: vmMessageCrash, message: message})
}

// reportFailure turns a panic out of a call into the instance into a crash message and re-panics anything else.
func (s *vmSession) reportFailure(recovered any) {
	if recovered == nil {
		return
	}
	trap, ok := recovered.(vmTrap)
	if !ok {
		panic(recovered)
	}
	s.crash(trap.Error())
}

// startVM creates the VM, evaluates the prelude, starts the script and runs it until it waits. Failures after the VM
// exists are reported as a crash message; startVM only returns an error when the VM cannot be created at all.
//
// Ports packages/codemode/src/runtime/vm.ts (startVm).
func startVM(options startVMOptions) (session *vmSession, err error) {
	var clock *virtualClock
	if options.data.Deterministic != nil {
		clock = newVirtualClock(options.data.Deterministic)
	}
	machine, err := options.engine.newVM(options.ctx, vmOptions{
		memoryLimit:     options.data.MemoryLimitBytes,
		maxStackSize:    options.maxStackSize,
		shouldInterrupt: options.shouldInterrupt,
		clock:           clock,
	})
	if err != nil {
		return nil, err
	}
	if clock != nil {
		clock.creating = false
		machine.syncClock()
	}
	if options.created != nil {
		options.created(machine)
	}
	session = &vmSession{vm: machine, post: options.post, shouldInterrupt: options.shouldInterrupt, clock: clock, nextSeq: 1, early: map[int]callResult{}}
	defer func() { session.reportFailure(recover()) }()
	session.start(options)
	return session, nil
}

func (s *vmSession) start(options startVMOptions) {
	vm, data := s.vm, options.data
	bridge := vm.newFunction("bridge", func(_ uint32, a []uint32) uint32 {
		s.post(s.bridgeMessage(a))
		return vm.undefined
	})
	prelude, x := vm.eval(options.prelude, "codemode-prelude.js")
	if s.failed(x) {
		return
	}
	toolsArg, globalsArg, storeArg := vm.newString(marshalNoEscape(data.Tools)), vm.newString(marshalNoEscape(data.Globals)), vm.newString(storeJSON(data.Store))
	api, x := vm.callFunction(prelude, vm.undefined, bridge, toolsArg, globalsArg, storeArg)
	for _, h := range []uint32{prelude, toolsArg, globalsArg, storeArg} {
		vm.free(h)
	}
	if s.failed(x) {
		return
	}
	s.api = api
	s.settleFn, s.runFn, s.stl = vm.getProp(api, "settle"), vm.getProp(api, "run"), vm.getProp(api, "stalled")
	if s.clock != nil {
		maxStack := options.maxStackSize
		if maxStack == 0 {
			maxStack = maxStackSize
		}
		s.post(vmMessage{kind: vmMessageStarted, fingerprint: fingerprint(vm.quickjsVersion(), options.prelude, maxStack, data.MemoryLimitBytes, options.interruptBudget)})
	}

	// The prefix shares the first line with the script so reported line numbers match the script as written.
	fn, x := vm.eval("(async (tools, console) => {"+data.Code+"\n})", "codemode.js")
	if x != nil {
		s.post(vmMessage{kind: vmMessageDone, errorText: describeException(x)})
		return
	}
	result, x := vm.callFunction(s.runFn, api, fn)
	if x == nil {
		vm.free(result)
	}
	vm.free(fn)
	if s.failed(x) {
		return
	}
	s.ready = true
	if s.drain() && s.clock != nil {
		s.post(vmMessage{kind: vmMessageIdle, seq: 0, issued: s.issued})
	}
}

// failed reports an exception as a crash.
func (s *vmSession) failed(x *jsException) bool {
	if x == nil {
		return false
	}
	s.crash(x.String())
	return true
}

// describeException is the JSON the host decodes as a script error: a script that failed to parse.
//
// Ports packages/codemode/src/runtime/vm.ts (describeException).
func describeException(x *jsException) string {
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
	var out []byte
	out = append(out, `{"name":`...)
	out = jsstring.AppendString(out, x.name)
	out = append(out, `,"message":`...)
	out = jsstring.AppendString(out, x.message)
	out = append(out, `,"stack":`...)
	out = jsstring.AppendString(out, stack)
	return string(append(out, '}'))
}

// bridgeMessage turns the prelude's call of the bridge function into a message.
func (s *vmSession) bridgeMessage(a []uint32) vmMessage {
	vm := s.vm
	arg := func(i int) (string, bool) {
		if i >= len(a) || vm.isUndefined(a[i]) {
			return "", false
		}
		return vm.toString(a[i]), true
	}
	kind, _ := arg(0)
	switch kind {
	case "call", "global":
		s.issued++
		name, _ := arg(2)
		args, defined := arg(3)
		//portlint:allow numbers the id is the prelude's own counter (assets/prelude.js:101,175 `nextId++` from 1), a small positive integer user code cannot reach
		return vmMessage{kind: vmMessageCall, id: int(vm.toNumber(a[1])), isTool: kind == "call", name: name, args: args, argsDefined: defined}
	case "output":
		typ, _ := arg(1)
		b, _ := arg(2)
		c, _ := arg(3)
		item := OutputItem{Type: OutputItemText, Text: b}
		switch typ {
		case "image":
			item = OutputItem{Type: OutputItemImage, Data: b, MimeType: c}
		case "console":
			item.Console = true
		}
		return vmMessage{kind: vmMessageOutput, item: item}
	case "done":
		text, defined := arg(2)
		// The host stops the VM on done, so the idle of this drain never arrives: report its numbers here.
		var during *During
		if s.clock != nil {
			during = &During{Seq: s.current, Issued: s.issued}
		}
		if !vm.toBool(a[1]) {
			return vmMessage{kind: vmMessageDone, errorText: text, during: during}
		}
		writes, writesDefined := arg(3)
		return vmMessage{kind: vmMessageDone, ok: true, value: text, valueDefined: defined, writes: writes, writesDefined: writesDefined, during: during}
	}
	return vmMessage{}
}

// deliver hands the VM a tool result and runs the script until it waits again. It must only be called while the VM is
// idle, which holds for any caller that never re-enters from post.
//
// A deterministic VM applies results strictly in seq order, buffering early ones, and reports idle after each.
//
// Ports packages/codemode/src/runtime/vm.ts (VmSession.deliver).
func (s *vmSession) deliver(r callResult) {
	if !s.ready {
		return
	}
	defer func() { s.reportFailure(recover()) }()
	if s.clock == nil {
		s.apply(r)
		return
	}
	if _, used := s.early[r.seq]; r.seq < s.nextSeq || used {
		s.crash(fmt.Sprintf("Result %d has sequence number %d, expected %d or later, once", r.id, r.seq, s.nextSeq))
		return
	}
	s.early[r.seq] = r
	for next, ok := s.early[s.nextSeq]; ok; next, ok = s.early[s.nextSeq] {
		delete(s.early, s.nextSeq)
		// Never backwards: a resumed host's clock may be behind the one that started the run.
		if next.hasNow {
			s.clock.advance(next.now)
			s.vm.syncClock()
		}
		s.current = s.nextSeq
		if !s.apply(next) {
			return
		}
		s.post(vmMessage{kind: vmMessageIdle, seq: s.nextSeq, issued: s.issued})
		s.nextSeq++
	}
}

// apply settles the call the result answers and drains the job queue; it reports false when that crashed the VM.
func (s *vmSession) apply(r callResult) bool {
	vm := s.vm
	id := vm.newNumber(float64(r.id))
	ok := uint32(vm.call("qjs_get_false"))
	if r.ok {
		ok = uint32(vm.call("qjs_get_true"))
	}
	payload, properties := vm.undefined, vm.undefined
	if r.defined {
		payload = vm.newString(r.payload)
	}
	if r.propertiesDefined {
		properties = vm.newString(r.properties)
	}
	result, x := vm.callFunction(s.settleFn, s.api, id, ok, payload, properties)
	if x == nil {
		vm.free(result)
	}
	vm.free(id)
	if r.defined {
		vm.free(payload)
	}
	if r.propertiesDefined {
		vm.free(properties)
	}
	if s.failed(x) {
		return false
	}
	return s.drain()
}

// drain runs queued jobs, then fails a script that waits on nothing that can ever resume it.
func (s *vmSession) drain() bool {
	if x := s.vm.executePendingJobs(s.shouldInterrupt); x != nil {
		return !s.failed(x)
	}
	result, x := s.vm.callFunction(s.stl, s.api)
	if x == nil {
		s.vm.free(result)
	}
	return !s.failed(x)
}

// dispose releases the VM's wasm instance. The session must not be used afterwards.
func (s *vmSession) dispose() { s.vm.close() }

func marshalNoEscape(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// storeJSON is the JSON object the prelude parses as the load() snapshot. A key keeps its UTF-16 units: an unpaired
// surrogate (WTF-8 in a Go string) is written with its \u escapes.
func storeJSON(store map[string]string) string {
	out := []byte{'{'}
	for _, key := range slices.Sorted(maps.Keys(store)) {
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = jsstring.AppendString(out, key)
		out = append(out, ':')
		out = jsstring.AppendString(out, store[key])
	}
	return string(append(out, '}'))
}
