package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var updatePigletInventory = flag.Bool("update-piglet-inventory", false, "rewrite testdata/piglet-inventory from the shipped Piglets")

const pigletInventoryDir = "testdata/piglet-inventory"

// TestShippedPigletInventory pins, for every shipped Piglet (a manifest under piglets/ with a release block), exactly
// which built-in tools, slash commands, built-in extensions, model APIs and features it exposes, and the extensions,
// tools allow-list and discovery it selects. A strip is opt-in (D92): an ID is stripped only when the Piglet's strip
// list names it, never because discovery is empty. The built-in states come from the production startup build of the
// resolved Piglet (applyPigletStrip, then buildResources and buildSession), so a strip list, a resolver or a startup
// change that removes or adds anything shows as a diff against the reviewed inventory. TestStripLeakGate proves that a
// stripped ID leaves every surface; this test pins which IDs each shipped Piglet strips.
//
// Regenerate after a reviewed change: go test ./coding/cli -run '^TestShippedPigletInventory$' -update-piglet-inventory
func TestShippedPigletInventory(t *testing.T) {
	if pigstrip.Active() {
		t.Skip("this build compiles built-ins out; the inventory is of Stock PiG running each Piglet")
	}
	root := testenv.ModuleRoot(t)
	manifests, err := filepath.Glob(filepath.Join(root, "piglets", "*", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	shipped := map[string]bool{}
	for _, path := range manifests {
		parsed, err := piglet.Parse(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if parsed.Release == nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		shipped[parsed.Name] = true
		t.Run(parsed.Name, func(t *testing.T) {
			got := shippedPigletInventory(t, path, filepath.ToSlash(rel))
			golden := filepath.Join(pigletInventoryDir, parsed.Name+".txt")
			if *updatePigletInventory {
				if err := os.MkdirAll(pigletInventoryDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("shipped Piglet %s has no reviewed inventory (%v); review this one and regenerate with -update-piglet-inventory:\n%s", parsed.Name, err, got)
			}
			if string(want) != got {
				t.Fatalf("shipped Piglet %s exposes a different inventory than %s; review the change and regenerate with -update-piglet-inventory\n%s", parsed.Name, golden, lineDiff(string(want), got))
			}
		})
	}
	if len(shipped) == 0 {
		t.Fatal("no shipped Piglet under piglets/; the inventory would pass vacuously")
	}
	if *updatePigletInventory {
		return
	}
	entries, err := os.ReadDir(pigletInventoryDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".txt"); !ok || !shipped[name] {
			t.Errorf("%s/%s pins no shipped Piglet; remove it", pigletInventoryDir, entry.Name())
		}
	}
}

// shippedPigletInventory renders the inventory of the Piglet at path as the reviewed text form.
func shippedPigletInventory(t *testing.T, path, rel string) string {
	t.Helper()
	resolved, err := piglet.ResolveEffective(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", rel, err)
	}
	p := resolved.Piglet
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s)\n", p.Name, rel)
	b.WriteString("# regenerate: go test ./coding/cli -run '^TestShippedPigletInventory$' -update-piglet-inventory\n")
	strip := "none"
	if !p.Strip.IsEmpty() {
		var ids []string
		for _, id := range p.Strip.StrippedIDs() {
			ids = append(ids, id.SlotID()+"("+id.Disposition+")")
		}
		strip = strings.Join(ids, " ")
	}
	fmt.Fprintf(&b, "strip: %s\n", strip)
	toolsAllow := "all"
	if p.BuiltinTools != nil {
		toolsAllow = "[" + strings.Join(*p.BuiltinTools, ", ") + "]"
	}
	fmt.Fprintf(&b, "tools allow-list: %s\n", toolsAllow)
	discovery := piglet.Discovery{}
	if p.Discovery != nil {
		discovery = *p.Discovery
	}
	fmt.Fprintf(&b, "discovery.extensions: [%s]\n", strings.Join(discovery.Extensions, ", "))
	fmt.Fprintf(&b, "discovery.skills: [%s]\n", strings.Join(discovery.Skills, ", "))
	frontend := "pig"
	if p.HasFrontend() {
		frontend = "replaced"
	}
	fmt.Fprintf(&b, "slot frontend: %s\n", frontend)
	for _, entry := range p.Extensions {
		tools := "all"
		if entry.Tools != nil {
			tools = "[" + strings.Join(*entry.Tools, ", ") + "]"
		}
		// Resolution makes local origins absolute, in the OS's separators; the inventory names them relative to the module
		// root with slashes, so it reads the same on every OS.
		origins := make([]string, len(entry.Origins))
		for i, origin := range entry.Origins {
			origins[i] = origin
			if local, ok := strings.CutPrefix(origin, "local:"); ok && filepath.IsAbs(local) {
				if rel, err := filepath.Rel(testenv.ModuleRoot(t), local); err == nil {
					origins[i] = "local:" + filepath.ToSlash(rel)
				}
			}
		}
		fmt.Fprintf(&b, "extension %s origins=[%s] tools=%s\n", entry.Name, strings.Join(origins, ", "), tools)
	}
	for _, skill := range p.Skills {
		fmt.Fprintf(&b, "skill %s\n", skill.Name)
	}

	// The built-in surface: the same resolved Piglet without its own extensions, which the build would compile and
	// start, through the production strip and startup build.
	startup, err := piglet.ResolveEffective(path)
	if err != nil {
		t.Fatal(err)
	}
	startup.Piglet.Extensions = nil
	build := buildStripRuntime(t, startup.Piglet)
	for _, id := range pigstrip.Known(pigstrip.ListTools) {
		state := "inactive"
		switch {
		case pigstrip.Has(pigstrip.ListTools, id):
			state = "stripped"
		case slices.Contains(build.AgentToolNames, id):
			state = "active"
		}
		fmt.Fprintf(&b, "tools.%s %s\n", id, state)
	}
	loaded := builtinExtensionPaths(build)
	for _, id := range pigstrip.Known(pigstrip.ListExtensions) {
		state := "available"
		switch {
		case pigstrip.Has(pigstrip.ListExtensions, id):
			state = "stripped"
		case slices.Contains(loaded, "builtin:"+id):
			state = "loaded"
		}
		fmt.Fprintf(&b, "extensions.%s %s\n", id, state)
	}
	for _, list := range []string{pigstrip.ListCommands, pigstrip.ListAPIs, pigstrip.ListFeatures} {
		for _, id := range pigstrip.Known(list) {
			state := "kept"
			if pigstrip.Has(list, id) {
				state = "stripped"
			}
			fmt.Fprintf(&b, "%s.%s %s\n", list, id, state)
		}
	}
	return b.String()
}

// lineDiff lists the lines only want has (-) and only got has (+), in order.
func lineDiff(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for _, line := range wantLines {
		if !slices.Contains(gotLines, line) {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	for _, line := range gotLines {
		if !slices.Contains(wantLines, line) {
			fmt.Fprintf(&b, "+ %s\n", line)
		}
	}
	return b.String()
}
