package plugins

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/next-node/plugins/discover.ts. Discovery (slice 7c §1): the plugins under the app's roots, found
// by listing each root one level deep and reading only each directory's `package.json` (its `name`, its `dependencies`,
// its `chord.external`); its facet files are the ones named after the app's facet kinds. Nothing parses or runs plugin
// code.

// Plugin is a plugin on disk: its name (its `package.json` name), its real directory, its facet files and npm packages.
// Every list is non-nil, as upstream's arrays are. Facets has one entry per kind the plugin has a file for; a kind order
// is the order of DiscoveryOptions.Kinds.
type Plugin struct {
	Name string
	// Dir is its directory's real path (symlinks followed).
	Dir string
	// Facets are its facet files by kind, absolute paths; none for a contracts-only plugin.
	Facets map[string]string
	// Dependencies are the packages in its `dependencies`: bundled into the facets that import them.
	Dependencies []string
	// External are the packages in its `chord.external`: kept external in Node builds, loaded from its `node_modules`.
	External []string
}

// DiscoveryProblem is a directory under a root that is not a plugin, or a plugin left out, and why.
type DiscoveryProblem struct {
	Path  string
	Error string
}

// Discovery is what discovery found: the plugins by name, and the directories it reported.
type Discovery struct {
	Plugins  []Plugin
	Problems []DiscoveryProblem
}

// DiscoveryOptions is what discovery takes besides the roots: the app's facet kinds (`["server", "worker", "web",
// "tui"]`).
type DiscoveryOptions struct {
	Kinds []string
}

// DiscoverPlugins lists every root one level deep: a directory with a valid `package.json` is a plugin (a missing root is
// empty). A directory without a `package.json`, with an invalid one or an invalid name, with two files of one facet kind,
// or whose name an earlier root (or an earlier directory of the same root) has already, is reported. Plugins sorted by
// name. It returns the abort's reason (context.Cause) when ctx aborts, and the error of a directory it cannot read.
func DiscoverPlugins(roots []string, options DiscoveryOptions, ctx context.Context) (Discovery, error) {
	plugins := map[string]Plugin{}
	problems := []DiscoveryProblem{}
	for _, root := range roots {
		dirs, err := directoriesOf(root)
		if err != nil {
			return Discovery{}, err
		}
		for _, dir := range dirs {
			if err := ctx.Err(); err != nil {
				return Discovery{}, context.Cause(ctx)
			}
			plugin, problem, err := pluginAt(dir, options.Kinds)
			if err != nil {
				return Discovery{}, err
			}
			if problem != "" {
				problems = append(problems, DiscoveryProblem{Path: dir, Error: problem})
				continue
			}
			if other, ok := plugins[plugin.Name]; ok {
				problems = append(problems, DiscoveryProblem{Path: dir, Error: `plugin "` + plugin.Name + `" is in ` + other.Dir + " already"})
				continue
			}
			plugins[plugin.Name] = plugin
		}
	}
	sorted := make([]Plugin, 0, len(plugins))
	for _, plugin := range plugins {
		sorted = append(sorted, plugin)
	}
	slices.SortFunc(sorted, func(a, b Plugin) int { return compareUTF16(a.Name, b.Name) })
	return Discovery{Plugins: sorted, Problems: problems}, nil
}

// compareUTF16 orders strings as JavaScript's `<` and the default `sort` do: by UTF-16 code unit.
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

// directoriesOf is the directories directly under root, by real path, in name order; none when root does not exist.
func directoriesOf(root string) ([]string, error) {
	listing, err := os.Open(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	names, err := listing.Readdirnames(-1)
	listing.Close()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(names, compareUTF16)
	dirs := []string{}
	for _, name := range names {
		// Not plugins: hidden directories and a root's own packages.
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		path := filepath.Join(root, name)
		// `os.Stat` follows a symlink: a linked plugin directory is listed by its real path.
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			real, err := realpath(path)
			if err != nil {
				return nil, err
			}
			dirs = append(dirs, real)
		}
	}
	return dirs, nil
}

// realpath is path absolute and with its symlinks followed.
func realpath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// facetExtensions are the facet files' extensions, in no order of preference: two files of one kind are reported.
var facetExtensions = []string{".ts", ".tsx", ".js", ".mjs"}

// pluginNamePattern is an npm package name, scoped or not: upstream's `isPluginName` of host/staging.ts.
var pluginNamePattern = regexp.MustCompile(`^(?:@[a-z0-9~-][a-z0-9._~-]*/)?[a-z0-9~-][a-z0-9._~-]*$`)

// isPluginName reports whether name is a valid plugin name.
func isPluginName(name string) bool { return pluginNamePattern.MatchString(name) }

// pluginAt is the plugin in dir (its real path), or the problem that is none (a non-empty text).
func pluginAt(dir string, kinds []string) (Plugin, string, error) {
	text, err := os.ReadFile(filepath.Join(dir, "package.json"))
	var manifest delta.JsonValue
	if err == nil {
		manifest, err = delta.DecodeJson(text)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Plugin{}, "no package.json", nil
		}
		return Plugin{}, "package.json: " + err.Error(), nil
	}
	// A trust boundary: a plugin's own `package.json`. Anything but an object has none of the three.
	object, _ := manifest.(*delta.JsonObject)
	member := func(object *delta.JsonObject, key string) (any, bool) {
		if object == nil {
			return nil, false
		}
		return object.Get(key)
	}
	name, hasName := member(object, "name")
	nameText, isText := name.(string)
	if !isText || !isPluginName(nameText) {
		return Plugin{}, "package.json: " + stringify(name, hasName) + " is not a valid plugin name", nil
	}
	chord, _ := member(object, "chord")
	chordObject, _ := chord.(*delta.JsonObject)
	external := []string{}
	if listed, present := member(chordObject, "external"); present && listed != nil {
		items, isList := listed.([]any)
		if !isList {
			return Plugin{}, "package.json: chord.external is not a list of package names", nil
		}
		for _, item := range items {
			text, isText := item.(string)
			if !isText {
				return Plugin{}, "package.json: chord.external is not a list of package names", nil
			}
			external = append(external, text)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Plugin{}, "", err
	}
	files := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		files[entry.Name()] = struct{}{}
	}
	facets := map[string]string{}
	for _, kind := range kinds {
		named := []string{}
		for _, extension := range facetExtensions {
			if _, ok := files[kind+extension]; ok {
				named = append(named, kind+extension)
			}
		}
		if len(named) > 1 {
			return Plugin{}, "two " + kind + " facets: " + strings.Join(named, ", "), nil
		}
		if len(named) == 1 {
			facets[kind] = filepath.Join(dir, named[0])
		}
	}
	dependencies, _ := member(object, "dependencies")
	return Plugin{Name: nameText, Dir: dir, Facets: facets, Dependencies: keysOf(dependencies), External: external}, "", nil
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

// stringify is `JSON.stringify(value)` as a template literal shows it: present false is `undefined`.
func stringify(value any, present bool) string {
	if !present {
		return "undefined"
	}
	text, err := chordjson.Marshal(value)
	if err != nil {
		return "undefined"
	}
	return string(text)
}
