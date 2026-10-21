package cli

import (
	"context"
	"sync"
)

// rpcBackgroundGate holds the extension UI calls that no request started while an RPC command's replacement runs from its rebind to its response.
//
// Pi's rpc-mode awaits `runtimeHost.newSession()` and its siblings, whose session_start handlers run on one event loop, and writes the response in the microtasks that follow the last handler. A timer or I/O callback an extension queued in a handler, such as a toast deferred with setTimeout(0), therefore runs after the response. This host's Node runtime is another process: its timer fires while the host's next dispatch crosses the process boundary, so the call reached the output first. The gate keeps such a call, which the Node runtime sends with no parent request once the handler settled, until the response is written (subprocess.Host.SetBackgroundCallGate).
type rpcBackgroundGate struct {
	mu    sync.Mutex
	holds int
	open  chan struct{}
	// commandHolds are the holds of replacements an extension command requested; they end when the last in-flight command answered.
	commandHolds []func()
}

// wait returns nil while no replacement holds the gate, else the channel that closes when the last hold ends.
func (g *rpcBackgroundGate) wait() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.holds == 0 {
		return nil
	}
	return g.open
}

// hold closes the gate until the returned release runs; release is idempotent.
func (g *rpcBackgroundGate) hold() (release func()) {
	g.mu.Lock()
	if g.holds == 0 {
		g.open = make(chan struct{})
	}
	g.holds++
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.holds--
			if g.holds == 0 {
				close(g.open)
			}
		})
	}
}

// rpcReplacementHold is the gate hold of one replacement an RPC command requested. The rebind that builds the replacement's Session takes it (acquire) before it dispatches session_start, and awaitReplacement releases it once the command's response is written, so the calls held in between follow the response.
type rpcReplacementHold struct {
	gate    *rpcBackgroundGate
	mu      sync.Mutex
	release func()
	ended   bool
}

type rpcReplacementHoldKey struct{}

func withRPCReplacementHold(ctx context.Context, hold *rpcReplacementHold) context.Context {
	return context.WithValue(ctx, rpcReplacementHoldKey{}, hold)
}

// acquireRPCReplacementHold closes the gate for the replacement ctx belongs to, once, and reports whether ctx belongs to an RPC command's replacement; a ctx of none holds nothing.
func acquireRPCReplacementHold(ctx context.Context) bool {
	hold, _ := ctx.Value(rpcReplacementHoldKey{}).(*rpcReplacementHold)
	if hold == nil {
		return false
	}
	hold.mu.Lock()
	defer hold.mu.Unlock()
	if hold.release == nil && !hold.ended {
		hold.release = hold.gate.hold()
	}
	return true
}

// end opens the gate again; a hold that was never acquired, or already ended, does nothing.
func (h *rpcReplacementHold) end() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ended = true
	if h.release != nil {
		h.release()
		h.release = nil
	}
}

// holdForCommand closes the gate for a replacement an extension command requested (`await ctx.newSession()`), until the commands in flight have all answered: Pi writes a prompt's response once `await command.handler()` returned, and the deferred calls of the replacement's extensions run after it.
func (g *rpcBackgroundGate) holdForCommand() {
	release := g.hold()
	g.mu.Lock()
	g.commandHolds = append(g.commandHolds, release)
	g.mu.Unlock()
}

// endCommandHolds opens the gate again for the replacements holdForCommand closed it for.
func (g *rpcBackgroundGate) endCommandHolds() {
	g.mu.Lock()
	releases := g.commandHolds
	g.commandHolds = nil
	g.mu.Unlock()
	for _, release := range releases {
		release()
	}
}
