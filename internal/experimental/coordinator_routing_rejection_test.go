package experimental

import (
	"fmt"
	"testing"
)

// coordinator.ts:405 and :435: the peer id "server" is reserved, and only the current server may broadcast. Either
// error destroys the sending connection (coordinator.ts:541-542) and routes nothing.
func TestCoordinatorRejectsTheServerPeerIDAndBroadcastsFromPeers(t *testing.T) {
	for _, oracle := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream=%t", oracle), func(t *testing.T) {
			_, control, _ := startTestCoordinator(t, oracle)
			reserved := controlDial(t, control)
			reserved.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "server"})
			reserved.wantClosed(t)

			worker := controlDial(t, control)
			worker.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "worker"})
			worker.want(t, `{"type":"peer_registered","peerId":"worker"}`)
			other := controlDial(t, control)
			other.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "other"})
			other.want(t, `{"type":"peer_registered","peerId":"other"}`)

			worker.send(t, map[string]any{"type": "broadcast", "payload": "from a peer"})
			worker.wantClosed(t)
			// The next line other receives is its own message, so the broadcast reached no peer.
			other.send(t, map[string]any{"type": "send", "to": "other", "payload": "after"})
			other.want(t, `{"type":"message","from":"other","payload":"after"}`)
		})
	}
}
