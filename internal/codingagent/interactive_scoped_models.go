package codingagent

import "github.com/MichaelKinsy/PiG/coding/extension"

type scopedModelSession interface {
	ScopedModels() []extension.ScopedModel
}

// extensionScopedModels reads the Session's already-resolved scope; SDK queries do not resolve catalogs or credentials.
func (m *InteractiveMode) extensionScopedModels() []extension.ScopedModel {
	if session, ok := m.opts.SessionHandle.(scopedModelSession); ok {
		return session.ScopedModels()
	}
	return []extension.ScopedModel{}
}
