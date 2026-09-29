package experimental

// Ports packages/coding-agent/src/experimental/client-runtime.ts (runtime/service-source contracts).

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// ServerServiceSource supplies server services and their replicated connection state. Both network and loopback presentations use this contract.
type ServerServiceSource interface {
	chord.RemoteServiceSource
	Connection() pico3.ReplicatedStateOf[*services.ServerConnectionState]
	Dispose(context.Context) error
}

// SessionServiceSource supplies the selected attachment's services. Readiness waits are fenced to the requested Session generation by the source implementation.
type SessionServiceSource interface {
	chord.RemoteServiceSource
	Attachment() pico3.ReplicatedStateOf[*services.SessionAttachmentState]
	WhenAttached(context.Context, string) error
	WhenDetached(context.Context) error
	Dispose(context.Context) error
}

// ClientRuntimeRoute is a selected Unix route with its socket path and logical server ID.
type ClientRuntimeRoute struct {
	Transport string  `json:"transport"`
	ServerId  string  `json:"serverId"`
	Path      *string `json:"path,omitempty"`
}

// ClientRuntimeServer owns one connected client and its server/Session service namespaces.
type ClientRuntimeServer struct {
	Route   ClientRuntimeRoute
	Client  *client.Client
	Server  ServerServiceSource
	Session SessionServiceSource
}

// ActivatedClientRuntimeServer holds the built-in service views used by a non-interactive presentation.
type ActivatedClientRuntimeServer struct {
	*ClientRuntimeServer
	Directory  services.SessionDirectory
	Management services.SessionManagement
	Plugins    services.PresentationPlugins
	Models     services.Models
	Agent      services.AgentController
	Transcript services.Transcript
}

// OpenClientRuntimeOptions preserves an omitted search directory separately from an explicitly empty path.
type OpenClientRuntimeOptions struct {
	Directory *string
}
