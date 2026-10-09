//go:build !pig_strip_pig_login

package builtin_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
)

// pig-login is PiG's own built-in extension (D2, owner decision 2026-10-01): the `/sprite` command of the sprite login.
// Upstream's index.ts lists no such entry, so it comes after upstream's and another extension's `/sprite` replaces it.
func entryNames(entries []builtin.Extension) []string {
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

func TestAllListsPigLoginLastAsReplaceable(t *testing.T) {
	all := builtin.All(builtin.Options{})
	if len(all) == 0 || all[len(all)-1].Name != "pig-login" {
		t.Fatalf("registry %v, want pig-login last", entryNames(all))
	}
	if !all[len(all)-1].Replaceable {
		t.Error("pig-login is not replaceable: an extension that registers /sprite could not take the command")
	}
	if names := entryNames(all); slices.Contains(names[:len(names)-1], "pig-login") {
		t.Errorf("pig-login listed twice in %v", names)
	}
}

func TestPigLoginResolvesAndRegistersTheSpriteCommand(t *testing.T) {
	entry, err := builtin.Resolve("builtin:pig-login", builtin.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path() != "builtin:pig-login" || entry.Factory == nil {
		t.Fatalf("entry = %+v", entry)
	}
	ext, err := factoryload.LoadExtensionFromFactory(entry.Factory, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "builtin:pig-login")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ext.Commands["sprite"]; !ok || !slices.Equal(ext.CommandOrder, []string{"sprite"}) {
		t.Fatalf("commands = %v (order %v), want /sprite", ext.Commands, ext.CommandOrder)
	}
}
