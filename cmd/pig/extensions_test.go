// pig-specific: no upstream equivalent.

package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestLoadSubprocessExtensions_NoConfigsReturnsNil(t *testing.T) {
	t.Parallel()

	exts, host, bridge, errs := loadSubprocessExtensions(context.Background(), t.TempDir(), extension.ModePrint, nil, nil, nil, nil)
	if exts != nil || host != nil || bridge != nil || errs != nil {
		t.Fatalf("loadSubprocessExtensions() = (%v, %v, %v, %v), want all nil", exts, host, bridge, errs)
	}
}

func TestLoadSubprocessExtensionsReloadUsesStartupResolver(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	configs := []subprocess.ExtConfig{{Name: "ctx-mode", Source: fixture, Enabled: true}}
	reloadInputs := func() []subprocess.ExtConfig { return append([]subprocess.ExtConfig(nil), configs...) }
	extensions, host, _, errs := loadSubprocessExtensions(t.Context(), t.TempDir(), extension.ModePrint, nil, configs, nil, reloadInputs)
	if len(errs) != 0 {
		t.Fatalf("startup load errors = %v", errs)
	}
	if host == nil {
		t.Fatal("startup resolver did not create an extension host")
	}
	defer host.Shutdown("test done")
	if len(extensions) != 1 || extensions[0].Name != "ctx-mode" {
		t.Fatalf("startup extensions = %#v", extensions)
	}
	reloaded, err := host.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 1 || reloaded[0].Name != "ctx-mode" {
		t.Fatalf("reload extensions = %#v, want startup identity ctx-mode", reloaded)
	}
}

// Upstream /reload rediscovers extensions even when none loaded at startup.
// Interactive startup with no extensions keeps an empty host whose reload
// loads an extension added afterwards; -ne keeps no host.
func TestReloadableExtensionHostLoadsExtensionAddedAfterStartup(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	var configs []subprocess.ExtConfig
	reloadInputs := func() []subprocess.ExtConfig { return append([]subprocess.ExtConfig(nil), configs...) }
	if host, bridge := ensureReloadableExtensionHost(nil, nil, true, t.TempDir(), nil, reloadInputs); host != nil || bridge != nil {
		t.Fatal("--no-extensions created an extension host")
	}
	exts, host, bridge, errs := loadSubprocessExtensions(t.Context(), t.TempDir(), extension.ModeTUI, nil, nil, nil, reloadInputs)
	if exts != nil || host != nil || errs != nil {
		t.Fatalf("startup without extensions = %v, %v, %v", exts, host, errs)
	}
	host, bridge = ensureReloadableExtensionHost(host, bridge, false, t.TempDir(), nil, reloadInputs)
	if host == nil || bridge == nil {
		t.Fatal("interactive startup without extensions has no reload-capable host")
	}
	defer host.Shutdown("test done")

	configs = []subprocess.ExtConfig{{Name: "ctx-mode", Source: fixture, Enabled: true}}
	reloaded, err := host.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 1 || reloaded[0].Name != "ctx-mode" {
		t.Fatalf("reloaded = %#v, want the extension added after startup", reloaded)
	}
}
