//go:build !tinygo

package codemode

import (
	"context"
	"fmt"
	"sync"
)

// Remote protocol: the VM runs in another isolate, reached over an RPC boundary such as a Cloudflare Durable Object
// proxy, while tools keep running in the caller. The caller keeps the host role (RemoteSandbox: tools, call records,
// deadline, abort) and the remote side only runs the VM (ServeRemote).
//
//	client                                        remote
//	remote.Execute(ctx, request, exchange) ─────▶ starts the VM (in-thread, with interrupt budget)
//	                                      ◀─────── exchange([...outputs, call])   one call per tool call
//	runs the tool, replies with the result ──────▶ VM continues
//	                                      ◀─────── exchange([...outputs, done])   terminal, reply ignored
//	Execute returns ◀──────────────────────────── VM disposed
//
// Output is buffered on the remote side and sent with the next call or the terminal message, so a script that prints
// in a loop does not make one RPC per line. Each exchange carries at most one call, so concurrent tool calls are
// concurrent RPCs. Messages are delivered to the client in the order the remote sent them, which keeps output ordered
// before the call or result that follows.
//
// Termination: the client cannot interrupt a remote VM that is computing. When the client finishes first (deadline,
// abort, Close), it answers every pending and later exchange with a stop reply, and the remote stops the VM at that
// point. A script that computes without calling a tool runs until its interrupt budget is used up. The remote reports
// that as a failure message, which the client turns into the same timeout error an in-thread VM produces.
//
// Failure: if Execute returns an error (the remote isolate crashed, hit its CPU limit, or was redeployed) or returns
// without a terminal message, the execution fails with ErrorSandbox.

// RemoteSandboxOptions configures NewRemoteSandbox.
//
// Ports packages/codemode/src/runtime/remote.ts (RemoteCodemodeSandboxOptions) and types.ts (CodemodeSandboxBaseOptions).
type RemoteSandboxOptions struct {
	Tools []Tool
	// Globals are exposed as top-level identifiers instead of on tools; see SandboxOptions.
	Globals []Tool
	// TimeoutMs is the overall deadline per execution, including time in tools. Zero means the default, 300000; +Inf
	// disables the deadline. A script that computes without calling a tool is limited by the remote's interrupt
	// budget instead.
	TimeoutMs float64
	// MemoryLimitBytes is the VM memory limit requested from the remote, which may lower it. Zero means the remote's
	// limit.
	MemoryLimitBytes uint32
	// Remote returns the remote that runs one execution, called once per Execute. For a Durable Object, pick the
	// instance here, for example by the session id. An error fails the execution as "Failed to reach the remote
	// sandbox: ...".
	Remote func() (Remote, error)
	// InterruptBudget is the interrupt budget requested from the remote. Zero means the remote's default.
	InterruptBudget int
}

// RemoteSandbox runs each execution's VM on a remote, usually a Cloudflare Durable Object deployed with
// CodemodeSandboxDurableObject, while tools run here. It has the API and results of Sandbox, so it can replace it.
// Differences: a script that computes without calling a tool is limited by the remote's interrupt budget rather than
// TimeoutMs, and after a deadline or abort the remote VM keeps computing until it next calls a tool or uses up that
// budget, unless the Remote passes the context of Execute to ServeRemote, which stops a computing VM once the client finished.
//
// Ports packages/codemode/src/runtime/remote.ts (RemoteCodemodeSandbox).
type RemoteSandbox struct {
	*Sandbox
	// served joins the Remote.Execute calls this sandbox started: they outlive the executions that started them
	// (the result does not wait for the remote), and Close waits for them.
	served sync.WaitGroup
}

// NewRemoteSandbox validates the globals and registers the tools.
func NewRemoteSandbox(options RemoteSandboxOptions) (*RemoteSandbox, error) {
	r := &RemoteSandbox{}
	sandbox, err := newSandbox(SandboxOptions{Tools: options.Tools, Globals: options.Globals, TimeoutMs: options.TimeoutMs, MemoryLimitBytes: options.MemoryLimitBytes},
		func(*Sandbox) vmLauncher { return r.launcher(options.Remote, options.InterruptBudget) })
	if err != nil {
		return nil, err
	}
	r.Sandbox = sandbox
	return r, nil
}

// Close aborts in-flight executions, rejects new ones, and waits for the Remote.Execute calls this sandbox started to
// return: each was told to stop when its execution ended. Upstream's close() does not wait for the remote; Go joins the
// background work it owns, so a Remote that ignores the context of Execute makes Close wait for as long as that remote runs
// (up to its interrupt budget).
func (r *RemoteSandbox) Close() error {
	err := r.Sandbox.Close()
	r.served.Wait()
	return err
}

// launcher is the client side of the protocol.
//
// Ports packages/codemode/src/runtime/remote.ts (remoteLauncher).
func (r *RemoteSandbox) launcher(remote func() (Remote, error), interruptBudget int) vmLauncher {
	return func(ctx context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
		target, err := remote()
		if err != nil {
			return nil, fmt.Errorf("Failed to reach the remote sandbox: %w", err)
		}
		executeCtx, cancel := context.WithCancel(ctx)
		channel := &remoteChannel{events: events, waiting: map[int]func(*RemoteReply, error){}, cancel: cancel}
		request := RemoteRequest{Data: data, InterruptBudget: interruptBudget}
		r.served.Go(func() {
			err := target.Execute(executeCtx, request, channel.exchange)
			stopped, settled := channel.state()
			switch {
			case stopped:
			case err != nil:
				events.failure(&Error{Kind: ErrorSandbox, Message: "Remote sandbox failed: " + err.Error()})
			case !settled:
				events.failure(&Error{Kind: ErrorSandbox, Message: "Remote sandbox returned before the script settled"})
			}
		})
		return channel, nil
	}
}

// remoteChannel is the client's handle on a remote VM: the exchange the remote calls and the replies it waits for.
type remoteChannel struct {
	events vmEvents
	cancel context.CancelFunc

	mu      sync.Mutex
	stopped bool
	settled bool
	// waiting holds the reply for each call the remote asked about.
	waiting map[int]func(*RemoteReply, error)
}

func (c *remoteChannel) state() (stopped, settled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped, c.settled
}

// exchange delivers the remote's messages to the host in order and registers the reply to the call they carry.
//
// Ports packages/codemode/src/runtime/remote.ts (remoteLauncher: exchange).
func (c *remoteChannel) exchange(_ context.Context, messages []RemoteMessage, reply func(*RemoteReply, error)) {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		reply(&RemoteReply{HostToWorkerMessage{Type: MessageStop}}, nil)
		return
	}
	// Register the waiter first: the host may answer a call synchronously, for example when the tool fails before it
	// returns.
	answered := false
	for _, message := range messages {
		if message.Type == MessageCall {
			c.waiting[message.ID] = reply
			answered = true
			break
		}
	}
	c.mu.Unlock()
	if !answered {
		reply(nil, nil)
	}
	for _, message := range messages {
		if message.Type == MessageFailure {
			c.mu.Lock()
			c.settled = true
			c.mu.Unlock()
			failure := message.Failure
			if failure == nil {
				failure = &Error{Kind: ErrorSandbox}
			}
			c.events.failure(failure)
			continue
		}
		// A malformed message still goes to the host, which fails the execution as a broken bridge.
		c.events.message(messageFromWire(message.WorkerToHostMessage))
		if message.Type == MessageDone || message.Type == MessageCrash {
			c.mu.Lock()
			c.settled = true
			c.mu.Unlock()
		}
	}
}

// start does nothing: the remote reports through exchange once Remote.Execute runs.
func (c *remoteChannel) start() {}

func (c *remoteChannel) post(result callResult) {
	c.mu.Lock()
	reply := c.waiting[result.id]
	delete(c.waiting, result.id)
	c.mu.Unlock()
	if reply != nil {
		reply(replyToWire(result), nil)
	}
}

// stop answers every pending exchange with a stop reply, so the remote ends its VM at that point, and cancels the
// context of Remote.Execute. It does not wait for the remote.
func (c *remoteChannel) stop() {
	c.mu.Lock()
	c.stopped = true
	waiting := c.waiting
	c.waiting = map[int]func(*RemoteReply, error){}
	c.mu.Unlock()
	for _, reply := range waiting {
		reply(&RemoteReply{HostToWorkerMessage{Type: MessageStop}}, nil)
	}
	c.cancel()
}

// messageFromWire decodes a message for the host. A message that is not one of the four types decodes to the empty
// kind, which the host fails as a broken bridge (`isWorkerToHostMessage` upstream checks only the type).
func messageFromWire(message WorkerToHostMessage) vmMessage {
	switch message.Type {
	case MessageCall:
		args, defined := derefString(message.Args)
		return vmMessage{kind: vmMessageCall, id: message.ID, isTool: message.Target == "tool", name: message.Name, args: args, argsDefined: defined}
	case MessageOutput:
		if message.Item == nil {
			return vmMessage{kind: vmMessageOutput}
		}
		return vmMessage{kind: vmMessageOutput, item: *message.Item}
	case MessageDone:
		errorText, _ := derefString(message.Error)
		value, valueDefined := derefString(message.Value)
		writes, writesDefined := derefString(message.Writes)
		return vmMessage{kind: vmMessageDone, ok: message.Ok, errorText: errorText, value: value, valueDefined: valueDefined, writes: writes, writesDefined: writesDefined, during: message.During}
	case MessageCrash:
		return vmMessage{kind: vmMessageCrash, message: message.Message}
	case MessageStarted:
		return vmMessage{kind: vmMessageStarted, fingerprint: message.Fingerprint}
	case MessageIdle:
		return vmMessage{kind: vmMessageIdle, seq: message.Seq, issued: message.Issued}
	}
	return vmMessage{}
}

func replyToWire(result callResult) *RemoteReply {
	reply := HostToWorkerMessage{Type: MessageResult, ID: result.id, Ok: result.ok, Payload: stringPtr(result.payload, result.defined), Properties: stringPtr(result.properties, result.propertiesDefined)}
	if result.seq != 0 {
		reply.Seq = &result.seq
	}
	if result.hasNow {
		reply.Now = &result.now
	}
	return &RemoteReply{reply}
}
