package subprocess_test

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

type startupFusedResolver map[string]func(net.Conn) error

func (r startupFusedResolver) FusedServe(config subprocess.ExtConfig) (func(net.Conn) error, bool) {
	serve, found := r[config.Name]
	return serve, found
}

func TestHostFinalExtensionSetKeepsFusedIdentitiesDistinct(t *testing.T) {
	subprocess.SetFusedResolver(startupFusedResolver{
		"first":  greetExt("first").RunWithConn,
		"second": greetExt("second").RunWithConn,
	})
	defer subprocess.SetFusedResolver(nil)
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test")
	first := subprocess.ExtConfig{Name: "first", Enabled: true}
	second := subprocess.ExtConfig{Name: "second", Enabled: true}
	if _, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{first}); len(errs) != 0 {
		t.Fatal(errs)
	}
	loaded, errs := host.LoadFinalExtensionSet(t.Context(), []subprocess.ExtConfig{second, first}, []subprocess.ExtConfig{first})
	if len(errs) != 0 || len(loaded) != 2 || loaded[0].Name != "second" || loaded[1].Name != "first" {
		t.Fatalf("final=%v errors=%v", loaded, errs)
	}
	for _, ext := range loaded {
		assertGreet(t, ext)
	}
}

func TestHostReloadReconstructsFusedFactory(t *testing.T) {
	var starts atomic.Int32
	serve := func(conn net.Conn) error {
		starts.Add(1)
		return greetExt("fused").RunWithConn(conn)
	}
	subprocess.SetFusedResolver(startupFusedResolver{"fused": serve})
	defer subprocess.SetFusedResolver(nil)

	config := subprocess.ExtConfig{Name: "fused", Enabled: true}
	host := subprocess.NewHost(t.TempDir())
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) {
		return []subprocess.ExtConfig{config}, nil
	})
	defer host.Shutdown("test")
	loaded, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{config})
	if len(errs) != 0 || len(loaded) != 1 || starts.Load() != 1 {
		t.Fatalf("startup loaded=%v errors=%v starts=%d", loaded, errs, starts.Load())
	}

	reloaded, err := host.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 1 || starts.Load() != 2 {
		t.Fatalf("reload loaded=%v starts=%d, want fresh fused factory", reloaded, starts.Load())
	}
	assertGreet(t, reloaded[0])
}
