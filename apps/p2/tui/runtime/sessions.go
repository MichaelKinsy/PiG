package runtime

// What optional plugins add to the TUI's session selector (p2-tui-port.md section 1.17): the subagents plugin its subagent conversations (threaded sort), the
// search plugin literal message matches. A registry, so the sessions plugin does not depend on either (it works without them loaded).
//
// pi: apps/p2/src/runtime/tui-sessions.ts

import p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"

// SessionChild is a named subagent conversation of a session.
type SessionChild struct {
	Name           string
	ConversationID int
	Model          string
}

// SessionHit is a literal message match.
type SessionHit struct {
	Session        string
	ConversationID int
	// Role is "user" or "assistant".
	Role  string
	Text  string
	Title string
}

// TuiSessionSource is one contribution to the session selector; a nil member is not provided.
type TuiSessionSource struct {
	// Children reports the named subagent conversations by session ID, as they change; it returns the stop function.
	Children func(runtime *TuiRuntime, onChange func(children map[string][]SessionChild)) (stop func())
	// Search is the literal message matches for a query (owner).
	Search func(runtime *TuiRuntime, query string, archived bool) ([]SessionHit, error)
}

// TuiSessionSources are the session selector's sources.
var TuiSessionSources = p2runtime.NewKeyedRegistry[TuiSessionSource]("p2.tui-session-sources")

// SessionWhere is where a session runs.
type SessionWhere struct {
	// Env is the environment; nil is this device.
	Env *string
	Cwd string
}

// TuiSessionPlace is where the viewed session runs, and moving it (the sessions plugin): `/envs` (the envs plugin) marks the session's environment and moves the
// session on Enter without depending on the sessions plugin.
type TuiSessionPlace struct {
	// Where is the viewed session's environment and directory; nil without a session.
	Where func(ctx TuiContext) *SessionWhere
	// MoveTo moves the viewed session to env (nil: this device): the directory prompt there (its note confirms), then `Sessions.relocate`.
	MoveTo func(ctx TuiContext, env *string) error
}

// TuiSessionPlaces are the places.
var TuiSessionPlaces = p2runtime.NewKeyedRegistry[TuiSessionPlace]("p2.tui-session-places")
