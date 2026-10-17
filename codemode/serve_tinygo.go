//go:build tinygo

package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"unsafe"
)

// The codemode module of the Cloudflare Worker host (durable/core/host/cf/src/codemode/serve.ts). The module serves one execution
// as a step machine: the host calls an export with an event (the request, a reply to an exchange, a rejected exchange) and reads
// the exchanges to send from the output buffer. Nothing in it blocks, as TinyGo's -scheduler=none requires; the host owns the
// asynchrony and awaits every exchange.
//
// Ports packages/codemode/src/runtime/remote.ts (serveCodemodeRemote) for the host that cannot block on an exchange.

var (
	errExchangeRejected = errors.New("the exchange was rejected")
	errBadReply         = errors.New("the reply is not a codemode remote reply")
)

// serveStart is the first event: the client's request and the limits of the sandbox object.
type serveStart struct {
	Request RemoteRequest `json:"request"`
	Options struct {
		MaxInterruptBudget  int    `json:"maxInterruptBudget"`
		MaxMemoryLimitBytes uint32 `json:"maxMemoryLimitBytes"`
	} `json:"options"`
}

// served is the one execution of this module instance.
var served struct {
	server  *remoteServer
	nextID  int
	waiting map[int]func(*RemoteReply, error)
	// chunks is the JSON of the exchanges the step sends, one buffer each, written as the server made them. The host reads them one
	// by one: a script's output can be megabytes, and one joined buffer would hold it twice next to the messages it came from.
	chunks   [][]byte
	resolved bool
	inbox    []byte
}

// hostOutput hands the Worker host one output message, as the JSON of a WorkerToHostMessage, for it to hold until an exchange takes it.
//
//go:wasmimport host output
func hostOutput(ptr, n uint32)

// outputScratch is the buffer each output message is written into before it is handed to the host.
var outputScratch []byte

// keepOutputInHost gives the host each output item as it happens, so that a trap of this module does not lose the items the script
// printed before it, and the module's heap does not hold them (Pi's remote keeps them in the isolate's memory, which a trap cannot reach).
func keepOutputInHost(m WorkerToHostMessage) {
	outputScratch = appendRemoteMessage(outputScratch[:0], RemoteMessage{WorkerToHostMessage: m})
	hostOutput(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(outputScratch)))), uint32(len(outputScratch))) //nolint:gosec // G103: the host reads the message at this offset of linear memory
}

// serveExchangeCall is the RemoteExchange of the served execution: it records the batch for the host and keeps the callback the
// host's answer will run.
func serveExchangeCall(_ context.Context, messages []RemoteMessage, reply func(*RemoteReply, error)) {
	served.nextID++
	served.waiting[served.nextID] = reply
	served.chunks = append(served.chunks, appendExchange(make([]byte, 0, exchangeSizeHint(messages)), served.nextID, messages))
}

// exchangeSizeHint is the capacity an exchange's buffer starts with: the strings of its messages as written when they need no escape,
// so that a script's megabytes of output are written into one allocation.
func exchangeSizeHint(messages []RemoteMessage) int {
	n := 64
	for _, m := range messages {
		n += 72 + len(m.Name) + len(m.Target) + len(m.Message)
		for _, s := range []*string{m.Args, m.Value, m.Writes, m.Error} {
			if s != nil {
				n += len(*s)
			}
		}
		if m.Item != nil {
			n += len(m.Item.Text) + len(m.Item.Data) + len(m.Item.MimeType)
		}
		if m.Failure != nil {
			n += len(m.Failure.Message) + len(m.Failure.Stack) + len(m.Failure.Name)
		}
	}
	return n
}

// serveFinish runs the queued jobs to quiescence and publishes the step: the chunks the exchanges made and whether the execution
// is over.
func serveFinish() {
	for job := served.server.loop.next(); job != nil; job = served.server.loop.next() {
		job()
	}
	served.resolved = served.server.resolved
}

// beginStep drops the chunks of the previous step.
func beginStep() { served.chunks = nil }

func serveInput(ptr, n uint32) []byte {
	if ptr != inboxPtr() || int(n) >= len(served.inbox) {
		panic("codemode: the input is not the buffer codemode_alloc returned")
	}
	return served.inbox[:n]
}

func inboxPtr() uint32 {
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(served.inbox)))) //nolint:gosec // G103: the host needs the offset of this buffer in linear memory
}

// entryPoints calls every //export function, which the linter does not see anyone call, so that they count as the roots everything
// else in the module is reached from. TinyGo does not allow an exported function as a value, so they are called, never run.
func entryPoints(run bool) {
	if run {
		codemodeAlloc(0)
		codemodeServeStart(0, 0)
		codemodeServeReply(0, 0, 0)
		codemodeServeFailed(0)
		codemodeOutCount()
		codemodeOutPtr(0)
		codemodeOutLen(0)
		codemodeOutResolved()
		codemodeHostCall(0, 0, 0, 0, 0)
		codemodeHostInterrupt()
	}
}

var _ = entryPoints

//export codemode_alloc
func codemodeAlloc(n uint32) uint32 {
	served.inbox = make([]byte, n+1)
	return inboxPtr()
}

//export codemode_serve_start
func codemodeServeStart(ptr, n uint32) {
	beginStep()
	var start serveStart
	if err := json.Unmarshal(serveInput(ptr, n), &start); err != nil {
		panic("codemode: bad start event: " + err.Error())
	}
	served.waiting = map[int]func(*RemoteReply, error){}
	options := ServeOptions{MaxInterruptBudget: start.Options.MaxInterruptBudget, MaxMemoryLimitBytes: start.Options.MaxMemoryLimitBytes}
	launch := func(budget int) vmLauncher {
		load := func(context.Context, uint32) (*engine, error) { return hostEngine, nil }
		return inlineLauncher(load, PreludeSource, budget)
	}
	served.server = startRemoteServer(context.Background(), start.Request, serveExchangeCall, options, launch)
	served.server.keepOutput = keepOutputInHost
	serveFinish()
}

//export codemode_serve_reply
func codemodeServeReply(id, ptr, n uint32) {
	beginStep()
	reply, ok := decodeReply(serveInput(ptr, n))
	done := served.waiting[int(id)]
	delete(served.waiting, int(id))
	if done != nil {
		if ok {
			done(reply, nil)
		} else {
			done(nil, errBadReply)
		}
	}
	serveFinish()
}

//export codemode_serve_failed
func codemodeServeFailed(id uint32) {
	beginStep()
	done := served.waiting[int(id)]
	delete(served.waiting, int(id))
	if done != nil {
		done(nil, errExchangeRejected)
	}
	serveFinish()
}

//export codemode_out_count
func codemodeOutCount() uint32 { return uint32(len(served.chunks)) }

//export codemode_out_ptr
func codemodeOutPtr(i uint32) uint32 {
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(served.chunks[i])))) //nolint:gosec // G103: the host reads the chunk at this offset of linear memory
}

//export codemode_out_len
func codemodeOutLen(i uint32) uint32 { return uint32(len(served.chunks[i])) }

//export codemode_out_resolved
func codemodeOutResolved() uint32 {
	if served.resolved {
		return 1
	}
	return 0
}

//export codemode_host_call
func codemodeHostCall(namePtr, nameLen, this, argc, argvPtr uint32) uint32 {
	return hostCallOf(namePtr, nameLen, this, argc, argvPtr)
}

//export codemode_host_interrupt
func codemodeHostInterrupt() uint32 { return hostInterruptOf() }
