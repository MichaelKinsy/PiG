package plugins

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// No Pi file corresponds to this one: the Go side of how a Node host's parent link is attached (docs/plan/
// chord-plugins-node-evaluator.md, 10.9).

// nodeLink is NodeHost.Link: the link to the process's host, attached lazily. The host `connect`s its parent link, and its
// heartbeat starts, when the Go host first uses this end (OnMessage, which a host's channel calls to listen, or Send): the
// first use sends the `attach` control request, once, before the use itself, on the one channel the link's messages travel,
// so the host has connected before the first link message reaches it. A host nothing attaches has no link and no clock.
// Close and OnClose attach nothing: closing an unattached link ends the channel and with it the process.
type nodeLink struct {
	link.Link
	node *NodeHost
	once sync.Once

	mu sync.Mutex
	// await is the wait for the Go host that took the link (host.DeliveryAwaiter); nil until a host took it.
	await func(ctx context.Context) error
}

var _ host.DeliveryAwaiter = (*nodeLink)(nil)

func newNodeLink(node *NodeHost, inner link.Link) *nodeLink {
	return &nodeLink{Link: inner, node: node}
}

// attach asks the process's host to connect its parent link, the first time it is called. A channel that ended is a closed
// link: there is nothing to attach.
func (l *nodeLink) attach() {
	l.once.Do(func() { _ = l.node.port.Load().control(map[string]any{"op": "attach"}) })
}

// Send attaches the link, then sends one message.
func (l *nodeLink) Send(message []byte) {
	l.attach()
	l.Link.Send(message)
}

// OnMessage attaches the link, then sets the listener.
func (l *nodeLink) OnMessage(listener func(message []byte)) {
	l.attach()
	l.Link.OnMessage(listener)
}

// Taken is host.DeliveryAwaiter's: the Go host that took the link says how to wait for its deliveries. A link the app wraps
// before it gives it to the host hides this method, and the waits of Load and Reload then do not reach that host: wrap it
// with an end that also implements host.DeliveryAwaiter and forwards Taken.
func (l *nodeLink) Taken(await func(ctx context.Context) error) {
	l.mu.Lock()
	l.await = await
	l.mu.Unlock()
}

// delivered waits until the Go host the link is attached to has delivered what the link handed it so far: each frame the
// process sent before the reply that was just read was handed to the host by the reader before the reply was decoded, so
// what is left is the host's own queue (host.DeliveryAwaiter). It returns at once when no host took the link. ctx ends the
// wait only: what the wait was for is a courtesy to the caller, the request itself has been answered.
func (l *nodeLink) delivered(ctx context.Context) {
	l.mu.Lock()
	await := l.await
	l.mu.Unlock()
	if await != nil {
		_ = await(ctx)
	}
}
