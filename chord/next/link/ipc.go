package link

import "sync"

// Ports packages/chord/src/next/link/ipc.ts. IPC links (§3): a link over a Node IPC channel, a forked child (`fork`
// with `serialization: "advanced"`) or `process` in the child, typed structurally so Chord imports nothing from Node.

// IpcPort is the part of a Node IPC channel a link uses: a `ChildProcess` or the child's `process`. Send returning
// false is backpressure (the message is queued by the channel), not a failure; a non-nil error is a thrown send.
// Messages the port receives are `any`: `[]byte` is a link's, anything else is the app's own.
type IpcPort interface {
	Send(message []byte, callback func(err error)) (bool, error)
	// OnMessage registers a listener and returns its removal (upstream's `on("message")`/`removeListener`).
	OnMessage(listener func(message any)) (remove func())
	// OnDisconnect registers a listener and returns its removal.
	OnDisconnect(listener func()) (remove func())
	Disconnect()
	Connected() bool
}

// IpcLink is a link over port: each message one `[]byte`. It listens from the moment it is made and queues messages
// until its listener is set, so a child makes it before awaiting its start message; messages that are no `[]byte` (the
// app's own, such as that start message) are not the link's and are skipped. A failed `Send`, an error in its callback
// or a disconnect closes the link once; its listeners are removed then.
func IpcLink(port IpcPort) Link {
	end := &ipcLink{port: port}
	if !port.Connected() {
		end.end()
		return end
	}
	removeMessage := port.OnMessage(func(message any) {
		if bytes, ok := message.([]byte); ok {
			end.deliver(bytes)
		}
	})
	removeDisconnect := port.OnDisconnect(end.end)
	end.portMu.Lock()
	end.removeMessage, end.removeDisconnect = removeMessage, removeDisconnect
	closed := end.port == nil
	end.portMu.Unlock()
	if closed {
		// The port disconnected while the listeners were being set.
		removeMessage()
		removeDisconnect()
	}
	return end
}

type ipcLink struct {
	LinkEnd
	// port is the port while the link is open; guarded by LinkEnd's state: set once, cleared by end.
	portMu           sync.Mutex
	port             IpcPort
	removeMessage    func()
	removeDisconnect func()
}

func (end *ipcLink) currentPort() IpcPort {
	end.portMu.Lock()
	defer end.portMu.Unlock()
	return end.port
}

func (end *ipcLink) Send(message []byte) {
	port := end.currentPort()
	if port == nil {
		return
	}
	if _, err := port.Send(message, func(err error) {
		if err != nil {
			end.end()
		}
	}); err != nil {
		end.end()
	}
}

func (end *ipcLink) Close() {
	port := end.currentPort()
	if port == nil {
		return
	}
	end.end()
	// Disconnecting a channel that is gone already would emit an error.
	if port.Connected() {
		port.Disconnect()
	}
}

// end detaches from the port and closes this end.
func (end *ipcLink) end() {
	end.portMu.Lock()
	port := end.port
	removeMessage, removeDisconnect := end.removeMessage, end.removeDisconnect
	end.port, end.removeMessage, end.removeDisconnect = nil, nil, nil
	end.portMu.Unlock()
	if port != nil {
		if removeMessage != nil {
			removeMessage()
		}
		if removeDisconnect != nil {
			removeDisconnect()
		}
	}
	end.ended()
}
