package subprocess

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi loads each extension on its own: an extension whose entry point does not resolve is that extension's load error (loader.ts loadExtensions collects one error
// per path), and the others load. Packed mode is observationally identical to isolated mode, so a Node cell that holds three extensions, one of whose entry points
// is gone, loads the other two and reports the missing one alone, at startup and at a reload.
func TestNodeCellLoadsItsOtherMembersWhenOneEntryPointIsGone(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	for _, entry := range []string{"file", "package"} {
		for _, phase := range []string{"startup", "reload"} {
			t.Run(entry+"/"+phase, func(t *testing.T) {
				var texts []string
				for _, isolation := range []string{"shared-ok", "isolated"} {
					loaded, failures, root := loadWithMissingMiddleEntry(t, entry, phase, isolation)
					if !slices.Equal(loaded, []string{"first", "last"}) {
						t.Errorf("%s: loaded %v, want first and last", isolation, loaded)
					}
					if len(failures) != 1 || !strings.Contains(failures[0], `"middle"`) || strings.Contains(failures[0], `"first"`) || strings.Contains(failures[0], `"last"`) {
						t.Errorf("%s: failures %q, want the middle extension's alone", isolation, failures)
					}
					texts = append(texts, strings.ReplaceAll(strings.Join(failures, "\n"), root, "<root>"))
				}
				// A member of a shared cell reports its missing entry point as the same extension loaded isolated does.
				if texts[0] != texts[1] {
					t.Errorf("shared-ok reports %q, isolated reports %q: want the same text", texts[0], texts[1])
				}
			})
		}
	}
}

// loadWithMissingMiddleEntry loads three Node extensions whose configs were resolved while their files existed, with the middle one's entry point removed before the
// load (startup) or between a load and a reload (reload). It returns the loaded extensions' names in order, the failure messages the load reported and the directory the extensions were written to.
func loadWithMissingMiddleEntry(t *testing.T, entry, phase, isolation string) (loaded, failures []string, root string) {
	t.Helper()
	root = t.TempDir()
	source := func(command string) string {
		return `export default function (pi) {
  pi.registerCommand("` + command + `", {description: "probe", handler: async () => {}});
}
`
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths := map[string]string{"first": filepath.Join(root, "first.mjs"), "last": filepath.Join(root, "last.mjs")}
	var remove string
	switch entry {
	case "file":
		paths["middle"] = filepath.Join(root, "middle.mjs")
		remove = paths["middle"]
	case "package":
		paths["middle"] = filepath.Join(root, "middle")
		write(filepath.Join(paths["middle"], "package.json"), `{"name":"middle","pi":{"extensions":["./entry.mjs"]}}`)
		remove = filepath.Join(paths["middle"], "entry.mjs")
		write(remove, source("middle-probe"))
	}
	if entry == "file" {
		write(remove, source("middle-probe"))
	}
	write(paths["first"], source("first-probe"))
	write(paths["last"], source("last-probe"))
	var configs []ExtConfig
	for _, name := range []string{"first", "middle", "last"} {
		config, _, err := ResolveExtConfig(paths[name])
		if err != nil {
			t.Fatal(err)
		}
		config.Isolation = isolation
		configs = append(configs, config)
	}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return slices.Clone(configs), nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := buildContext(t)
	defer cancel()
	if phase == "reload" {
		if _, errs := h.LoadAll(ctx, slices.Clone(configs)); len(errs) != 0 {
			t.Fatalf("the load before the removal failed: %v", errs)
		}
		if names := extensionNames(h); len(names) != 3 {
			t.Fatalf("loaded %v before the removal, want three extensions", names)
		}
	}
	if err := os.Remove(remove); err != nil {
		t.Fatal(err)
	}
	if phase == "startup" {
		_, errs := h.LoadAll(ctx, slices.Clone(configs))
		for _, err := range errs {
			failures = append(failures, err.Error())
		}
	} else {
		if _, err := h.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		failures = h.LastReloadReport().Issues
	}
	return extensionNames(h), failures, root
}

func extensionNames(h *Host) []string {
	var names []string
	for _, ext := range h.Extensions() {
		names = append(names, ext.Name)
	}
	return names
}

// A shared Go cell does the same at startup: the member whose source is gone fails alone, with its own error, and the other two members load. (A reload of a
// Go member whose source went reuses the cached build of the unchanged source, as a reload of any unchanged extension does.)
func TestPackedGoCellLoadsItsOtherMembersWhenOneSourceIsGone(t *testing.T) {
	var configs []ExtConfig
	var middle string
	for _, name := range []string{"first", "middle", "last"} {
		module := "example.com/gone/" + name
		root := writePackedFactoryModule(t, module, "gone-"+name, "tool_"+name)
		if name == "middle" {
			middle = root
		}
		configs = append(configs, packedFactoryConfig("gone-"+name, root, module, "h"+name))
	}
	if err := os.RemoveAll(middle); err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := buildContext(t)
	defer cancel()
	_, errs := h.LoadAll(ctx, configs)
	if names := extensionNames(h); !slices.Equal(names, []string{"gone-first", "gone-last"}) {
		t.Errorf("loaded %v, want gone-first and gone-last", names)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `"gone-middle"`) || strings.Contains(errs[0].Error(), `"gone-first"`) || strings.Contains(errs[0].Error(), `"gone-last"`) {
		t.Errorf("errors %q, want the middle extension's alone", errs)
	}
}
