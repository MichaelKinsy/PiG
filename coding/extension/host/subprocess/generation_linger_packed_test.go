package subprocess

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A member of a shared Go cell that a reload removes keeps its provider as an isolated one does (Pi's reload never unregisters a provider, model-runtime.ts:965-993):
// the generation lingers with its connection and its reference on the cell's process, the cell's other members are replaced as any reload replaces them, and
// Shutdown ends the process the removed member held.
func TestPackedGoMemberRemovedByAReloadKeepsItsProviderUntilShutdown(t *testing.T) {
	rootA := writePackedFactoryModuleWithProvider(t, "example.com/lingerpack/a", "lingerpack-a", "tool_a", "prov-a")
	rootB := writePackedFactoryModuleWithProvider(t, "example.com/lingerpack/b", "lingerpack-b", "tool_b", "prov-b")
	configs := []ExtConfig{
		packedFactoryConfig("lingerpack-a", rootA, "example.com/lingerpack/a", "ha"),
		packedFactoryConfig("lingerpack-b", rootB, "example.com/lingerpack/b", "hb"),
	}
	var (
		mu           sync.Mutex
		registered   = map[string]struct{}{}
		unregistered []string
	)
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return slices.Clone(configs), nil })
	bind := func() {
		h.SetProviderCallbacks(func(name string, _ extension.ProviderConfig) error {
			mu.Lock()
			defer mu.Unlock()
			registered[name] = struct{}{}
			return nil
		}, func(name string) {
			mu.Lock()
			defer mu.Unlock()
			delete(registered, name)
			unregistered = append(unregistered, name)
		})
	}
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := buildContext(t)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	bind()
	configs = configs[:1]
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	bind()
	mu.Lock()
	_, hasB := registered["prov-b"]
	gone := slices.Clone(unregistered)
	mu.Unlock()
	if !hasB || slices.Contains(gone, "prov-b") {
		t.Fatalf("the removed member's provider left the registry (registered %v, unregistered %v)", hasB, gone)
	}
	h.mu.Lock()
	lingering := make([]*managedExt, 0, len(h.lingering))
	for me := range h.lingering {
		lingering = append(lingering, me)
	}
	h.mu.Unlock()
	if len(lingering) != 1 || lingering[0].config.Name != "lingerpack-b" {
		t.Fatalf("lingering generations %d, want the removed member alone", len(lingering))
	}
	ended := lingering[0].packedProcess.startWait()
	select {
	case <-ended:
		t.Fatal("the removed member's process ended with the reload")
	default:
	}
	h.Shutdown("lingering packed shutdown")
	select {
	case <-ended:
	case <-time.After(testTimeout(t, 10*time.Second)):
		t.Fatal("Shutdown left the removed member's process running")
	}
}
