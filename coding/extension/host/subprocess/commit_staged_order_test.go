package subprocess

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Reload stops the replaced generations one after another and then the removed extensions, so the providers they registered leave the registry in that order.
func TestCommitStagedUnregistersProvidersInReloadOrder(t *testing.T) {
	const replaced, removed = 8, 8
	for range 20 {
		h := NewHost(t.TempDir())
		var order []string
		h.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(name string) { order = append(order, name) })
		var staged []stagedManagedExt
		var gone []*managedExt
		var want []string
		for i := range replaced {
			name := fmt.Sprintf("replaced-%d", i)
			old := &managedExt{config: ExtConfig{Name: name}, providerNames: []string{"old-" + name}}
			h.exts[name] = old
			staged = append(staged, stagedManagedExt{name: name, me: &managedExt{config: ExtConfig{Name: name}}})
			want = append(want, "old-"+name)
		}
		for i := range removed {
			name := fmt.Sprintf("removed-%d", i)
			old := &managedExt{config: ExtConfig{Name: name}, providerNames: []string{"old-" + name}}
			h.exts[name] = old
			gone = append(gone, old)
			want = append(want, "old-"+name)
		}
		h.commitStaged(staged, gone, "reload replaced")
		if !slices.Equal(order, want) {
			t.Fatalf("providers unregistered as %v, want %v", order, want)
		}
	}
}
