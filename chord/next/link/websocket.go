package link

import "sync"

// Ports packages/chord/src/next/link/websocket.ts. WebSocket links (§3): a link over a WebSocket, the browser's or
// `ws` in Node, typed structurally; binary frames, messages sent while it still connects buffered until it opens.

// WebSocketEvent names the events a link listens for.
type WebSocketEvent string

const (
	WebSocketOpen    WebSocketEvent = "open"
	WebSocketClose   WebSocketEvent = "close"
	WebSocketError   WebSocketEvent = "error"
	WebSocketMessage WebSocketEvent = "message"
)

// WebSocketLike is the part of a WebSocket a link uses. A message event's data is `[]byte` for a binary frame and
// anything else (a string) for a text frame.
type WebSocketLike interface {
	// ReadyState is 0 connecting, 1 open, 2 closing, 3 closed.
	ReadyState() int
	SetBinaryType(binaryType string)
	// Send sends one binary frame; a non-nil error is a thrown send.
	Send(data []byte) error
	Close()
	// AddEventListener registers a listener (data is the frame of a message event, nil for the others) and returns
	// its removal.
	AddEventListener(event WebSocketEvent, listener func(data any)) (remove func())
}

const (
	socketConnecting = 0
	socketOpen       = 1
)

// WebSocketLink is a link over socket, one binary frame per message (binaryType `arraybuffer`). Messages sent while it
// connects wait for `open`; a socket closing or closed already gives a closed link; `close` and `error` close the link
// once, and so does a text frame (no message of this protocol). A frame above the transport's maximum payload closes
// the socket (code 1009), which closes the link.
func WebSocketLink(socket WebSocketLike) Link {
	end := &webSocketLink{}
	if state := socket.ReadyState(); state != socketConnecting && state != socketOpen {
		end.ended()
		return end
	}
	end.socket = socket
	socket.SetBinaryType("arraybuffer")
	end.mu.Lock()
	if socket.ReadyState() == socketConnecting {
		end.connecting = [][]byte{}
		end.connectingSet = true
	}
	end.mu.Unlock()
	if end.connectingSet {
		end.removeOpen = socket.AddEventListener(WebSocketOpen, func(any) { end.opened() })
	}
	end.removeMessage = socket.AddEventListener(WebSocketMessage, func(data any) {
		if frame, ok := data.([]byte); ok {
			end.deliver(frame)
		} else {
			end.Close()
		}
	})
	// `error` and `close` stay handled until the socket's own close, after this end closed: `ws` emits an `error` for a
	// socket closed while it connects, and an unhandled one would end the process.
	var removeClose, removeError func()
	removeClose = socket.AddEventListener(WebSocketClose, func(any) {
		removeClose()
		removeError()
		end.end()
	})
	removeError = socket.AddEventListener(WebSocketError, func(any) { end.end() })
	return end
}

type webSocketLink struct {
	LinkEnd
	mu sync.Mutex
	// socket is the socket while the link is open.
	socket WebSocketLike
	// connecting holds the messages sent while the socket connects; connectingSet is false once it is open.
	connecting    [][]byte
	connectingSet bool
	removeOpen    func()
	removeMessage func()
}

func (end *webSocketLink) opened() {
	end.mu.Lock()
	queued := end.connecting
	end.connecting, end.connectingSet = nil, false
	end.mu.Unlock()
	for _, message := range queued {
		end.Send(message)
	}
}

func (end *webSocketLink) Send(message []byte) {
	end.mu.Lock()
	socket := end.socket
	if socket == nil {
		end.mu.Unlock()
		return
	}
	if end.connectingSet {
		end.connecting = append(end.connecting, message)
		end.mu.Unlock()
		return
	}
	end.mu.Unlock()
	if err := socket.Send(message); err != nil {
		end.Close()
	}
}

func (end *webSocketLink) Close() {
	end.mu.Lock()
	socket := end.socket
	end.mu.Unlock()
	if socket == nil {
		return
	}
	end.end()
	socket.Close()
}

// end stops listening for frames, drops what waits to be sent and closes this end.
func (end *webSocketLink) end() {
	end.mu.Lock()
	socket := end.socket
	if socket == nil {
		end.mu.Unlock()
		return
	}
	end.socket = nil
	end.connecting, end.connectingSet = nil, false
	removeOpen, removeMessage := end.removeOpen, end.removeMessage
	end.mu.Unlock()
	if removeOpen != nil {
		removeOpen()
	}
	if removeMessage != nil {
		removeMessage()
	}
	end.ended()
}
