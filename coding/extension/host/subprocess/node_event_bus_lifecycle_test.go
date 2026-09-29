package subprocess

import (
	"testing"
)

// Ports packages/coding-agent/test/suite/regressions/7193-event-bus-lifecycle.test.ts
// ("removes extension-owned event-bus listeners on reload and dispose"):
// an extension's pi.events.on() listener is called once per emit before any
// reload, once after each reload (the old generation's listener is gone, not
// duplicated), and never after the host is shut down. The upstream host-owned
// listener half has no PiG counterpart: the Go host exposes no event bus and the Node
// bus is shared with every Node realm (D83), so the survival of a host-side listener is not
// observable here; a reload re-invokes the factory in the retained process and
// retires the old generation with its connection. The old generation's captured API
// is checked through its handler, which must not reach the new generation.
func TestNodeEventBusListenersRemovedOnReloadAndShutdown(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(map[int]string{1: "single", 2: "two-members"}[count], func(t *testing.T) {
			h, configs, _ := nodeRecoveryFixture(t, count)
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			want := "member0 | member0"
			if count == 2 {
				want = "member0,member1 | member0+member1"
			}
			if got := recoveryBus(t, h, "member0"); got != want {
				t.Fatalf("before reload: %s, want %s", got, want)
			}
			h.mu.Lock()
			stale := h.exts["member0"].ext.EventHandlers("session_start")[0]
			execute := h.exts["member0"].ext.Tools["member0"].Definition.Execute
			h.mu.Unlock()
			for i := 1; i <= 2; i++ {
				if _, err := h.Reload(t.Context()); err != nil {
					t.Fatalf("reload %d: %v", i, err)
				}
				if got := recoveryBus(t, h, "member0"); got != want {
					t.Fatalf("after reload %d: %s, want %s (old listeners must be removed, live ones called once)", i, got, want)
				}
			}
			if _, err := stale(); err == nil {
				t.Fatal("old-generation extension API reached the reloaded process")
			}
			h.Shutdown("dispose")
			if _, err := execute(t.Context(), "after-dispose", []byte(`{}`), nil); err == nil {
				t.Fatal("extension listener still reachable after dispose")
			}
		})
	}
}
