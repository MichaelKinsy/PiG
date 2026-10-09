package coding

import (
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// ErrSessionNotReady rejects a session action that an extension calls before the Session exists.
var ErrSessionNotReady = errors.New("agent session not initialized")

// BindExtensionHostSessionActions binds the subprocess host actions that Pi's _bindExtensionCore binds to the Session in every mode: getActiveTools and setActiveTools (getActiveToolNames, setActiveToolsByName) and sendMessage (agent-session.ts:3341-3352). current returns the Session the actions reach, or nil before it exists: getActiveTools then lists no tools, setActiveTools does nothing and sendMessage fails with [ErrSessionNotReady].
func BindExtensionHostSessionActions(bridge *subprocess.UIBridge, current func() *Session) {
	bridge.SetHostAction("sendMessage", func(message extension.CustomMessageRef, opts subprocess.SendMessageOptions) error {
		sess := current()
		if sess == nil {
			return ErrSessionNotReady
		}
		return sess.SendMessage(message, &extension.SendMessageOptions{TriggerTurn: opts.TriggerTurn, DeliverAs: extension.DeliverAs(opts.DeliverAs)})
	})
	bridge.SetHostAction("getActiveTools", func() []string {
		if sess := current(); sess != nil {
			return sess.ActiveToolNames()
		}
		return []string{}
	})
	bridge.SetHostAction("setActiveTools", func(names []string) {
		if sess := current(); sess != nil {
			sess.SetActiveToolsByName(names)
		}
	})
}
