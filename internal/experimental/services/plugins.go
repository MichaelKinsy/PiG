package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// PrepareSessionPluginsRequest selects packages for a Session. Nil PackagePaths selects defaults; an empty slice selects no packages.
type PrepareSessionPluginsRequest struct {
	SessionId    string   `json:"sessionId"`
	PackagePaths []string `json:"packagePaths"`
}

// PresentationPlugins exposes server-built plugin generations to presentations. Calls wait for completion and propagate errors to the caller.
type PresentationPlugins interface {
	PrepareSession(context.Context, PrepareSessionPluginsRequest) (chord.JsonValue, error)
	Reload(context.Context) (chord.JsonValue, error)
}

// PresentationPluginsDefinition is the pi.presentation-plugins token; Go types and values share a namespace.
var PresentationPluginsDefinition = chord.DefineService[PresentationPlugins]("pi.presentation-plugins")

// SessionPlugins reloads plugin facets in the currently attached Session worker. Reload waits for completion and propagates errors to the caller.
type SessionPlugins interface {
	Reload(context.Context) error
}

// SessionPluginsDefinition is the pi.session-plugins token.
var SessionPluginsDefinition = chord.DefineService[SessionPlugins]("pi.session-plugins")
