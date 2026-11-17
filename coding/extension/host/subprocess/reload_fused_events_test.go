package subprocess_test

import (
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// A fused member's factory runs in the Host's own process each time the Host starts it, and its pi.events.on is live when the factory
// returns, as in Pi (loader.ts createExtensionAPI events.on). A reload starts every factory again in extension order and retires the old
// generation's listeners, so one emit reaches each live listener once, in extension order (issue 486: the same row as the native SDKs').
func TestHostReloadFusedFactoriesWithFactoryTimeSubscriptionsDeliverOnce(t *testing.T) {
	const members = 8
	var mu sync.Mutex
	var heard []string
	resolver := startupFusedResolver{}
	var configs []subprocess.ExtConfig
	for i := range members {
		name := fmt.Sprintf("fused%d", i)
		configs = append(configs, subprocess.ExtConfig{Name: name, Enabled: true})
		resolver[name] = func(conn net.Conn) error {
			ext := sdk.New(name)
			if _, err := ext.Events().On("reload", func(_ sdk.Context, _ any) error {
				mu.Lock()
				defer mu.Unlock()
				heard = append(heard, name)
				return nil
			}); err != nil {
				return err
			}
			ext.Command("emit", "emit", func(ctx sdk.Context, _ string) error { return ctx.Events().Emit("reload", 7) })
			return ext.RunWithConn(conn)
		}
	}
	subprocess.SetFusedResolver(resolver)
	defer subprocess.SetFusedResolver(nil)

	host := subprocess.NewHost(t.TempDir())
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
	defer host.Shutdown("test")
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != members {
		t.Fatalf("startup loaded %d of %d, errors=%v", len(loaded), members, errs)
	}
	var want []string
	for _, config := range configs {
		want = append(want, config.Name)
	}
	for pass := range 3 {
		loaded, err := host.Reload(t.Context())
		if err != nil || len(loaded) != members {
			t.Fatalf("reload %d loaded %d of %d: %v", pass, len(loaded), members, err)
		}
		mu.Lock()
		heard = nil
		mu.Unlock()
		if err := loaded[0].Commands["emit"].Handler(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got := slices.Clone(heard)
		mu.Unlock()
		if !slices.Equal(got, want) {
			t.Fatalf("reload %d: listeners heard %v, want each live listener once in extension order %v", pass, got, want)
		}
	}
}
