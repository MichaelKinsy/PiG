package plugins

import (
	"sync"

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
}

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
