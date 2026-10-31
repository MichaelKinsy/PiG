package plugins

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Ports packages/chord/test/next-node/plugins/discover.test.ts. Tests of discovery (slice 7c §1): names from package.json,
// the first root winning a duplicate, directories reported (no or an invalid package.json, an invalid name, two files of
// one kind), contracts-only plugins, symlinks.

var background = chordctx.Background()

func TestDiscoveryNamesAPluginByItsPackageJSONListsItsFacetFilesByKindItsDependenciesAndExternals(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	dir := writePlugin(t, root, "deps-dir", "@test/deps", map[string]string{"worker.ts": "", "store.ts": ""}, map[string]any{
		"dependencies": map[string]any{"marked": "1.0.0"},
		"chord":        map[string]any{"external": []any{"better-sqlite3"}},
	})
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Problems) != 0 {
		t.Fatalf("problems = %v", discovery.Problems)
	}
	canon := realOf(t, root)
	want := []Plugin{
		{Name: "@test/contracts", Dir: filepath.Join(canon, "contracts"), Facets: map[string]string{}, Dependencies: []string{}, External: []string{}},
		{
			Name:         "@test/deps",
			Dir:          realOf(t, dir),
			Facets:       map[string]string{"worker": filepath.Join(canon, "deps-dir", "worker.ts")},
			Dependencies: []string{"marked"},
			External:     []string{"better-sqlite3"},
		},
		{
			Name:         "@test/notes",
			Dir:          filepath.Join(canon, "notes"),
			Facets:       map[string]string{"server": filepath.Join(canon, "notes", "server.ts"), "worker": filepath.Join(canon, "notes", "worker.ts")},
			Dependencies: []string{},
			External:     []string{},
		},
	}
	if !reflect.DeepEqual(discovery.Plugins, want) {
		t.Fatalf("plugins = %#v\nwant %#v", discovery.Plugins, want)
	}
}

func TestDiscoveryReportsADirectoryWithoutPackageJSONWithAnInvalidOneAnInvalidNameOrTwoFilesOfAKind(t *testing.T) {
	root := tempRoot(t)
	if err := os.Mkdir(filepath.Join(root, "bare"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(root, "broken"), map[string]string{"package.json": "{ not json"})
	writePlugin(t, root, "invalid", "Not A Name", map[string]string{}, nil)
	writePlugin(t, root, "two", "@test/two", map[string]string{"worker.ts": "", "worker.js": ""}, nil)
	writePlugin(t, root, "good", "@test/good", map[string]string{"web.tsx": ""}, nil)
	// Not plugins and not reported: hidden directories and a root's own packages.
	for _, name := range []string{".git", "node_modules"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, plugin := range discovery.Plugins {
		names = append(names, plugin.Name)
	}
	if !reflect.DeepEqual(names, []string{"@test/good"}) {
		t.Fatalf("plugins = %v", names)
	}
	canon := realOf(t, root)
	want := []struct {
		path  string
		error string
	}{
		{filepath.Join(canon, "bare"), "no package.json"},
		{filepath.Join(canon, "broken"), `^package\.json: `},
		{filepath.Join(canon, "invalid"), `package.json: "Not A Name" is not a valid plugin name`},
		{filepath.Join(canon, "two"), "two worker facets: worker.ts, worker.js"},
	}
	if len(discovery.Problems) != len(want) {
		t.Fatalf("problems = %v", discovery.Problems)
	}
	for i, problem := range discovery.Problems {
		if problem.Path != want[i].path {
			t.Errorf("problem %d path = %q, want %q", i, problem.Path, want[i].path)
		}
		if want[i].path == filepath.Join(canon, "broken") {
			if !regexp.MustCompile(want[i].error).MatchString(problem.Error) {
				t.Errorf("problem %d error = %q, want it to match %s", i, problem.Error, want[i].error)
			}
		} else if problem.Error != want[i].error {
			t.Errorf("problem %d error = %q, want %q", i, problem.Error, want[i].error)
		}
	}
}

func TestDiscoveryKeepsTheFirstRootsPluginOfADuplicateNameAndReportsTheOtherAMissingRootIsEmpty(t *testing.T) {
	first := tempRoot(t)
	second := tempRoot(t)
	writePlugin(t, first, "a", "@test/same", map[string]string{"worker.ts": ""}, nil)
	writePlugin(t, second, "b", "@test/same", map[string]string{"server.ts": ""}, nil)
	discovery, err := DiscoverPlugins([]string{first, filepath.Join(first, "missing"), second}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, plugin := range discovery.Plugins {
		dirs = append(dirs, plugin.Dir)
	}
	if want := []string{filepath.Join(realOf(t, first), "a")}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("plugin dirs = %v, want %v", dirs, want)
	}
	want := []DiscoveryProblem{{
		Path:  filepath.Join(realOf(t, second), "b"),
		Error: `plugin "@test/same" is in ` + filepath.Join(realOf(t, first), "a") + " already",
	}}
	if !reflect.DeepEqual(discovery.Problems, want) {
		t.Fatalf("problems = %#v, want %#v", discovery.Problems, want)
	}
}

func TestDiscoveryFollowsASymlinkedPluginDirectoryToItsRealPath(t *testing.T) {
	elsewhere := tempRoot(t)
	root := tempRoot(t)
	dir := writePlugin(t, elsewhere, "linked", "@test/linked", map[string]string{"worker.ts": ""}, nil)
	if err := os.Symlink(dir, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Plugins) != 1 || discovery.Plugins[0].Name != "@test/linked" || discovery.Plugins[0].Dir != realOf(t, dir) {
		t.Fatalf("plugins = %#v", discovery.Plugins)
	}
	if want := map[string]string{"worker": filepath.Join(realOf(t, dir), "worker.ts")}; !reflect.DeepEqual(discovery.Plugins[0].Facets, want) {
		t.Fatalf("facets = %v, want %v", discovery.Plugins[0].Facets, want)
	}
}

// Not upstream's: the abort `context.abortSignal?.throwIfAborted()` throws before each directory (never at entry).
func TestDiscoveryReturnsTheAbortsReasonBeforeEachDirectoryAndNothingForNoDirectory(t *testing.T) {
	root := tempRoot(t, "contracts")
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	cancel(stop)
	if _, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, aborted); !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason", err)
	}
	discovery, err := DiscoverPlugins([]string{filepath.Join(root, "missing")}, DiscoveryOptions{Kinds: kinds}, aborted)
	if err != nil || len(discovery.Plugins) != 0 || len(discovery.Problems) != 0 {
		t.Fatalf("discovery = %+v, error = %v", discovery, err)
	}
}
