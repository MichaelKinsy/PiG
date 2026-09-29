package coding

import (
	"slices"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/src/core/agent-session.ts.
// ScopedModel pairs a native cycling model with its optional thinking preference.
type ScopedModel = extension.ScopedModel

type sessionScopedModels struct {
	value atomic.Pointer[[]ScopedModel]
}

// ScopedModels returns the current read-only cycling scope without copying its list or models. An empty scope means all available models.
func (s *Session) ScopedModels() []ScopedModel {
	if value := s.scopedModels.value.Load(); value != nil {
		return *value
	}
	return []ScopedModel{}
}

// SetScopedModels replaces the session-only cycling scope, retaining the supplied list and model identities without changing settings. Nil means an empty scope.
func (s *Session) SetScopedModels(models []ScopedModel) {
	if models == nil {
		models = []ScopedModel{}
	}
	s.scopedModels.value.Store(&models)
}

// addPersistedDefaultToNonEmptyScope appends a missing default without mutating any previously returned list.
// upstream: packages/coding-agent/src/core/agent-session.ts:_addPersistedDefaultToNonEmptyScope
func (s *Session) addPersistedDefaultToNonEmptyScope(model *ai.Model) error {
	for {
		previous := s.scopedModels.value.Load()
		if previous == nil || len(*previous) == 0 {
			return nil
		}
		if slices.ContainsFunc(*previous, func(entry ScopedModel) bool {
			return entry.Model != nil && entry.Model.ID == model.ID && providerID(entry.Model) == providerID(model)
		}) {
			return nil
		}
		next := append(slices.Clone(*previous), ScopedModel{Model: model})
		if s.scopedModels.value.CompareAndSwap(previous, &next) {
			break
		}
	}
	settings := s.services.SettingsManager()
	enabled := settings.GetEnabledModels()
	reference := providerID(model) + "/" + model.ID
	if len(enabled) == 0 || slices.ContainsFunc(enabled, func(pattern string) bool { return strings.EqualFold(pattern, reference) }) {
		return nil
	}
	enabled = append(slices.Clone(enabled), reference)
	return settings.UpdateGlobal(func(value *Settings) { value.EnabledModels = enabled })
}
