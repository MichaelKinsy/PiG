package codemode

import (
	"context"
	"sync"
)

// vmEvents is what a launcher reports about its VM.
//
// Ports packages/codemode/src/runtime/execution.ts (VmEvents).
type vmEvents struct {
	// message delivers a message from the VM. The host validates it, so a launcher passes through anything it receives.
	message func(vmMessage)
	// failure reports that the VM failed or stopped outside the bridge.
	failure func(*Error)
	// ready tells the host that the launcher has loaded what it needs and is about to run the VM, so the deadline starts
	// now: compiling the module is not the script's time. A launcher that never calls it has the deadline armed when launch
	// returns.
	ready func()
	// turn is held by a launcher whose VM runs in-thread for as long as one turn of the VM lasts: a segment of the script and the
	// delivery of the messages it queued. The host's own deadline, context and Close wait for the turn to end before they
	// finish the execution, as Node runs a timer or an abort listener only between tasks, so a segment that computes past
	// the deadline still delivers its result first. A launcher whose VM runs elsewhere never takes it.
	turn *sync.Mutex
}

// vmChannel is the host's handle on a running VM.
//
// Ports packages/codemode/src/runtime/execution.ts (VmChannel).
type vmChannel interface {
	// start begins delivering the VM's messages. The host calls it once, as soon as it holds the channel.
	start()
	// post sends the VM the result of a nested call. A channel that has stopped drops it.
	post(callResult)
	// stop requests VM shutdown. The host calls it once, after the execution has its result. A worker VM may exit after
	// stop returns; an in-thread VM is closed when it returns.
	stop()
}

// vmLauncher starts a VM for one execution. An error becomes a sandbox failure with the error's message. The VM may
// report messages before launch returns, and a launcher must not call into the host from inside a VM callback.
//
// Ports packages/codemode/src/runtime/execution.ts (VmLauncher).
type vmLauncher func(ctx context.Context, data VmStartData, events vmEvents) (vmChannel, error)
