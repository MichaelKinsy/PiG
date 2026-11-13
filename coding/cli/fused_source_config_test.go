package cli

import (
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func configNames(configs []subprocess.ExtConfig) []string {
	names := make([]string, len(configs))
	for i, cfg := range configs {
		names[i] = cfg.Name + "=" + cfg.Source
	}
	return names
}

// A Binary built with `build.fusedReload: source` (D31) gives the host one configuration for a fused member whose source the run
// names (`-e ./core`): the source configuration, in the member's place. A Binary built without the key, and stock pig, merge as
// before: the source configuration loads beside the fused member as a second copy.
func TestSourceConfigTakesTheFusedMembersPlaceOnlyWithABakedDigest(t *testing.T) {
	core, other := t.TempDir(), t.TempDir()
	fused := []subprocess.ExtConfig{{Name: "core", Enabled: true}, {Name: "plain", Enabled: true}}
	configs := []subprocess.ExtConfig{{Name: "other", Source: other, Enabled: true}, {Name: "core", Source: core, Enabled: true}}
	baked := func(name string) bool { return name == "core" }

	withKey := subprocess.DisambiguateIdentities(withFusedExtensionConfigs(fused, slices.Clone(configs), baked))
	if got, want := configNames(withKey), []string{"core=" + core, "plain=", "other=" + other}; !slices.Equal(got, want) {
		t.Fatalf("with a baked digest: %q, want %q", got, want)
	}
	if !slices.Equal(configNames(configs), []string{"other=" + other, "core=" + core}) {
		t.Fatalf("the caller's configs were changed: %q", configNames(configs))
	}
	want := []string{"core=", "plain=", "other=" + other, "core:2=" + core}
	for name, predicate := range map[string]func(string) bool{"no predicate": nil, "no digest baked": func(string) bool { return false }} {
		if got := configNames(subprocess.DisambiguateIdentities(withFusedExtensionConfigs(fused, slices.Clone(configs), predicate))); !slices.Equal(got, want) {
			t.Fatalf("%s: %q, want today's %q", name, got, want)
		}
	}
	// The active Piglet's own entry for the fused member (name only) is the same extension, not a second copy.
	listed := append([]subprocess.ExtConfig{{Name: "core", Enabled: true}}, configs...)
	if got, want := configNames(subprocess.DisambiguateIdentities(withFusedExtensionConfigs(fused, listed, baked))), []string{"core=" + core, "plain=", "other=" + other}; !slices.Equal(got, want) {
		t.Fatalf("with the member listed by name too: %q, want %q", got, want)
	}
	// A configuration of the name without a source does not take the place.
	nameOnly := []subprocess.ExtConfig{{Name: "core", Path: "/bin/core", Enabled: true}}
	if got := configNames(withFusedExtensionConfigs(fused, nameOnly, baked)); !slices.Equal(got, []string{"core=", "plain=", "core="}) {
		t.Fatalf("a configuration without a source: %q", got)
	}
}

// A start reports the fused-source warning as a warning that names the member, not as a failed load: the member is loaded.
func TestFusedSourceWarningIsAStartWarning(t *testing.T) {
	diagnostics := extensionLoadDiagnostics([]error{
		&subprocess.ExtensionLoadError{Name: "core", Path: "./core", Err: &subprocess.FusedSourceWarning{Message: "source at ./core differs from the fused copy; no Go toolchain: running the fused copy: remedy"}},
		&subprocess.ExtensionLoadError{Name: "bad", Path: "./bad", Err: errors.New("boom")},
	})
	if len(diagnostics) != 2 || diagnostics[0].Type != "warning" || diagnostics[0].Message != `Extension "core": source at ./core differs from the fused copy; no Go toolchain: running the fused copy: remedy` {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	if diagnostics[1].Type != "error" || diagnostics[1].Message != `Failed to load extension "./bad": Failed to load extension: boom` {
		t.Fatalf("an ordinary failure = %+v", diagnostics[1])
	}
}
