package codingagent

import (
	"sync/atomic"
	"testing"
)

// Pi's registerNativeProvider binding refreshes the Session's active model from the registry after the registration (agent-session.ts), as registerProvider's does, and the Session does it from the registry's observers: a native registration reports to them, so an active model of the provider is the registered object at the next read.
func TestRegisterNativeProviderReportsToTheRegistryObservers(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	var reports atomic.Int32
	stop := registry.ObserveChanges(func() { reports.Add(1) })
	t.Cleanup(stop)
	var checks atomic.Int32
	if err := registry.RegisterNativeProvider(t.Context(), countingNativeProvider(&checks)); err != nil {
		t.Fatal(err)
	}
	if reports.Load() == 0 {
		t.Fatal("a native provider registration never reached the registry observers")
	}
}
