package cli

import (
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// bindSessionAppendEntry binds the custom-entry append for print and JSON
// mode: upstream's appendEntry and ctx.sessionManager.appendCustomEntry both
// write the session's log there (agent-session.ts _bindExtensionCore). When
// emit is set (JSON mode, whose subscriber prints every session event), pi.appendEntry
// alone also emits the entry_appended event (agent-session.ts:3433-3436).
func bindSessionAppendEntry(bridge *subprocess.UIBridge, current func() *coding.Session, emit func(RPCEntryAppendedEvent)) {
	if bridge == nil {
		return
	}
	var onAppend func(entry codingagent.CustomEntry, direct *subprocess.DirectEntryAppend) error
	if emit != nil {
		onAppend = func(entry codingagent.CustomEntry, direct *subprocess.DirectEntryAppend) error {
			if direct != nil && !direct.Event {
				return nil
			}
			emit(RPCEntryAppendedEvent{
				Type: "entry_appended",
				Entry: RPCEntryAppendedEntry{
					Type: entry.Type, CustomType: entry.CustomType, Data: entry.Data,
					ID: entry.ID, ParentID: entry.ParentID, Timestamp: entry.Timestamp,
				},
			})
			return nil
		}
	}
	codingagent.BindAppendEntryAction(bridge, func() (*codingagent.Session, error) {
		sess := current()
		if sess == nil {
			return nil, coding.ErrSessionNotReady
		}
		return sess.Inner(), nil
	}, onAppend)
}
