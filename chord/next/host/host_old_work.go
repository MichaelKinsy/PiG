package host

import (
	"context"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Ports packages/chord/src/host/old-work.ts. Old work during a reload (decision "Reload restarts every affected object; old
// work finishes on old code"): the calls running on a restart's open old attempt and every call they cause. A call is old
// work when its call chain (the method ctxs it runs under, found through its context) holds an attempt that a restart
// drains, or when it arrived as old work from another host (`call.old` on a trusted link). The host counts its open old
// attempts and the old calls it runs for other hosts, so outside reloads no chain is walked.
//
// Forced Go differences. `CALL_CHAIN` and `OPENING` are chordctx keys; a method call's ctx answers `CALL_CHAIN` with itself
// (callContext.Value), as in Pi. `chainAttempts` returns a slice in first-seen order (Pi's Set keeps insertion order).

// callNode is one link of a call chain: the attempt a method call runs on, and the chain of the call that made it (up).
type callNode interface {
	// on is the attempt the call runs on: old work while that attempt drains for a restart.
	on() oldWorkSource
	// up is the chain of the call that made this one; nil for the first.
	up() callNode
}

// oldWorkSource is what a call chain's node runs on: an attempt, or another host's old work.
type oldWorkSource interface{ draining() bool }

// callChainKey is the key under which a context carries its call chain (a method ctx is its own node: no allocation per
// call).
var callChainKey = chordctx.NewKey[callNode]("chord.callChain")

// openingKey is the key under which an attempt's activation context carries its object: a call its opener makes is known as
// that object's (its gate waits are the object's wait edges for reload cycles).
var openingKey = chordctx.NewKey[*chordObject]("chord.opening")

// oldWork is the host's old work: whether a context runs under it, counted so that the check is free outside reloads. Every
// method runs in a section of the host's loop, where an attempt's `draining` is read.
type oldWork struct {
	// count is the open old attempts of restarts and the old calls from other hosts running here now.
	count int
}

// open: an old attempt began draining, or an old call from another host began running here.
func (w *oldWork) open() { w.count++ }

// close is the counterpart of open.
func (w *oldWork) close() { w.count-- }

// of is whether ctx runs under old work (walked only while this host has any).
func (w *oldWork) of(ctx context.Context) bool {
	if w.count == 0 {
		return false
	}
	for node := chainOf(ctx); node != nil; node = node.up() {
		if node.on().draining() {
			return true
		}
	}
	return false
}

// chainOf is the call chain ctx carries; nil when it has none.
func chainOf(ctx context.Context) callNode {
	node, _ := chordctx.Value(ctx, callChainKey)
	return node
}

// remoteOldWorkNode is the chain node of a call that arrived as old work from another host: always old, with nothing above it.
type remoteOldWorkNode struct{}

type remoteOldWorkSource struct{}

func (remoteOldWorkSource) draining() bool { return true }

func (remoteOldWorkNode) on() oldWorkSource { return remoteOldWorkSource{} }
func (remoteOldWorkNode) up() callNode      { return nil }

// asRemoteOldWork is ctx marked as old work that arrived from another host (`call.old` on a trusted link).
func asRemoteOldWork(ctx context.Context) context.Context {
	return chordctx.WithValue[callNode](ctx, callChainKey, remoteOldWorkNode{})
}

// chainAttempts are the attempts of ctx's call chain, each once in the order met, for the reload cycle check (another host's
// old work has none here).
func chainAttempts(ctx context.Context) []oldWorkSource {
	var attempts []oldWorkSource
	for node := chainOf(ctx); node != nil; node = node.up() {
		if _, remote := node.(remoteOldWorkNode); remote {
			continue
		}
		source := node.on()
		seen := false
		for _, each := range attempts {
			if each == source {
				seen = true
				break
			}
		}
		if !seen {
			attempts = append(attempts, source)
		}
	}
	return attempts
}
