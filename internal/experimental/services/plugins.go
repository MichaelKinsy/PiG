package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// PrepareSessionPluginsRequest selects packages for a Session. Nil PackagePaths selects defaults; an empty slice selects no packages.
type PrepareSessionPluginsRequest struct {
	SessionId    string   `json:"sessionId"`
	PackagePaths []string `json:"packagePaths"`
}

// PresentationPlugins exposes server-built plugin generations to presentations. Calls wait for completion and propagate errors to the caller.
type PresentationPlugins interface {
	PrepareSession(context.Context, PrepareSessionPluginsRequest) (pico3.JsonValue, error)
	Reload(context.Context) (pico3.JsonValue, error)
}

// PresentationPluginsDefinition is the pi.presentation-plugins token; Go types and values share a namespace.
var PresentationPluginsDefinition = pico3.DefineService[PresentationPlugins]("pi.presentation-plugins")

// SessionPlugins reloads plugin facets in the currently attached Session worker. Reload waits for completion and propagates errors to the caller.
type SessionPlugins interface {
	Reload(context.Context) error
}

// SessionPluginsDefinition is the pi.session-plugins token.
var SessionPluginsDefinition = pico3.DefineService[SessionPlugins]("pi.session-plugins")
