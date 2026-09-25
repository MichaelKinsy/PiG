package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// SessionAddress identifies a Session on a server.
type SessionAddress struct {
	ServerId  string `json:"serverId"`
	SessionId string `json:"sessionId"`
}

// SessionSummary adds the creation timestamp in milliseconds.
type SessionSummary struct {
	SessionAddress
	CreatedAt float64 `json:"createdAt"`
}

// SessionCreateOptions distinguishes an omitted id from an explicitly empty id.
type SessionCreateOptions struct {
	Id *string `json:"id,omitempty"`
}

// SessionDirectoryState is the ordered, revisioned set of Sessions advertised by a server.
type SessionDirectoryState struct {
	Revision int              `json:"revision"`
	Sessions []SessionSummary `json:"sessions"`
}

// SessionDirectory exposes replicated Session discovery without mutation operations.
type SessionDirectory interface {
	State() pico3.ReplicatedStateOf[*SessionDirectoryState]
}

// SessionDirectoryDefinition is the pi.session-directory token.
var SessionDirectoryDefinition = pico3.DefineService[SessionDirectory]("pi.session-directory")

// SessionManagement creates, removes, and selects Sessions. Calls wait for completion and return operation errors.
type SessionManagement interface {
	Create(context.Context, SessionCreateOptions) (SessionSummary, error)
	Remove(context.Context, string) error
	Attach(context.Context, string) error
	Detach(context.Context) error
}

// SessionManagementDefinition is the pi.session-management token.
var SessionManagementDefinition = pico3.DefineService[SessionManagement]("pi.session-management")
