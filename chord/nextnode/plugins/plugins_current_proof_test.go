package plugins

import (
	"path/filepath"
	"testing"
)

// Proof of Plugins.current(kind) and Plugins.watchBase(inputs) (plugins.ts) as the component hands them out.

func TestPluginsCurrentIsEmptyBeforeAKindsFirstUseThenItsBuiltSet(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	c := pcNew(t, root)
	if scripts, ok := c.Current("worker"); ok || scripts != nil {
		t.Fatalf("Current before the first use = %v, %v; want no set", scripts, ok)
	}
	if _, err := c.Scripts("worker", background); err != nil {
		t.Fatal(err)
	}
	scripts, ok := c.Current("worker")
	if !ok || len(scripts) != 1 || scripts[0].Plugin.Name != "@test/a" {
		t.Fatalf("Current after the first use = %v, %v; want the set of @test/a", scripts, ok)
	}
	if _, ok := c.Current("web"); ok {
		t.Fatal("a kind nobody used has a current set")
	}
}

func TestPluginsWatchBaseSetsBaseWhileARecordedFileDiffersFromItsBytes(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	c := pcNew(t, root)
	if _, err := c.Scripts("worker", background); err != nil {
		t.Fatal(err)
	}
	elsewhere := realOf(t, tempRoot(t))
	file := filepath.Join(elsewhere, "entry.ts")
	write(t, file, "export const a = 1;\n")
	<-c.WatchBaseCompared(BaseInputs{file: hashOf("export const a = 1;\n")})
	if c.Marks().Base {
		t.Fatal("base set by a record whose file has the recorded bytes")
	}
	c.WatchBase(BaseInputs{file: hashOf("export const a = 2;\n")})
	// WatchBase compares once now: its comparison of the other record settles before this one is read.
	<-c.WatchBaseCompared(BaseInputs{filepath.Join(elsewhere, "gone.ts"): hashOf("")})
	if !c.Marks().Base {
		t.Fatal("base not set while a recorded file differs from its bytes or is gone")
	}
}
