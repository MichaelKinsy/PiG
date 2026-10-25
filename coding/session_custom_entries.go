package coding

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// AppendCustomEntry records extension-owned data as a custom entry on the active branch and reports it as an
// `entry_appended` event, like the entries the core appends for its own state. It is the session behind upstream's
// `pi.appendEntry()`, for built-in extensions that reach the session through `ctx.sessionManager`.
//
// upstream: agent-session.ts (appendCustomEntry through the extension runner's appendEntry action)
func (s *Session) AppendCustomEntry(customType string, data any) (string, error) {
	return s.AppendExtensionEntry(customType, data, nil)
}

// AppendExtensionEntry records an extension host's appendEntry request on the active branch. A nil direct allocates an ID and timestamp; otherwise it preserves the supplied identity and rejects an empty or duplicate ID or an invalid RFC3339 timestamp. Identity validation, parent selection and append share the Session log's mutation lock.
// It emits entry_appended after a successful append when direct is nil or direct.Event is true. A direct append with Event false only writes the log, as ctx.sessionManager.appendCustomEntry does.
// Filesystem errors retain the native writer's semantics: the entry may already be in memory, but no entry_appended event is emitted.
// pig additive (D19): embedding hosts can persist the subprocess bridge's entry identity through the native Session writer.
func (s *Session) AppendExtensionEntry(customType string, data any, direct *subprocess.DirectEntryAppend) (string, error) {
	appended, err := icodingagent.AppendExtensionEntry(s.inner, customType, data, direct)
	if err != nil {
		return "", err
	}
	if direct == nil || direct.Event {
		if entry, ok := s.inner.GetEntry(appended.ID); ok {
			s.emitEvent(agent.EntryAppendedEvent{Entry: entry.Raw()})
		}
	}
	return appended.ID, nil
}
