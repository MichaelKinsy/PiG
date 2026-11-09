package model

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/node/plugins/discover.ts. Discovery (slice 7c §1, slice 12 §2, §4): the plugins under the
// app's roots, found by listing each root and reading only `package.json` files. Each directory at a root's top is a
// package: the plugins its `chord.plugins` manifest lists (decision "`chord.plugins` is a package's manifest of plugins"),
// else by convention itself when its top has facet files, else its subfolders with a `package.json` (a checkout of
// several), else itself (contracts only). Files named `<name>.<kind>.<ext>` at a root's top are single-file plugins grouped
// by name. Facet files are the ones named after the app's facet kinds. Nothing parses or runs plugin code.

// PluginRoot is a root plugins are listed in: a path, or a path marked `required` (a plugin of it failing at `load` is
// fatal). Upstream's `string | { path, required? }`: the string form is PluginRoot{Path: path}.
type PluginRoot struct {
	Path     string
	Required bool
}

// PluginLayout is how a plugin is laid out on disk: "directory" (a directory with a `package.json`) or "file" (files at a
// root's top, `hello.server.ts`: host packages only, no files).
type PluginLayout string

const (
	LayoutDirectory PluginLayout = "directory"
	LayoutFile      PluginLayout = "file"
)

// Plugin is a plugin on disk: its name, its real directory, its root, its facet files and npm packages. Every list is
// non-nil, as upstream's arrays are. Facets has one entry per kind the plugin has a file for.
type Plugin struct {
	Name string
	// Dir is its directory's real path (symlinks followed); a single-file plugin's is its root's.
	Dir string
	// Root is the real path of the root it was found in: relative imports between plugins stay within it.
	Root string
	// Required is whether its root is required: its build, evaluation or setup failing at `load` rejects the load.
	Required bool
	// Layout is "directory" (a directory with a `package.json`) or "file" (files at a root's top).
	Layout PluginLayout
	// Facets are its facet files by kind, absolute paths; none for a contracts-only plugin.
	Facets map[string]string
	// Dependencies are the packages in its `dependencies`: bundled into the facets that import them.
	Dependencies []string
	// External are the packages in its `chord.external`: kept external in Node builds, loaded from its `node_modules`.
	External []string
}

// DiscoveryProblem is a root, directory or file that is not a plugin, a plugin left out, or a required root missing, and
// why; with the root it is in (a required root's problems make `plugins.load` reject).
type DiscoveryProblem struct {
	Path     string
	Error    string
	Root     string
	Required bool
}

// Discovery is what discovery found: the plugins by name, and the paths it reported.
type Discovery struct {
	Plugins  []Plugin
	Problems []DiscoveryProblem
}

// DiscoveryOptions is what discovery takes besides the roots: the app's facet kinds (`["server", "worker", "web",
// "tui"]`).
type DiscoveryOptions struct {
	Kinds []string
}

// RootContext is where a root's plugins are listed from, and what each records: its root's real path, requiredness, the
// kinds.
type RootContext struct {
	Root     string
	Required bool
	Kinds    []string
}

// DiscoverPlugins lists every root: its packages' plugins (by their manifests or by convention) and its single-file
// plugins. Reported, not plugins: a directory without any `package.json`, an invalid one, name or manifest, two files of
// one facet kind, a name an earlier root (or an earlier entry of the same root) has already; a single-file plugin named
// like a directory plugin of its root loses to it; a required root that does not exist. A missing root that is not required
// is empty. Plugins sorted by name. It returns the abort's reason (context.Cause) when ctx aborts (checked before each
// package), and the error of a directory it cannot read.
func DiscoverPlugins(roots []PluginRoot, options DiscoveryOptions, ctx context.Context) (Discovery, error) {
	plugins := map[string]Plugin{}
	paths := map[string]string{}
	problems := []DiscoveryProblem{}
	for _, each := range roots {
		listed, err := listRoot(each.Path, each.Required, options.Kinds, ctx)
		if err != nil {
			return Discovery{}, err
		}
		problems = append(problems, listed.problems...)
		for i, plugin := range listed.plugins {
			if other, ok := plugins[plugin.Name]; ok {
				problems = append(problems, DiscoveryProblem{
					Path: listed.paths[i], Error: `plugin "` + plugin.Name + `" is in ` + other.Dir + " already", Root: plugin.Root, Required: plugin.Required,
				})
				continue
			}
			plugins[plugin.Name] = plugin
			paths[plugin.Name] = listed.paths[i]
		}
	}
	sorted := make([]Plugin, 0, len(plugins))
	for _, plugin := range plugins {
		sorted = append(sorted, plugin)
	}
	slices.SortFunc(sorted, func(a, b Plugin) int { return CompareUTF16(a.Name, b.Name) })
	return Discovery{Plugins: sorted, Problems: problems}, nil
}

// DiscoverPackage is the plugins of the package in dir (its manifest's folders, or by convention) and its problems,
// recorded as of at.Root: what InstallPlugin checks a package with before it moves it into a root. It returns the abort's
// reason (context.Cause) when ctx is aborted.
func DiscoverPackage(dir string, at RootContext, ctx context.Context) (Discovery, error) {
	if ctx.Err() != nil {
		return Discovery{}, context.Cause(ctx)
	}
	plugins := []Plugin{}
	problems := []DiscoveryProblem{}
	real, err := Realpath(dir)
	if err != nil {
		return Discovery{}, err
	}
	found, err := packageAt(real, at)
	if err != nil {
		return Discovery{}, err
	}
	for _, each := range found {
		if each.plugin != nil {
			plugins = append(plugins, *each.plugin)
		} else {
			problems = append(problems, DiscoveryProblem{Path: each.path, Error: each.error, Root: at.Root, Required: at.Required})
		}
	}
	return Discovery{Plugins: plugins, Problems: problems}, nil
}

// SameFiles is whether two records of one plugin name the same directory, layout and facet files (else its code moved).
func SameFiles(before, after Plugin) bool {
	return before.Dir == after.Dir && before.Layout == after.Layout && maps.Equal(before.Facets, after.Facets)
}

// FacetExtensions are the facet files' extensions, in no order of preference: two files of one kind are reported.
var FacetExtensions = []string{".ts", ".tsx", ".js", ".mjs"}

// rootListing is the plugins of one root, directory plugins first, with the path each is reported at (upstream's
// `pathOf`: a directory plugin's directory, a single-file plugin's first facet file), and its problems.
type rootListing struct {
	plugins  []Plugin
	paths    []string
	problems []DiscoveryProblem
}

// listRoot is the plugins of one root, directory plugins first, and its problems.
func listRoot(path string, required bool, kinds []string, ctx context.Context) (rootListing, error) {
	dirs, files, exists, err := entriesOf(path)
	if err != nil {
		return rootListing{}, err
	}
	if !exists {
		if !required {
			return rootListing{}, nil
		}
		return rootListing{problems: []DiscoveryProblem{{Path: path, Error: "the required root does not exist", Root: path, Required: required}}}, nil
	}
	root, err := Realpath(path)
	if err != nil {
		return rootListing{}, err
	}
	at := RootContext{Root: root, Required: required, Kinds: kinds}
	listed := rootListing{}
	for _, dir := range dirs {
		found, err := DiscoverPackage(dir, at, ctx)
		if err != nil {
			return rootListing{}, err
		}
		for _, plugin := range found.Plugins {
			listed.plugins = append(listed.plugins, plugin)
			listed.paths = append(listed.paths, plugin.Dir)
		}
		listed.problems = append(listed.problems, found.Problems...)
	}
	single := singleFilePlugins(files, at)
	listed.problems = append(listed.problems, single.problems...)
	for i, plugin := range single.plugins {
		directory := slices.IndexFunc(listed.plugins, func(each Plugin) bool { return each.Name == plugin.Name })
		if directory < 0 {
			listed.plugins = append(listed.plugins, plugin)
			listed.paths = append(listed.paths, single.paths[i])
			continue
		}
		listed.problems = append(listed.problems, DiscoveryProblem{
			Path: single.paths[i], Error: `plugin "` + plugin.Name + `" is in ` + listed.plugins[directory].Dir + " already", Root: root, Required: required,
		})
	}
	return listed, nil
}

// found is a plugin, or why a directory is none (path and error): exactly one of plugin and error is set.
type found struct {
	plugin *Plugin
	path   string
	error  string
}

// CompareUTF16 orders strings as JavaScript's `<` and the default `sort` do: by UTF-16 code unit.
func CompareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

// entriesOf is the directories (by real path) and plain files directly under path, in name order; exists is false when it
// does not exist. Hidden entries and `node_modules` are not plugins.
func entriesOf(path string) (dirs, files []string, exists bool, err error) {
	listing, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	names, err := listing.Readdirnames(-1)
	// A directory opened for reading: its close cannot lose data.
	_ = listing.Close()
	if err != nil {
		return nil, nil, false, err
	}
	slices.SortFunc(names, CompareUTF16)
	dirs = []string{}
	files = []string{}
	for _, name := range names {
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		entry := filepath.Join(path, name)
		// `os.Stat` follows a symlink: a linked plugin directory is listed by its real path.
		info, statErr := os.Stat(entry)
		if statErr != nil {
			continue
		}
		if info.IsDir() {
			real, err := Realpath(entry)
			if err != nil {
				return nil, nil, false, err
			}
			dirs = append(dirs, real)
		} else if info.Mode().IsRegular() {
			files = append(files, name)
		}
	}
	return dirs, files, true, nil
}

// Realpath is path absolute and with its symlinks followed.
func Realpath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// manifest is a package's `package.json`: missing, unreadable (why), or its value (a trust boundary, checked where it is
// read).
type manifest struct {
	kind  manifestState
	error string
	value delta.JsonValue
}

type manifestState int

const (
	manifestMissing manifestState = iota
	manifestInvalid
	manifestRead
)

// manifestOf is the `package.json` of dir.
func manifestOf(dir string) manifest {
	text, err := os.ReadFile(filepath.Join(dir, "package.json"))
	var value delta.JsonValue
	if err == nil {
		value, err = delta.DecodeJson(text)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return manifest{kind: manifestMissing}
		}
		return manifest{kind: manifestInvalid, error: "package.json: " + err.Error()}
	}
	return manifest{kind: manifestRead, value: value}
}

// Member is `object[key]` of a `package.json` value that may be no object: present false is `undefined`.
func Member(object *delta.JsonObject, key string) (any, bool) {
	if object == nil {
		return nil, false
	}
	return object.Get(key)
}

// packageAt is the plugins of the package in dir (a real path): its manifest's folders; without a `chord.plugins` itself
// when its top has facet files, else its subfolders with a `package.json`, else itself; without a `package.json` its
// subfolders with one (a checkout of several), else a problem.
func packageAt(dir string, at RootContext) ([]found, error) {
	read := manifestOf(dir)
	if read.kind == manifestInvalid {
		return []found{{path: dir, error: read.error}}, nil
	}
	var listed any
	var hasListed bool
	if read.kind == manifestRead {
		object, _ := read.value.(*delta.JsonObject)
		chord, _ := Member(object, "chord")
		chordObject, _ := chord.(*delta.JsonObject)
		listed, hasListed = Member(chordObject, "plugins")
	}
	if hasListed {
		return manifestPlugins(dir, listed, at)
	}
	if read.kind == manifestRead {
		has, err := hasFacetFile(dir, at.Kinds)
		if err != nil {
			return nil, err
		}
		if has {
			plugin, err := pluginAt(dir, read, at)
			return []found{plugin}, err
		}
	}
	inner := []found{}
	subs, _, _, err := entriesOf(dir)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if sibling := manifestOf(sub); sibling.kind != manifestMissing {
			plugin, err := pluginAt(sub, sibling, at)
			if err != nil {
				return nil, err
			}
			inner = append(inner, plugin)
		}
	}
	if len(inner) > 0 {
		return inner, nil
	}
	if read.kind == manifestRead {
		plugin, err := pluginAt(dir, read, at)
		return []found{plugin}, err
	}
	return []found{{path: dir, error: "no package.json"}}, nil
}

// hasFacetFile is whether dir's top has a facet file of one of kinds.
func hasFacetFile(dir string, kinds []string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	files := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		files[entry.Name()] = struct{}{}
	}
	return slices.ContainsFunc(kinds, func(kind string) bool {
		return slices.ContainsFunc(FacetExtensions, func(extension string) bool {
			_, ok := files[kind+extension]
			return ok
		})
	}), nil
}

// leaves is whether folder lies outside the package dir: its relative path starts with `..` or is absolute (a path on
// another drive).
func leaves(dir, folder string) bool {
	rest, err := Relative(dir, folder)
	return err != nil || HasDotDotPrefix(rest) || filepath.IsAbs(rest)
}

// manifestPlugins is the plugins `chord.plugins` (listed: folders of the package, as paths, globs and `!` exclusions, as
// pi's `pi.extensions`) of the package in dir names: each a folder with a `package.json` (the package itself when listed as
// `.`). A manifest that is no list of strings, an entry or a link leaving the package, a listed path that is no directory,
// and a listing of nothing are problems.
func manifestPlugins(dir string, listed any, at RootContext) ([]found, error) {
	problem := func(error string) ([]found, error) {
		return []found{{path: dir, error: "package.json: chord.plugins " + error}}, nil
	}
	items, isList := listed.([]any)
	entries := make([]string, 0, len(items))
	for _, item := range items {
		text, isText := item.(string)
		if !isText {
			isList = false
			break
		}
		entries = append(entries, text)
	}
	if !isList {
		return problem("is not a list of plugin folders")
	}
	excludes := []string{}
	for _, each := range entries {
		if strings.HasPrefix(each, "!") {
			excludes = append(excludes, Posix(each[1:]))
		}
	}
	seen := map[string]struct{}{}
	folders := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry, "!") {
			continue
		}
		var matched []string
		if strings.ContainsAny(entry, "*?") {
			matched = globFolders(dir, Posix(entry))
		} else {
			matched = []string{ResolvePath(dir, entry)}
		}
		for _, folder := range matched {
			if leaves(dir, folder) {
				return problem(`"` + entry + `" leaves the package`)
			}
			rest, _ := Relative(dir, folder)
			rest = Posix(rest)
			if rest == "" {
				rest = "."
			}
			if !slices.ContainsFunc(excludes, func(exclude string) bool { return matchesGlob(rest, exclude) }) {
				if _, ok := seen[folder]; !ok {
					seen[folder] = struct{}{}
					folders = append(folders, folder)
				}
			}
		}
	}
	// One plugin inside another's directory would make the outer one's imports of it its own files, not copies.
	sorted := slices.Clone(folders)
	slices.SortFunc(sorted, CompareUTF16)
	for _, inner := range sorted {
		if outer := slices.IndexFunc(sorted, func(folder string) bool { return Inside(folder, inner) }); outer >= 0 {
			name := func(folder string) string {
				rest, _ := Relative(dir, folder)
				if rest = Posix(rest); rest == "" {
					return "."
				}
				return rest
			}
			return problem(`lists "` + name(inner) + `" inside the listed "` + name(sorted[outer]) + `"`)
		}
	}
	if len(folders) == 0 {
		return problem("lists no plugin folder")
	}
	result := []found{}
	for _, folder := range sorted {
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			result = append(result, found{path: folder, error: "listed in chord.plugins, not a directory"})
			continue
		}
		real, err := Realpath(folder)
		if err != nil {
			return nil, err
		}
		if leaves(dir, real) {
			result = append(result, found{path: folder, error: "listed in chord.plugins, links out of the package"})
			continue
		}
		plugin, err := pluginAt(real, manifestOf(real), at)
		if err != nil {
			return nil, err
		}
		result = append(result, plugin)
	}
	return result, nil
}

// Posix is path with `/` separators and no leading `./` (as manifests and globs write paths).
func Posix(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "./")
}

// matchesGlob is whether the `/`-separated path matches the glob pattern: `*` and `?` within one segment, `**` across
// segments (`node:path`'s `matchesGlob` is experimental in Node 22, Chord's floor); anything else literal. Upstream builds a
// RegExp from the pattern and tests it on UTF-16 code units, where `.` is none of the line terminators; this matches the
// same units with the same tokens.
func matchesGlob(path, pattern string) bool {
	const (
		tokenFolders = iota // `(?:.*/)?`
		tokenAny            // `.*`
		tokenSegment        // `[^/]*`
		tokenOne            // `[^/]`
		tokenLiteral
	)
	type token struct {
		kind int
		unit uint16
	}
	units := utf16.Encode([]rune(pattern))
	tokens := []token{}
	for at := 0; at < len(units); at++ {
		switch {
		case slices.Equal(units[at:min(at+3, len(units))], []uint16{'*', '*', '/'}):
			tokens = append(tokens, token{kind: tokenFolders})
			at += 2
		case slices.Equal(units[at:min(at+2, len(units))], []uint16{'*', '*'}):
			tokens = append(tokens, token{kind: tokenAny})
			at++
		case units[at] == '*':
			tokens = append(tokens, token{kind: tokenSegment})
		case units[at] == '?':
			tokens = append(tokens, token{kind: tokenOne})
		default:
			tokens = append(tokens, token{kind: tokenLiteral, unit: units[at]})
		}
	}
	text := utf16.Encode([]rune(path))
	var match func(t, i int) bool
	match = func(t, i int) bool {
		if t == len(tokens) {
			return i == len(text)
		}
		switch tokens[t].kind {
		case tokenFolders:
			if match(t+1, i) {
				return true
			}
			// `.*/`: any run of non-terminators, then a `/` (itself matched by `.`).
			for end := i; end < len(text); end++ {
				if text[end] == '/' && match(t+1, end+1) {
					return true
				}
				if !regexpDot(text[end]) {
					break
				}
			}
			return false
		case tokenAny:
			for end := i; ; end++ {
				if match(t+1, end) {
					return true
				}
				if end >= len(text) || !regexpDot(text[end]) {
					return false
				}
			}
		case tokenSegment:
			for end := i; ; end++ {
				if match(t+1, end) {
					return true
				}
				if end >= len(text) || text[end] == '/' {
					return false
				}
			}
		case tokenOne:
			return i < len(text) && text[i] != '/' && match(t+1, i+1)
		}
		return i < len(text) && text[i] == tokens[t].unit && match(t+1, i+1)
	}
	return match(0, 0)
}

// regexpDot is whether JavaScript's `.` (without the `s` flag) matches the code unit: all but the line terminators.
func regexpDot(unit uint16) bool {
	return unit != '\n' && unit != '\r' && unit != 0x2028 && unit != 0x2029
}

// globDepth is how deep a glob of `chord.plugins` looks for folders.
const globDepth = 6

// globFolders is the folders under dir (hidden ones and `node_modules` skipped, globDepth deep) whose path matches
// pattern.
func globFolders(dir, pattern string) []string {
	matched := []string{}
	var walk func(at string, depth int)
	walk = func(at string, depth int) {
		if depth > globDepth {
			return
		}
		entries, err := os.ReadDir(at)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				continue
			}
			path := filepath.Join(at, entry.Name())
			rest, _ := Relative(dir, path)
			if matchesGlob(Posix(rest), pattern) {
				matched = append(matched, path)
			}
			walk(path, depth+1)
		}
	}
	walk(dir, 1)
	return matched
}

// pluginAt is the plugin in dir (its real path) with its `package.json` read as read, or why it is none.
func pluginAt(dir string, read manifest, at RootContext) (found, error) {
	problem := func(error string) (found, error) { return found{path: dir, error: error}, nil }
	if read.kind == manifestMissing {
		return problem("no package.json")
	}
	if read.kind == manifestInvalid {
		return problem(read.error)
	}
	// A trust boundary: a plugin's own `package.json`. Anything but an object has none of the three.
	object, _ := read.value.(*delta.JsonObject)
	name, hasName := Member(object, "name")
	nameText, isText := name.(string)
	if !isText || !host.IsPluginName(nameText) {
		return problem("package.json: " + Stringify(name, hasName) + " is not a valid plugin name")
	}
	chord, _ := Member(object, "chord")
	chordObject, _ := chord.(*delta.JsonObject)
	external := []string{}
	if listed, present := Member(chordObject, "external"); present && listed != nil {
		items, isList := listed.([]any)
		if !isList {
			return problem("package.json: chord.external is not a list of package names")
		}
		for _, item := range items {
			text, isText := item.(string)
			if !isText {
				return problem("package.json: chord.external is not a list of package names")
			}
			external = append(external, text)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return found{}, err
	}
	files := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		files[entry.Name()] = struct{}{}
	}
	facets := map[string]string{}
	for _, kind := range at.Kinds {
		named := []string{}
		for _, extension := range FacetExtensions {
			if _, ok := files[kind+extension]; ok {
				named = append(named, kind+extension)
			}
		}
		if len(named) > 1 {
			return problem("two " + kind + " facets: " + strings.Join(named, ", "))
		}
		if len(named) == 1 {
			facets[kind] = filepath.Join(dir, named[0])
		}
	}
	dependencies, _ := Member(object, "dependencies")
	return found{plugin: &Plugin{
		Name: nameText, Dir: dir, Root: at.Root, Required: at.Required, Layout: LayoutDirectory,
		Facets: facets, Dependencies: keysOf(dependencies), External: external,
	}}, nil
}

// singleFiles is the single-file plugins among a root's top files, the path each is reported at, and the problems.
type singleFiles struct {
	plugins  []Plugin
	paths    []string
	problems []DiscoveryProblem
}

// singleFilePlugins is the single-file plugins among a root's top files: `<name>.<kind>.<ext>` with `<kind>` one of the
// app's kinds and `<ext>` a facet extension, grouped by `<name>` (an unscoped plugin name, else reported; two files of one
// kind reported). Other files are not plugins.
func singleFilePlugins(files []string, at RootContext) singleFiles {
	type group struct {
		name  string
		kinds []string
		files map[string][]string
	}
	groups := []*group{}
	byName := map[string]*group{}
	for _, file := range files {
		name, kind, ok := singleFileName(file, at.Kinds)
		if !ok {
			continue
		}
		each, seen := byName[name]
		if !seen {
			each = &group{name: name, files: map[string][]string{}}
			byName[name] = each
			groups = append(groups, each)
		}
		if _, seen := each.files[kind]; !seen {
			each.kinds = append(each.kinds, kind)
		}
		each.files[kind] = append(each.files[kind], file)
	}
	result := singleFiles{}
	for _, each := range groups {
		first := filepath.Join(at.Root, each.files[each.kinds[0]][0])
		if !host.IsPluginName(each.name) || strings.HasPrefix(each.name, "@") {
			result.problems = append(result.problems, DiscoveryProblem{Path: first, Error: `"` + each.name + `" is not a valid unscoped plugin name`, Root: at.Root, Required: at.Required})
			continue
		}
		twice := slices.IndexFunc(each.kinds, func(kind string) bool { return len(each.files[kind]) > 1 })
		if twice >= 0 {
			kind := each.kinds[twice]
			result.problems = append(result.problems, DiscoveryProblem{Path: first, Error: "two " + kind + " facets: " + strings.Join(each.files[kind], ", "), Root: at.Root, Required: at.Required})
			continue
		}
		facets := map[string]string{}
		for _, kind := range each.kinds {
			facets[kind] = filepath.Join(at.Root, each.files[kind][0])
		}
		result.plugins = append(result.plugins, Plugin{
			Name: each.name, Dir: at.Root, Root: at.Root, Required: at.Required, Layout: LayoutFile,
			Facets: facets, Dependencies: []string{}, External: []string{},
		})
		result.paths = append(result.paths, first)
	}
	return result
}

// singleFileName is the plugin name and kind of a single-file plugin's file name (`hello.server.ts`); ok is false for
// another file.
func singleFileName(file string, kinds []string) (name, kind string, ok bool) {
	i := slices.IndexFunc(FacetExtensions, func(each string) bool { return strings.HasSuffix(file, each) })
	if i < 0 {
		return "", "", false
	}
	stem := strings.TrimSuffix(file, FacetExtensions[i])
	dot := strings.LastIndex(stem, ".")
	if dot <= 0 {
		return "", "", false
	}
	kind = stem[dot+1:]
	if !slices.Contains(kinds, kind) {
		return "", "", false
	}
	return stem[:dot], kind, true
}

// keysOf is `Object.keys(value)` of a `package.json` value: an object's own keys in JavaScript's order, an array's
// indices, none for anything else (`typeof dependencies === "object" && dependencies !== null ? … : []`).
func keysOf(value any) []string {
	switch typed := value.(type) {
	case *delta.JsonObject:
		return typed.Keys()
	case []any:
		keys := make([]string, len(typed))
		for i := range typed {
			keys[i] = strconv.Itoa(i)
		}
		return keys
	}
	return []string{}
}

// Stringify is `JSON.stringify(value)` as a template literal shows it: present false is `undefined`.
func Stringify(value any, present bool) string {
	if !present {
		return "undefined"
	}
	text, err := chordjson.Marshal(value)
	if err != nil {
		return "undefined"
	}
	return string(text)
}
