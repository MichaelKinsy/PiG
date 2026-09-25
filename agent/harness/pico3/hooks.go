package pico3

import (
	"slices"
	"sync"
)

// hookRegistration binds one handler set to a kind and a namespace, either
// harness-wide or to a conversation (and optionally its subtree).
type hookRegistration struct {
	namespace      *Namespace
	kind           *Kind
	handlers       any
	conversationId *Id
	subtree        bool
}

type hookRegistry struct {
	mu            sync.Mutex
	registrations []*hookRegistration
}

func (registry *hookRegistry) add(registration *hookRegistration) func() {
	registry.mu.Lock()
	registry.registrations = append(registry.registrations, registration)
	registry.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			registry.mu.Lock()
			defer registry.mu.Unlock()
			registry.registrations = slices.DeleteFunc(registry.registrations, func(candidate *hookRegistration) bool { return candidate == registration })
		})
	}
}

func (registry *hookRegistry) removeNamespace(namespace *Namespace) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.registrations = slices.DeleteFunc(registry.registrations, func(candidate *hookRegistration) bool { return candidate.namespace == namespace })
}

func (registry *hookRegistry) snapshot() []*hookRegistration {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return slices.Clone(registry.registrations)
}

// runnerFor returns the hook runner for one kind invocation.
func (registry *hookRegistry) runnerFor(kind *Kind, api HookApi, ancestors func(Id) []Id, onReport func(error)) HookRunner {
	api.Kind = kind.Name
	return HookRunner{
		onReport: onReport,
		handlers: func() []HookBinding {
			var bindings []HookBinding
			for _, registration := range registry.snapshot() {
				if registration.kind != kind || !registration.matches(api.ConversationId, ancestors) {
					continue
				}
				bindings = append(bindings, HookBinding{Handlers: registration.handlers, Namespace: registration.namespace, Api: api})
			}
			return bindings
		},
	}
}

func (registration *hookRegistration) matches(conversationId Id, ancestors func(Id) []Id) bool {
	if registration.conversationId == nil || *registration.conversationId == conversationId {
		return true
	}
	return registration.subtree && slices.Contains(ancestors(conversationId), *registration.conversationId)
}
