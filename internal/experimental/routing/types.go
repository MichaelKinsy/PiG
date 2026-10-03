// Package routing hosts contract-agnostic experimental server and client routing.
package routing

// Ports packages/server/src/types.ts.
// Ports packages/server/src/connection.ts.
// Ports packages/server/src/listener.ts.

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// ByteConnection is an established, authorized ordered byte connection. Send waits for completion; Close sends a non-nil final chunk before closing.
type ByteConnection interface {
	Closed() bool
	Send([]byte) error
	Close(finalChunk []byte) error
}

// ByteConnectionHandler receives ordered transport callbacks.
type ByteConnectionHandler struct {
	OnData  func([]byte)
	OnClose func()
	OnError func(error)
}

// ByteConnectionAcceptor attaches a handler to an authorized connection.
type ByteConnectionAcceptor func(ByteConnection) ByteConnectionHandler

// ServerListener supplies authorized byte connections. Start and Close await their transport operations.
type ServerListener interface {
	Start(ByteConnectionAcceptor) error
	Close() error
}

// RoutedServerPresentation is the presentation-scoped management capability shared with the service provider.
type RoutedServerPresentation = services.RoutedServerPresentation

// RoutedServerServiceAttachment owns the server service endpoint for one connection.
type RoutedServerServiceAttachment interface {
	InvokeService(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (json.RawMessage, error)
	Release(context.Context) error
}

// RoutedSessionAttachment has the same service-invocation and release contract as a server attachment.
type RoutedSessionAttachment = RoutedServerServiceAttachment

// RoutedServerServiceHost opens a server service endpoint for one presentation.
type RoutedServerServiceHost interface {
	AttachClient(context.Context, RoutedServerPresentation) (RoutedServerServiceAttachment, error)
}

// RoutedSessionHandle acquires presentation-scoped Session capabilities. Terminated closes once; TerminalError retains its result for every watcher. A nil Terminated channel represents an omitted termination promise.
type RoutedSessionHandle interface {
	AttachClient(context.Context) (RoutedSessionAttachment, error)
	Terminated() <-chan struct{}
	TerminalError() error
	Close(context.Context) error
}

// SessionMetadata is the application's metadata of a hosted Session; routing requires only its ID (packages/server/src/types.ts SessionMetadata). The metadata ResolveSession returns is the value OpenSession receives, so an application asserts its own concrete type there.
type SessionMetadata interface {
	SessionID() string
}

// BasicSessionMetadata is the minimal SessionMetadata: only an ID.
type BasicSessionMetadata struct{ ID string }

func (metadata BasicSessionMetadata) SessionID() string { return metadata.ID }

// ServerHost supplies application Session resolution and opening. Routing does not own the durable storage implementation.
type ServerHost struct {
	ServerServices RoutedServerServiceHost
	ResolveSession func(context.Context, string) (SessionMetadata, error)
	OpenSession    func(context.Context, SessionMetadata) (RoutedSessionHandle, error)
}

// ServerOptions selects listeners, logical identity, and bounded framing/handshake admission. Nil numeric options select Pi's defaults; explicit zero is preserved.
type ServerOptions struct {
	Listeners                []ServerListener
	ServerId                 string
	MaxFrameLength           *float64
	HandshakeTimeoutMs       *float64
	OnConnectionCountChanged func(int)
	OnError                  func(error)
}
