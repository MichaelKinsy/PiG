package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// writePackedFactoryModuleWithProvider scaffolds a factory-style Go module
// that registers exactly one provider in addition to a no-op tool. Used for
// packed-cell provider lifecycle tests.
func writePackedFactoryModuleWithProvider(t *testing.T, modulePath, extName, toolName, providerName string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`package ext

import "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Tool(%q, "noop", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]any{"content": "ok"}, nil
	})
	e.RegisterProvider(%q, sdk.ProviderConfig{"baseUrl": "http://localhost"})
	return e
}
`, extName, toolName, providerName)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPackedCell_ProviderLifecycle ensures that packed-cell members can
// register providers, that reload preserves them when packed artifacts are
// reused, and that quarantine unregisters every packed-member provider.
//
// pig-specific. Production-grade reload safety: globally-visible provider
// registrations from packed members must not leak after quarantine.
func TestPackedCell_ProviderLifecycle(t *testing.T) {
	rootA := writePackedFactoryModuleWithProvider(t, "example.com/provpack/a", "provpack-a", "tool_a", "prov-a")
	rootB := writePackedFactoryModuleWithProvider(t, "example.com/provpack/b", "provpack-b", "tool_b", "prov-b")
	configs := []ExtConfig{
		packedFactoryConfig("provpack-a", rootA, "example.com/provpack/a", "ha"),
		packedFactoryConfig("provpack-b", rootB, "example.com/provpack/b", "hb"),
	}

	var (
		mu         sync.Mutex
		registered = map[string]extension.ProviderConfig{}
	)
	h := NewHost(t.TempDir())
	h.SetProviderCallbacks(
		func(name string, cfg extension.ProviderConfig) {
			mu.Lock()
			defer mu.Unlock()
			registered[name] = cfg
		},
		func(name string) {
			mu.Lock()
			defer mu.Unlock()
			delete(registered, name)
		},
	)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("first reload: %v", err)
	}

	check := func(want []string) {
		t.Helper()
		mu.Lock()
		got := make([]string, 0, len(registered))
		for k := range registered {
			got = append(got, k)
		}
		mu.Unlock()
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("provider set = %v, want %v", got, want)
		}
		for i, name := range want {
			if got[i] != name {
				t.Fatalf("provider set = %v, want %v", got, want)
			}
		}
	}
	check([]string{"prov-a", "prov-b"})

	// Reload with identical config: packed artifact is reused; provider set must
	// remain stable (no double-register, no temporary unregister).
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	check([]string{"prov-a", "prov-b"})

	// Quarantine the packed cell → all packed-member providers must be
	// unregistered when the host stops the quarantined extensions.
	h.mu.Lock()
	packedKey := h.exts["provpack-a"].packedCellKey
	h.mu.Unlock()
	h.quarantinePackedCell(packedKey, "boom")
	check(nil)
}
