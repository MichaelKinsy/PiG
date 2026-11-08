package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The Piglet was named pig-aot in 0.5.0 and 0.7.0 and is pig-bolt from 0.7.1. The owner approved one compatibility alias for the old name
// (piglets/pig-bolt/pig-aot.yaml, a Piglet that extends pig-bolt), so running or building that source path by the old name works. Like pig-bolt.yaml
// itself (relative local origins), it runs from its source path; `pig piglet add` refuses both, and the bare name does not resolve. The alias must select
// exactly what pig-bolt selects and say, in its description, that the name moved.
func TestPigAotAliasResolvesToPigBolt(t *testing.T) {
	root := testenv.ModuleRoot(t)
	boltPath := filepath.Join(root, "piglets", "pig-bolt", "pig-bolt.yaml")
	aliasPath := filepath.Join(root, "piglets", "pig-bolt", "pig-aot.yaml")
	bolt, err := piglet.ResolveEffective(boltPath)
	if err != nil {
		t.Fatal(err)
	}
	alias, err := piglet.ResolveEffective(aliasPath)
	if err != nil {
		t.Fatalf("the pig-aot alias does not resolve: %v", err)
	}
	if alias.Piglet.Name != "pig-aot" || bolt.Piglet.Name != "pig-bolt" {
		t.Fatalf("names = %q, %q", alias.Piglet.Name, bolt.Piglet.Name)
	}
	names := func(p *piglet.Piglet) []string {
		var out []string
		for _, e := range p.Extensions {
			out = append(out, e.Name)
		}
		return out
	}
	if got, want := names(alias.Piglet), names(bolt.Piglet); !slices.Equal(got, want) || len(want) != 1 || want[0] != "boltruntime" {
		t.Errorf("alias extensions = %v, pig-bolt extensions = %v, want both [boltruntime]", got, want)
	}
	if !strings.Contains(alias.Piglet.Description, "pig-bolt") {
		t.Errorf("the alias description does not point to pig-bolt: %q", alias.Piglet.Description)
	}
	parsed, err := piglet.Parse(aliasPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Release != nil {
		t.Error("the alias has a release block: it would be a shipped Piglet of its own (TestShippedPigletInventory)")
	}

	// The command from the source tree root: the relative path resolves and the Piglet it names resolves with its extends.
	t.Chdir(root)
	literal, err := piglet.Resolve(filepath.Join("piglets", "pig-bolt", "pig-aot.yaml"))
	if err != nil {
		t.Fatalf("pig --piglet piglets/pig-bolt/pig-aot.yaml: %v", err)
	}
	viaPath, err := piglet.ResolveEffective(literal)
	if err != nil || viaPath.Piglet.Name != "pig-aot" || !slices.Equal(names(viaPath.Piglet), []string{"boltruntime"}) {
		t.Fatalf("the source path resolves to %+v, %v", viaPath, err)
	}
	// The bare name does not resolve: nothing installs the alias into a Piglet directory, and the text of the alias says so.
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PATH", "")
	if path, err := piglet.Resolve("pig-aot"); err == nil {
		t.Errorf("the bare name pig-aot resolved to %s; the alias text says it does not", path)
	}
}
