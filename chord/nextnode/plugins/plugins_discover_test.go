package plugins

// pi: packages/chord/src/next-node/plugins/discover.ts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/chord/test/node/plugins/discover.test.ts. Tests of discovery (slice 7c §1, slice 12 §1, §2, §4): names
// from package.json, the first root winning a duplicate, directories reported (no or an invalid package.json, an invalid
// name, two files of one kind), contracts-only plugins, symlinks, required roots, checkouts of several plugins, packages'
// chord.plugins manifests, single-file plugins.

var background = chordctx.Background()

// plainRoots are paths as roots that are not required (upstream's string form of a root).
func plainRoots(paths ...string) []PluginRoot {
	roots := make([]PluginRoot, len(paths))
	for i, path := range paths {
		roots[i] = PluginRoot{Path: path}
	}
	return roots
}

// pathsAndErrors is problems as the upstream tests compare them: `{ path, error }` of each.
func pathsAndErrors(problems []DiscoveryProblem) [][2]string {
	pairs := [][2]string{}
	for _, problem := range problems {
		pairs = append(pairs, [2]string{problem.Path, problem.Error})
	}
	return pairs
}

// pluginNames is the names of plugins.
func pluginNames(plugins []Plugin) []string {
	names := []string{}
	for _, plugin := range plugins {
		names = append(names, plugin.Name)
	}
	return names
}

// discovered is DiscoverPlugins's result, which must not be an error.
func discovered(t *testing.T, roots []PluginRoot) Discovery {
	t.Helper()
	discovery, err := DiscoverPlugins(roots, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	return discovery
}

// manifestText is a package.json's text.
func manifestText(t *testing.T, fields map[string]any) string {
	t.Helper()
	text, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestDiscoveryNamesAPluginByItsPackageJSONListsItsFacetFilesByKindItsDependenciesAndExternals(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	dir := writePlugin(t, root, "deps-dir", "@test/deps", map[string]string{"worker.ts": "", "store.ts": ""}, map[string]any{
		"dependencies": map[string]any{"marked": "1.0.0"},
		"chord":        map[string]any{"external": []any{"better-sqlite3"}},
	})
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Problems) != 0 {
		t.Fatalf("problems = %v", discovery.Problems)
	}
	canon := realOf(t, root)
	want := []Plugin{
		{Name: "@test/contracts", Dir: filepath.Join(canon, "contracts"), Root: canon, Layout: LayoutDirectory, Facets: map[string]string{}, Dependencies: []string{}, External: []string{}},
		{
			Name:         "@test/deps",
			Dir:          realOf(t, dir),
			Root:         canon,
			Layout:       LayoutDirectory,
			Facets:       map[string]string{"worker": filepath.Join(canon, "deps-dir", "worker.ts")},
			Dependencies: []string{"marked"},
			External:     []string{"better-sqlite3"},
		},
		{
			Name:         "@test/notes",
			Dir:          filepath.Join(canon, "notes"),
			Root:         canon,
			Layout:       LayoutDirectory,
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
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
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
	discovery, err := DiscoverPlugins(plainRoots(first, filepath.Join(first, "missing"), second), DiscoveryOptions{Kinds: kinds}, background)
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
	want := [][2]string{{
		filepath.Join(realOf(t, second), "b"),
		`plugin "@test/same" is in ` + filepath.Join(realOf(t, first), "a") + " already",
	}}
	if got := pathsAndErrors(discovery.Problems); !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %#v, want %#v", got, want)
	}
}

func TestDiscoveryFollowsASymlinkedPluginDirectoryToItsRealPath(t *testing.T) {
	elsewhere := tempRoot(t)
	root := tempRoot(t)
	dir := writePlugin(t, elsewhere, "linked", "@test/linked", map[string]string{"worker.ts": ""}, nil)
	testenv.RequireDirectoryLink(t, dir, filepath.Join(root, "link"))
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
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
	if _, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, aborted); !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason", err)
	}
	discovery, err := DiscoverPlugins(plainRoots(filepath.Join(root, "missing")), DiscoveryOptions{Kinds: kinds}, aborted)
	if err != nil || len(discovery.Plugins) != 0 || len(discovery.Problems) != 0 {
		t.Fatalf("discovery = %+v, error = %v", discovery, err)
	}
}

func TestDiscoveryMarksThePluginsOfARequiredRootAndRecordsEachPluginsRoot(t *testing.T) {
	app := tempRoot(t)
	user := tempRoot(t)
	writePlugin(t, app, "core", "@test/core", map[string]string{"server.ts": ""}, nil)
	writePlugin(t, user, "extra", "@test/extra", map[string]string{"server.ts": ""}, nil)
	discovery := discovered(t, []PluginRoot{{Path: app, Required: true}, {Path: user}})
	type row struct {
		name, root string
		required   bool
	}
	got := []row{}
	for _, plugin := range discovery.Plugins {
		got = append(got, row{plugin.Name, plugin.Root, plugin.Required})
	}
	want := []row{{"@test/core", realOf(t, app), true}, {"@test/extra", realOf(t, user), false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
}

func TestDiscoveryListsThePluginsOfACheckoutOfSeveralADirectoryWithoutPackageJSONWithinItsRoot(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, filepath.Join(root, "repo"), "notes", "@test/notes", map[string]string{"worker.ts": ""}, nil)
	writePlugin(t, filepath.Join(root, "repo"), "search", "@test/search", map[string]string{"worker.ts": ""}, nil)
	if err := os.MkdirAll(filepath.Join(root, "repo", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	discovery := discovered(t, plainRoots(root))
	canon := realOf(t, root)
	if len(discovery.Problems) != 0 {
		t.Fatalf("problems = %v", discovery.Problems)
	}
	type row struct{ name, dir, root string }
	got := []row{}
	for _, plugin := range discovery.Plugins {
		got = append(got, row{plugin.Name, plugin.Dir, plugin.Root})
	}
	want := []row{
		{"@test/notes", filepath.Join(canon, "repo", "notes"), canon},
		{"@test/search", filepath.Join(canon, "repo", "search"), canon},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
}

func TestDiscoveryListsTheFoldersChordPluginsNamesPathsGlobsAndExclusions(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, filepath.Join(root, "bob"), map[string]string{
		"package.json": manifestText(t, map[string]any{
			"name":  "@bob/plugins",
			"chord": map[string]any{"plugins": []any{"./notes/", "extra/*", "!extra/old", "deep/**/p?"}},
		}),
		"server.ts": "",
	})
	writePlugin(t, filepath.Join(root, "bob"), "notes", "@bob/notes", map[string]string{"worker.ts": ""}, nil)
	writePlugin(t, filepath.Join(root, "bob"), "unlisted", "@bob/unlisted", map[string]string{"worker.ts": ""}, nil)
	writePlugin(t, filepath.Join(root, "bob", "extra"), "search", "@bob/search", map[string]string{"worker.ts": ""}, nil)
	writePlugin(t, filepath.Join(root, "bob", "extra"), "old", "@bob/old", map[string]string{"worker.ts": ""}, nil)
	writePlugin(t, filepath.Join(root, "bob", "deep", "a", "b"), "p1", "@bob/p1", map[string]string{"worker.ts": ""}, nil)
	discovery := discovered(t, plainRoots(root))
	if len(discovery.Problems) != 0 {
		t.Fatalf("problems = %v", discovery.Problems)
	}
	canon := realOf(t, root)
	got := [][2]string{}
	for _, plugin := range discovery.Plugins {
		got = append(got, [2]string{plugin.Name, filepath.ToSlash(plugin.Dir[len(canon)+1:])})
	}
	want := [][2]string{{"@bob/notes", "bob/notes"}, {"@bob/p1", "bob/deep/a/b/p1"}, {"@bob/search", "bob/extra/search"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
}

func TestDiscoveryListsThePackageItselfAsDotAndReportsAListedFolderInsideAnotherListedOneOpusN5(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, filepath.Join(root, "one"), map[string]string{
		"package.json": manifestText(t, map[string]any{"name": "@bob/one", "chord": map[string]any{"plugins": []any{"."}}}),
		"worker.ts":    "",
	})
	writeFiles(t, filepath.Join(root, "two"), map[string]string{
		"package.json":     manifestText(t, map[string]any{"name": "@bob/two", "chord": map[string]any{"plugins": []any{".", "./sub"}}}),
		"sub/package.json": manifestText(t, map[string]any{"name": "@bob/sub"}),
	})
	discovery := discovered(t, plainRoots(root))
	if got := pluginNames(discovery.Plugins); !reflect.DeepEqual(got, []string{"@bob/one"}) {
		t.Fatalf("plugins = %v", got)
	}
	errs := []string{}
	for _, problem := range discovery.Problems {
		errs = append(errs, problem.Error)
	}
	if want := []string{`package.json: chord.plugins lists "sub" inside the listed "."`}; !reflect.DeepEqual(errs, want) {
		t.Fatalf("problem errors = %q, want %q", errs, want)
	}
}

func TestDiscoveryReportsAManifestThatIsNoListListsNothingLeavesThePackageOrNamesNoDirectory(t *testing.T) {
	root := tempRoot(t)
	manifest := func(plugins any) string {
		return manifestText(t, map[string]any{"name": "@bob/x", "chord": map[string]any{"plugins": plugins}})
	}
	writeFiles(t, root, map[string]string{
		"a/package.json": manifest(map[string]any{"@bob/notes": "github:bob/plugins#v1:notes"}),
		"b/package.json": manifest([]any{"nothing/*"}),
		"c/package.json": manifest([]any{"../a"}),
		"d/package.json": manifest([]any{"./file.txt"}),
		"d/file.txt":     "",
	})
	discovery := discovered(t, plainRoots(root))
	canon := realOf(t, root)
	if len(discovery.Plugins) != 0 {
		t.Fatalf("plugins = %#v", discovery.Plugins)
	}
	got := [][2]string{}
	for _, problem := range discovery.Problems {
		got = append(got, [2]string{filepath.ToSlash(problem.Path[len(canon)+1:]), problem.Error})
	}
	want := [][2]string{
		{"a", "package.json: chord.plugins is not a list of plugin folders"},
		{"b", "package.json: chord.plugins lists no plugin folder"},
		{"c", `package.json: chord.plugins "../a" leaves the package`},
		{"d/file.txt", "listed in chord.plugins, not a directory"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %q, want %q", got, want)
	}
}

func TestDiscoveryByConventionAPackageWithFacetFilesAtItsTopIsOnePluginElseItsSubfoldersWithAPackageJSON(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "single", "@test/single", map[string]string{"worker.ts": "", "fixtures/package.json": "{}"}, nil)
	writeFiles(t, filepath.Join(root, "repo"), map[string]string{"package.json": manifestText(t, map[string]any{"name": "@test/repo"})})
	writePlugin(t, filepath.Join(root, "repo"), "notes", "@test/notes", map[string]string{"worker.ts": ""}, nil)
	writePlugin(t, root, "contracts-only", "@test/contracts", map[string]string{"contract.ts": ""}, nil)
	discovery := discovered(t, plainRoots(root))
	if len(discovery.Problems) != 0 {
		t.Fatalf("problems = %v", discovery.Problems)
	}
	if got, want := pluginNames(discovery.Plugins), []string{"@test/contracts", "@test/notes", "@test/single"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
}

func TestDiscoveryReportsARequiredRootThatDoesNotExistAndRecordsEachProblemsRoot(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, filepath.Join(root, "broken"), map[string]string{"package.json": "{ not json"})
	missing := filepath.Join(root, "missing")
	discovery := discovered(t, []PluginRoot{{Path: root, Required: true}, {Path: missing, Required: true}})
	canon := realOf(t, root)
	if len(discovery.Problems) != 2 {
		t.Fatalf("problems = %#v", discovery.Problems)
	}
	first := discovery.Problems[0]
	if first.Path != filepath.Join(canon, "broken") || !regexp.MustCompile(`^package\.json: `).MatchString(first.Error) || first.Root != canon || !first.Required {
		t.Fatalf("problem 0 = %#v", first)
	}
	want := DiscoveryProblem{Path: missing, Error: "the required root does not exist", Root: missing, Required: true}
	if discovery.Problems[1] != want {
		t.Fatalf("problem 1 = %#v, want %#v", discovery.Problems[1], want)
	}
}

func TestDiscoveryGroupsNameKindExtFilesAtARootsTopByNameOneFacetPerFileWithoutPackageJSON(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, root, map[string]string{
		"hello.server.ts":  "",
		"hello.web.tsx":    "",
		"clock.worker.mjs": "",
		"notes.md":         "",
		"helper.ts":        "",
		"other.thing.ts":   "",
	})
	discovery := discovered(t, plainRoots(root))
	canon := realOf(t, root)
	if len(discovery.Problems) != 0 {
		t.Fatalf("problems = %v", discovery.Problems)
	}
	of := Plugin{Dir: canon, Root: canon, Layout: LayoutFile, Dependencies: []string{}, External: []string{}}
	clock, hello := of, of
	clock.Name, clock.Facets = "clock", map[string]string{"worker": filepath.Join(canon, "clock.worker.mjs")}
	hello.Name, hello.Facets = "hello", map[string]string{"server": filepath.Join(canon, "hello.server.ts"), "web": filepath.Join(canon, "hello.web.tsx")}
	if want := []Plugin{clock, hello}; !reflect.DeepEqual(discovery.Plugins, want) {
		t.Fatalf("plugins = %#v\nwant %#v", discovery.Plugins, want)
	}
}

func TestDiscoveryReportsAnInvalidNameAndTwoFilesOfAKindADirectoryPluginOfTheSameNameWins(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, root, map[string]string{
		"Bad.server.ts":   "",
		"twice.server.ts": "",
		"twice.server.js": "",
		"notes.server.ts": "",
	})
	dir := writePlugin(t, root, "zz-notes", "notes", map[string]string{"server.ts": ""}, nil)
	discovery := discovered(t, plainRoots(root))
	canon := realOf(t, root)
	type row struct {
		name   string
		layout PluginLayout
		dir    string
	}
	got := []row{}
	for _, plugin := range discovery.Plugins {
		got = append(got, row{plugin.Name, plugin.Layout, plugin.Dir})
	}
	if want := []row{{"notes", LayoutDirectory, realOf(t, dir)}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
	want := [][2]string{
		{filepath.Join(canon, "Bad.server.ts"), `"Bad" is not a valid unscoped plugin name`},
		{filepath.Join(canon, "twice.server.js"), "two server facets: twice.server.js, twice.server.ts"},
		{filepath.Join(canon, "notes.server.ts"), `plugin "notes" is in ` + realOf(t, dir) + " already"},
	}
	if got := pathsAndErrors(discovery.Problems); !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %q, want %q", got, want)
	}
}

// Not upstream's: sameFiles compares directory, layout and facet files (not the other fields).
func TestSameFilesComparesDirectoryLayoutAndFacetFiles(t *testing.T) {
	base := Plugin{Name: "a", Dir: "/d", Root: "/r", Layout: LayoutDirectory, Facets: map[string]string{"server": "/d/server.ts"}}
	same := Plugin{Name: "b", Dir: "/d", Root: "/other", Required: true, Layout: LayoutDirectory, Facets: map[string]string{"server": "/d/server.ts"}}
	if !SameFiles(base, same) {
		t.Fatal("plugins of one directory, layout and facets differ")
	}
	for name, other := range map[string]Plugin{
		"dir":    {Dir: "/e", Layout: LayoutDirectory, Facets: base.Facets},
		"layout": {Dir: "/d", Layout: LayoutFile, Facets: base.Facets},
		"facets": {Dir: "/d", Layout: LayoutDirectory, Facets: map[string]string{"server": "/d/server.js"}},
		"kinds":  {Dir: "/d", Layout: LayoutDirectory, Facets: map[string]string{"worker": "/d/server.ts"}},
	} {
		if SameFiles(base, other) {
			t.Errorf("a changed %s is the same files", name)
		}
	}
}

// Not upstream's: matchesGlob's tokens (`**/` optional folders, `**`, `*`, `?`, literals) match as the RegExp they build does.
func TestMatchesGlobStarsStayInASegmentDoubleStarsCrossThemQuestionMarksMatchOneUnit(t *testing.T) {
	for _, row := range []struct {
		path, pattern string
		want          bool
	}{
		{"a/b", "a/*", true},
		{"a/b/c", "a/*", false},
		{"a/b/c", "a/**", true},
		{"a", "a/**", false},
		{"a/p1", "a/**/p?", true},
		{"a/x/y/p1", "a/**/p?", true},
		{"a/p12", "a/**/p?", false},
		{"a.b", "a.b", true},
		{"axb", "a.b", false},
		{"a/😀", "a/?", false},
		{"a/\n/b", "a/**/b", false},
		{".", ".", true},
	} {
		if got := matchesGlob(row.path, row.pattern); got != row.want {
			t.Errorf("matchesGlob(%q, %q) = %v, want %v", row.path, row.pattern, got, row.want)
		}
	}
}
