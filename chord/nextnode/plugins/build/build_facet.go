package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/src/next-node/plugins/build.ts (`buildFacet`, `ImportRules`, `freeGlobals`, `describe`) on esbuild's
// Go API (`github.com/evanw/esbuild/pkg/api`, Pi's pin 0.28.2). Building (slice 7c §2, slice 12 §1-§4): one facet of one
// plugin as one script, by esbuild from cold. Its own files, files of other plugins of its root imported by relative
// path (copies; decision "Contracts come from where the plugin's code came from") and its npm `dependencies` (through
// their `exports`) are inlined; host packages, this component, `chord.external` packages and Node builtins stay external,
// answered at evaluation by the `require` the script takes. Every import is checked by the file it resolves to: a
// per-kind file only for its kind, the app's API only at a listed version the kind provides, one version per facet,
// never Chord directly. The result is plain data, the same value a worker or a page evaluates where it runs; a failed
// build is a result, never an error.

// pluginsSpecifier is the one specifier of this component a script imports (`pluginFile`): external always, answered per
// facet (evaluate.ts's PLUGINS_SPECIFIER).
const pluginsSpecifier = "@earendil-works/chord/plugins"

// globalMarkers are what a free `__dirname` and `__filename` become in a build: names no script has, found again by
// `freeGlobals`.
var globalMarkers = map[string]string{"__dirname": "__chord_plugin_dirname", "__filename": "__chord_plugin_filename"}

// BuildFacet builds plugin's facet of kind (one esbuild build from cold): a script wrapped as `(function (require,
// module, exports) { … })`, or the errors that refuse it (an import the rules do not admit, a syntax error,
// `import.meta`, `__dirname`, `__filename`, a literal dynamic `import("x")`, a top-level `await`), each naming its file and
// line. A computed `import(x)` becomes a call of the script's `require`. It returns an error only when ctx is aborted
// before the build (its cause, as `throwIfAborted`), when the facet file's real path cannot be read (upstream's `realpath`
// rejects), or when the script's own source map cannot be read.
func BuildFacet(plugin model.Plugin, kind string, options model.BuildOptions, ctx context.Context) (model.BuiltFacet, error) {
	failed := func(errors []string) model.BuiltFacet {
		return model.BuiltFacet{Kind: model.BuiltFacetFailed, Plugin: plugin.Name, Facet: kind, Errors: errors}
	}
	named, ok := plugin.Facets[kind]
	if !ok {
		return failed([]string{`plugin "` + plugin.Name + `" has no ` + kind + ` facet`}), nil
	}
	if ctx.Err() != nil {
		return model.BuiltFacet{}, context.Cause(ctx)
	}
	// A facet file may be a link: its real file must be the plugin's, and of no other kind.
	entry, err := filepath.EvalSymlinks(named)
	if err != nil {
		return model.BuiltFacet{}, err
	}
	rules := newImportRules(plugin, kind, entry, options, ctx)
	if wrongEntry := rules.checkEntry(named, entry); wrongEntry != "" {
		return failed([]string{wrongEntry}), nil
	}
	browser := options.Platform == model.PlatformBrowser
	publicPath := "/"
	if options.PublicPath != nil {
		publicPath = *options.PublicPath
	}
	platform := api.PlatformNode
	if browser {
		platform = api.PlatformBrowser
	}
	buildOptions := api.BuildOptions{
		EntryPoints:   []string{entry},
		AbsWorkingDir: plugin.Dir,
		// Nothing is written: the directory only makes the source map's paths relative to the plugin.
		Outdir:            plugin.Dir,
		EntryNames:        "[name]-[hash]",
		AssetNames:        "[name]-[hash]",
		PublicPath:        publicPath,
		Write:             false,
		Bundle:            true,
		Format:            api.FormatCommonJS,
		Platform:          platform,
		LegalComments:     api.LegalCommentsNone,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Sourcemap:         api.SourceMapInline,
		// Positions only: a stack is mapped to files and lines, never shown their text.
		SourcesContent: api.SourcesContentExclude,
		// Ignore any tsconfig.json: TypeScript is stripped, JSX compiled with the automatic React runtime.
		TsconfigRaw: "{}",
		JSX:         api.JSXAutomatic,
		Banner:      map[string]string{"js": "(function (require, module, exports) {"},
		Footer:      map[string]string{"js": "})"},
		// `import.meta` is refused here; a free `__dirname` or `__filename` becomes a marker found in the output below
		// (esbuild reports no `import.meta` it substituted itself).
		Define:      globalMarkers,
		LogOverride: map[string]api.LogLevel{"empty-import-meta": api.LogLevelError},
		// A computed `import(x)` (a literal one is refused below) goes through the facet's `require`, which names the
		// facet when it does not answer; `vm.Script` has no loader for a real one.
		Supported: map[string]bool{"dynamic-import": false},
		LogLevel:  api.LogLevelSilent,
		Plugins:   []api.Plugin{rules.plugin},
	}
	if browser {
		buildOptions.Target = api.ES2022
	} else {
		buildOptions.Engines = []api.Engine{{Name: api.EngineNode, Version: "22"}}
	}
	result := api.Build(buildOptions)
	// A CSS module whose mapping could not be read because ctx ended is an abort, not a failed build.
	if rules.aborted.Load() {
		return model.BuiltFacet{}, context.Cause(ctx)
	}
	if len(result.Errors) > 0 {
		errors := make([]string, len(result.Errors))
		for i, message := range result.Errors {
			errors[i] = describe(plugin, message)
		}
		return failed(errors), nil
	}
	// Never written: esbuild hands the outputs back.
	var script, css *api.OutputFile
	for i := range result.OutputFiles {
		file := &result.OutputFiles[i]
		if script == nil && strings.HasSuffix(file.Path, ".js") {
			script = file
		}
		if css == nil && strings.HasSuffix(file.Path, ".css") {
			css = file
		}
	}
	source := string(script.Contents)
	globals, err := freeGlobals(source)
	if err != nil {
		return model.BuiltFacet{}, err
	}
	if len(globals) > 0 {
		return failed(globals), nil
	}
	if mixed := rules.mixedVersions(); mixed != "" {
		return failed([]string{mixed}), nil
	}
	digest := sha256.Sum256([]byte(source))
	built := model.BuiltFacet{
		Kind:   model.BuiltFacetScript,
		Plugin: plugin.Name,
		Facet:  kind,
		Source: source,
		Digest: hex.EncodeToString(digest[:]),
		Inputs: rules.inputs(),
		Copied: rules.copied(),
		Assets: []model.Asset{},
	}
	if css != nil {
		built.CSS = &model.CSS{Name: filepath.Base(css.Path), Text: string(css.Contents)}
	}
	for i := range result.OutputFiles {
		file := &result.OutputFiles[i]
		if file == script || file == css {
			continue
		}
		name, err := model.Relative(plugin.Dir, file.Path)
		if err != nil {
			return model.BuiltFacet{}, err
		}
		built.Assets = append(built.Assets, model.Asset{Name: name, Bytes: file.Contents})
	}
	if options.Api != nil {
		built.Api = &model.BuiltApi{Name: options.Api.Name, Version: rules.version()}
	}
	return built, nil
}

// pluginFileHint is where a build error points for what a plugin script cannot have.
const pluginFileHint = `a file of the plugin is read through pluginFile(path) from "` + pluginsSpecifier + `"`

// noModuleIdentity is the error of a script using `import.meta`, `__dirname` or `__filename`.
const noModuleIdentity = "import.meta.url, __dirname and __filename are not available in a plugin script (" + pluginFileHint + ")"

// globalMarkerPattern finds the markers of a free `__dirname` and `__filename` in a built script.
var globalMarkerPattern = regexp.MustCompile(`__chord_plugin_(?:dirname|filename)`)

// freeGlobals is the errors for every free `__dirname` and `__filename` in the built source (their markers), each at its
// file, line and column through the script's source map. It returns an error when the script's inline source map cannot
// be read.
func freeGlobals(source string) ([]string, error) {
	// Most scripts have none: no map parsed for them.
	if !strings.Contains(source, "__chord_plugin_") {
		return nil, nil
	}
	m, err := inlineSourceMap(source)
	if err != nil || m == nil {
		return nil, err
	}
	var errors []string
	//portlint:allow pathseparators Pi's freeGlobals splits the built source on "\n" (build.ts:157): the source is esbuild's output, whose lines the source map counts
	for line, text := range strings.Split(source, "\n") {
		for _, match := range globalMarkerPattern.FindAllStringIndex(text, -1) {
			entry, found := m.findEntry(line, utf16Length(text[:match[0]]))
			if !found || !entry.hasSource {
				continue
			}
			errors = append(errors, entry.originalSource+":"+strconv.Itoa(entry.originalLine+1)+":"+strconv.Itoa(entry.originalColumn+1)+": "+noModuleIdentity)
		}
	}
	return errors, nil
}

// utf16Length is the length of text in UTF-16 code units (a JavaScript string's `length`, the unit of a column).
func utf16Length(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}

// Where a resolved file lies (a real path), from the plugin being built: its own source, another plugin's of its root (a
// copy), a package of its own `node_modules`, a plugin of another root, or no plugin.
type placeKind uint8

const (
	placeOwn placeKind = iota
	placeCopied
	placePackage
	placeOtherRoot
	placeOutside
)

type place struct {
	kind placeKind
	// plugin is the other plugin of a placeCopied or placeOtherRoot (identity: the same pointer is the same plugin).
	plugin *model.Plugin
}

// browserOnly is what only a browser build imports: stylesheets and wasm assets.
var browserOnly = map[string]bool{".css": true, ".wasm": true}

// resolving marks esbuild's resolution the rules ask for themselves, so they do not judge it again.
type resolving struct{}

// assetData is what an asset import's resolution carries to its load (the `file` loader whatever its extension).
type assetData struct{}

// loaders is how the source files a build reads are loaded, by extension (packages load as esbuild decides).
var loaders = map[string]api.Loader{
	".ts":   api.LoaderTS,
	".mts":  api.LoaderTS,
	".cts":  api.LoaderTS,
	".tsx":  api.LoaderTSX,
	".js":   api.LoaderJS,
	".mjs":  api.LoaderJS,
	".cjs":  api.LoaderJS,
	".jsx":  api.LoaderJSX,
	".json": api.LoaderJSON,
	".css":  api.LoaderCSS,
	".wasm": api.LoaderFile,
}

// chordPackage is Chord's package: never imported by plugins of an app with an API (only its `/plugins` for
// `pluginFile`).
const chordPackage = "@earendil-works/chord"

// manifestKind is what a directory's `package.json` is: absent, without a `chord` field, or with one.
type manifestKind uint8

const (
	manifestNone manifestKind = iota
	manifestPlain
	manifestChord
)

// listedPlugins is the plugin folders a `chord` package lists, or the error of reading it.
type listedPlugins struct {
	dirs map[string]bool
	err  error
}

// memo is a map of values computed once per key (upstream's map of promises): esbuild runs its callbacks on several
// goroutines, and the first to ask for a key computes it while the others wait for that value.
type memo[V any] struct {
	mu      sync.Mutex
	entries map[string]*memoEntry[V]
}

type memoEntry[V any] struct {
	once  sync.Once
	value V
}

func (m *memo[V]) get(key string, compute func() V) V {
	m.mu.Lock()
	if m.entries == nil {
		m.entries = map[string]*memoEntry[V]{}
	}
	entry, ok := m.entries[key]
	if !ok {
		entry = &memoEntry[V]{}
		m.entries[key] = entry
	}
	m.mu.Unlock()
	entry.once.Do(func() { entry.value = compute() })
	return entry.value
}

// importedVersion is an API version a facet imports, with its first importer.
type importedVersion struct {
	version  string
	importer string
}

// importRules is the import rules of one build (§2's table, slice 12 §1-§4) as an esbuild plugin, and what the build read:
// every file of a plugin hashed as it is handed to esbuild (the exact bytes built, not a later read racing a save), the
// copied files, the importer of each file (for the chain a per-kind refusal names) and the API versions imported.
// esbuild runs its callbacks on several goroutines; mu guards what they record.
type importRules struct {
	plugin api.Plugin
	own    *model.Plugin
	kind   string
	// entry is the facet file's real path.
	entry string
	// modules is the plugin's own packages' directory: every package file inlined lies in it.
	modules string
	// others are the other directory plugins: a single-file plugin's files are no one's to import.
	others       []model.Plugin
	options      model.BuildOptions
	hostPackages map[string]bool
	ctx          context.Context
	// manifests is what a directory of a package's `package.json` is, by directory.
	manifests memo[manifestKind]
	// listed is the plugin folders a `chord` package lists, by its directory.
	listed memo[listedPlugins]

	mu         sync.Mutex
	readInputs map[string]string
	readCopied map[string]bool
	// scoped is the scoped stylesheet of every CSS module read, by its file.
	scoped map[string]string
	// mappings reads the mapping of each CSS module (the caller's Node subprocess).
	mappings model.MappingEvaluator
	// aborted is set when ctx ended a read of a mapping: the build is aborted, not failed.
	aborted atomic.Bool
	// importers is the first importer of every file resolved, by file.
	importers map[string]string
	// versions is the API versions imported, in the order they were first imported.
	versions []importedVersion
}

func newImportRules(own model.Plugin, kind, entry string, options model.BuildOptions, ctx context.Context) *importRules {
	rules := &importRules{
		own:          &own,
		kind:         kind,
		entry:        entry,
		modules:      filepath.Join(own.Dir, "node_modules"),
		options:      options,
		hostPackages: map[string]bool{},
		ctx:          ctx,
		readInputs:   map[string]string{},
		readCopied:   map[string]bool{},
		scoped:       map[string]string{},
		mappings:     options.Mappings,
		importers:    map[string]string{},
	}
	for _, each := range options.Plugins {
		if each.Dir != own.Dir && each.Layout == model.LayoutDirectory {
			rules.others = append(rules.others, each)
		}
	}
	for _, specifier := range options.HostPackages {
		rules.hostPackages[specifier] = true
	}
	rules.plugin = api.Plugin{Name: "chord-plugin-imports", Setup: rules.setup}
	return rules
}

// noteImporter records importer as the first importer of path, when it has none.
func (rules *importRules) noteImporter(path, importer string) {
	rules.mu.Lock()
	defer rules.mu.Unlock()
	if _, has := rules.importers[path]; !has {
		rules.importers[path] = importer
	}
}

func (rules *importRules) setup(builder api.PluginBuild) {
	builder.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
		if path, scoped := strings.CutPrefix(args.Path, scopedCSSNamespace+":"); scoped {
			return api.OnResolveResult{Path: path, Namespace: scopedCSSNamespace}, nil
		}
		// The plugin's files are resolved here, the entry too: esbuild then gives them no Node module type
		// (`"type": "module"`), whose interop would read a whole host package for a default import instead of honouring
		// the `__esModule` an ESM namespace is wrapped with (§3).
		if args.Kind == api.ResolveEntryPoint {
			return api.OnResolveResult{Path: rules.entry}, nil
		}
		if args.PluginData == any(resolving{}) {
			return api.OnResolveResult{}, nil
		}
		resolved := rules.resolve(args.Path, args.Importer, args.Kind)
		switch resolved.kind {
		case resolvedError:
			return api.OnResolveResult{Errors: []api.Message{{Text: resolved.text}}}, nil
		case resolvedExternal:
			return api.OnResolveResult{Path: args.Path, External: true}, nil
		}
		target := args.Path
		if resolved.kind == resolvedPath {
			target = resolved.path
		}
		found := builder.Resolve(target, api.ResolveOptions{
			Kind:       args.Kind,
			Importer:   args.Importer,
			ResolveDir: args.ResolveDir,
			PluginData: resolving{},
		})
		if len(found.Errors) > 0 {
			return api.OnResolveResult{Errors: found.Errors}, nil
		}
		sideEffects := api.SideEffectsTrue
		if !found.SideEffects {
			sideEffects = api.SideEffectsFalse
		}
		// A module a package's `browser` field disables: as esbuild resolved it.
		if found.Namespace != "file" {
			return api.OnResolveResult{Path: found.Path, External: found.External, SideEffects: sideEffects, Namespace: found.Namespace, Suffix: found.Suffix}, nil
		}
		// esbuild resolves links: `found.Path` is the real file.
		if resolved.kind == resolvedPackage {
			if rules.place(found.Path).kind != placePackage {
				return api.OnResolveResult{Errors: []api.Message{{Text: `"` + args.Path + `": resolves outside the plugin's node_modules (` + rules.shown(found.Path) + `)`}}}, nil
			}
			// A package that is plugin code (a `chord` field: a plugin, or a repository of several) keeps the per-kind
			// rule; an ordinary library names its files as it likes (`pdf.worker.mjs`).
			code, err := rules.pluginCode(found.Path)
			if err != nil {
				return api.OnResolveResult{}, err
			}
			if code != nil {
				rules.noteImporter(found.Path, args.Importer)
				if refused := rules.checkKind(found.Path, args.Path, code.top); refused != "" {
					return api.OnResolveResult{Errors: []api.Message{{Text: refused}}}, nil
				}
			}
			return api.OnResolveResult{Path: found.Path, SideEffects: sideEffects, Suffix: found.Suffix}, nil
		}
		if found.Path != rules.entry {
			rules.noteImporter(found.Path, args.Importer)
		}
		if refused := rules.checkKind(found.Path, args.Path, ""); refused != "" {
			return api.OnResolveResult{Errors: []api.Message{{Text: refused}}}, nil
		}
		if rules.options.Platform == model.PlatformNode && browserOnly[model.Extname(found.Path)] {
			return api.OnResolveResult{Errors: []api.Message{{Text: `"` + args.Path + `": stylesheets and assets are for browser builds`}}}, nil
		}
		if checked := rules.checkFile(found.Path, args.Importer, args.Path); checked != "" {
			return api.OnResolveResult{Errors: []api.Message{{Text: checked}}}, nil
		}
		result := api.OnResolveResult{Path: found.Path}
		if resolved.asset {
			result.PluginData = assetData{}
		}
		return result, nil
	})
	builder.OnLoad(api.OnLoadOptions{Filter: ".*"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
		if args.Namespace == scopedCSSNamespace {
			// Its `url()` and `@import` resolve relative to the module file.
			rules.mu.Lock()
			css := rules.scoped[args.Path]
			rules.mu.Unlock()
			return api.OnLoadResult{Contents: &css, Loader: api.LoaderCSS, ResolveDir: filepath.Dir(args.Path)}, nil
		}
		if args.Namespace != "file" {
			return api.OnLoadResult{}, nil
		}
		loader, known := loaders[model.Extname(args.Path)]
		if args.PluginData == any(assetData{}) {
			loader, known = api.LoaderFile, true
		}
		if !known || rules.place(args.Path).kind == placePackage {
			return api.OnLoadResult{}, nil
		}
		contents, err := os.ReadFile(args.Path)
		if err != nil {
			// pig divergence (D222): esbuild's Go API reports this error at the import that names the input; Pi's JavaScript API
			// reports it at its own library's line (`lib/main.js:1552:22`), which PiG has no stack to produce.
			return api.OnLoadResult{Errors: []api.Message{{Text: nodeReadError(err, args.Path)}}}, nil
		}
		sum := sha256.Sum256(contents)
		rules.mu.Lock()
		rules.readInputs[args.Path] = hex.EncodeToString(sum[:])
		rules.mu.Unlock()
		if args.PluginData != any(assetData{}) && strings.HasSuffix(args.Path, ".module.css") {
			// build.ts: `scopeCssModule(…, new TextDecoder().decode(contents))`.
			return rules.cssModule(args.Path, DecodeUTF8(contents))
		}
		text := string(contents)
		return api.OnLoadResult{Contents: &text, Loader: loader, ResolveDir: filepath.Dir(args.Path)}, nil
	})
}

// cssModule is the CSS module file with contents as the facet imports it: a module whose default export is the class
// mapping, importing the scoped stylesheet (ScopeCSSModule), which the build collects with the facet's other CSS.
func (rules *importRules) cssModule(file, contents string) (api.OnLoadResult, error) {
	scoped, err := ScopeCSSModule(rules.own.Name, rules.own.Dir, file, contents, rules.mappings, rules.ctx)
	if err != nil {
		if failure, ok := errors.AsType[*cssModuleError](err); ok {
			return api.OnLoadResult{Errors: failure.messages}, nil
		}
		if rules.ctx.Err() != nil {
			rules.aborted.Store(true)
		}
		return api.OnLoadResult{}, err
	}
	rules.mu.Lock()
	rules.scoped[file] = scoped.CSS
	rules.mu.Unlock()
	module := "import " + jsString(scopedCSSNamespace+":"+file) + ";export default " + scoped.Classes + ";"
	return api.OnLoadResult{Contents: &module, Loader: api.LoaderJS, ResolveDir: filepath.Dir(file)}, nil
}

// nodeReadError is the text of the error Node's `readFile(path)` throws, which Pi's build reports for an input it cannot read
// (`ENOENT: no such file or directory, open '<path>'`, `EACCES: permission denied, open '<path>'`); Go's `open <path>: ...`
// otherwise. (esbuild's JavaScript API puts the location of its own library in front of a thrown plugin error, which is no
// part of the text: the Go API puts the position of the import in front of it.)
func nodeReadError(err error, path string) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT: no such file or directory, open '" + path + "'"
	case errors.Is(err, fs.ErrPermission):
		return "EACCES: permission denied, open '" + path + "'"
	}
	return err.Error()
}

// inputs is every plugin file read, sorted by path.
func (rules *importRules) inputs() []model.BuildInput {
	rules.mu.Lock()
	defer rules.mu.Unlock()
	inputs := make([]model.BuildInput, 0, len(rules.readInputs))
	for file, hash := range rules.readInputs {
		inputs = append(inputs, model.BuildInput{File: file, Hash: hash})
	}
	slices.SortFunc(inputs, func(a, b model.BuildInput) int { return model.CompareUTF16(a.File, b.File) })
	return inputs
}

// copied is the other plugins' files inlined, sorted.
func (rules *importRules) copied() []string {
	rules.mu.Lock()
	defer rules.mu.Unlock()
	copied := make([]string, 0, len(rules.readCopied))
	for file := range rules.readCopied {
		copied = append(copied, file)
	}
	slices.SortFunc(copied, model.CompareUTF16)
	return copied
}

// version is the API version the script imports, if it imports one (one version: `mixedVersions` refuses two); empty
// when it imports none.
func (rules *importRules) version() string {
	rules.mu.Lock()
	defer rules.mu.Unlock()
	if len(rules.versions) == 0 {
		return ""
	}
	return rules.versions[0].version
}

// checkEntry is why the facet file named (its real path entry) may not be built: a link out of the plugin (a single-file
// plugin's out of its root's top), or to a file of another kind; "" when it may.
func (rules *importRules) checkEntry(named, entry string) string {
	var own bool
	if rules.own.Layout == model.LayoutFile {
		own = filepath.Dir(entry) == rules.own.Dir
	} else {
		own = model.Inside(rules.own.Dir, entry)
	}
	if !own {
		return filepath.Base(named) + ": the facet file links out of its plugin (" + entry + ")"
	}
	fileKind, ok := rules.kindOf(entry, "")
	if !ok || sameKind(fileKind, rules.kind) {
		return ""
	}
	return filepath.Base(named) + ": links to a " + fileKind + " file (" + rules.shown(entry) + "), not a " + rules.kind + " facet"
}

// mixedVersions is the error of a facet importing two API versions (both named with an importer), or "".
func (rules *importRules) mixedVersions() string {
	rules.mu.Lock()
	defer rules.mu.Unlock()
	if len(rules.versions) < 2 {
		return ""
	}
	name := rules.options.Api.Name
	sorted := slices.Clone(rules.versions)
	slices.SortFunc(sorted, func(a, b importedVersion) int { return model.CompareUTF16(a.version, b.version) })
	seen := make([]string, len(sorted))
	for i, each := range sorted {
		seen[i] = name + "/" + each.version + " (" + rules.shown(each.importer) + ")"
	}
	return "one facet imports one version of " + name + ": this one imports " + strings.Join(seen, " and ")
}

type resolutionKind uint8

const (
	resolvedExternal resolutionKind = iota
	resolvedPackage
	resolvedPath
	resolvedError
)

// resolution is what an import becomes (see importRules.resolve): external, refused, a package, or a plugin file.
type resolution struct {
	kind  resolutionKind
	path  string
	asset bool
	text  string
}

// resolve is what an import of specifier from importer becomes: external, refused, a package esbuild resolves (one of the
// plugin's `dependencies`, or anything a package imports) or a path esbuild resolves and the rules check.
func (rules *importRules) resolve(specifier, importer string, kind api.ResolveKind) resolution {
	browser := rules.options.Platform == model.PlatformBrowser
	failure := func(text string) resolution { return resolution{kind: resolvedError, text: text} }
	if kind == api.ResolveJSDynamicImport {
		return failure(`import("` + specifier + `"): a plugin script has no dynamic import (` + pluginFileHint + `)`)
	}
	if specifier == pluginsSpecifier {
		return resolution{kind: resolvedExternal}
	}
	if imported, ok := rules.apiImport(specifier, importer); ok {
		return imported
	}
	if rules.hostPackages[specifier] {
		return resolution{kind: resolvedExternal}
	}
	if rules.own.Layout == model.LayoutFile {
		directory := rules.own.Name + "/"
		return failure(`"` + specifier + `": a single-file plugin imports only host packages (the app's API); make it a directory "` + directory + `" with a package.json`)
	}
	from := rules.place(importer)
	// A copied file (another plugin's) imports host packages and its own plugin's files only: no builtins either.
	if from.kind == placeCopied && !strings.HasPrefix(specifier, ".") && !filepath.IsAbs(specifier) {
		return failure(`"` + specifier + `": ` + copiedRule(*from.plugin))
	}
	if isBuiltin(specifier) {
		if !browser {
			return resolution{kind: resolvedExternal}
		}
		return failure(`"` + specifier + `": a Node builtin in a browser build`)
	}
	if path, ok := strings.CutSuffix(specifier, "?url"); ok {
		if browser {
			return resolution{kind: resolvedPath, path: path, asset: true}
		}
		return failure(`"` + specifier + `": assets by URL are for browser builds (` + pluginFileHint + `)`)
	}
	if from.kind == placePackage {
		return resolution{kind: resolvedPackage}
	}
	// A stylesheet's `url()` names an asset of the plugin (served under `publicPath`).
	asset := kind == api.ResolveCSSURLToken && browser
	if strings.HasPrefix(specifier, ".") || filepath.IsAbs(specifier) {
		return resolution{kind: resolvedPath, path: specifier, asset: asset}
	}
	name := packageName(specifier)
	if slices.Contains(rules.own.External, name) {
		if !browser {
			return resolution{kind: resolvedExternal}
		}
		return failure(`"` + specifier + `": chord.external packages are for Node builds`)
	}
	if slices.Contains(rules.own.Dependencies, name) {
		return resolution{kind: resolvedPackage}
	}
	return failure(`"` + specifier + `": neither a host package nor in the dependencies of plugin "` + rules.own.Name + `"`)
}

// newestVersion is the app's newest API version as upstream interpolates `api.versions[0]`: `undefined` for an app that
// lists no version.
func newestVersion(app *model.AppApi) string {
	if len(app.Versions) == 0 {
		return "undefined"
	}
	return app.Versions[0]
}

// apiImport is an import of the app's API or of Chord when the app has an API (slice 12 §1): a listed version the kind
// provides is external (its version recorded); an unlisted version, a module the kind lacks and Chord itself are refused.
// It reports false for any other specifier.
func (rules *importRules) apiImport(specifier, importer string) (resolution, bool) {
	app := rules.options.Api
	if app == nil {
		return resolution{}, false
	}
	failure := func(text string) (resolution, bool) { return resolution{kind: resolvedError, text: text}, true }
	newest := app.Name + "/" + newestVersion(app)
	if packageName(specifier) == chordPackage {
		return failure(`"` + specifier + `": plugins import Chord through the app's API ("` + newest + `"), never directly`)
	}
	if specifier != app.Name && !strings.HasPrefix(specifier, app.Name+"/") {
		return resolution{}, false
	}
	provided := "this app provides " + strings.Join(app.Versions, ", ")
	version, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(specifier, app.Name), "/"), "/")
	if version == "" {
		return failure(`"` + specifier + `": import a version of the app's API (` + provided + `)`)
	}
	if !slices.Contains(app.Versions, version) {
		return failure(`"` + specifier + `": ` + app.Name + "/" + version + " is not available: " + provided)
	}
	if !rules.hostPackages[specifier] {
		return failure(`"` + specifier + `": not available to ` + rules.kind + ` facets`)
	}
	rules.mu.Lock()
	if !slices.ContainsFunc(rules.versions, func(each importedVersion) bool { return each.version == version }) {
		rules.versions = append(rules.versions, importedVersion{version: version, importer: importer})
	}
	rules.mu.Unlock()
	return resolution{kind: resolvedExternal}, true
}

// checkKind is why the plugin file path (not a package's: npm packages name files as they like, `pdf.worker.mjs`) may not
// be in a facet of this kind: a per-kind file (`contract.web.tsx`) or a facet file (`web.tsx`) of another kind, named with
// the chain of imports that reached it; "" when it may. packageTop is the directory of the plugin package path is in, or "".
func (rules *importRules) checkKind(path, specifier, packageTop string) string {
	fileKind, ok := rules.kindOf(path, packageTop)
	if !ok || sameKind(fileKind, rules.kind) {
		return ""
	}
	chain := rules.chainOf(path)
	for i, file := range chain {
		chain[i] = rules.shown(file)
	}
	return `"` + specifier + `": a ` + fileKind + ` file, imported into a ` + rules.kind + ` facet (` + strings.Join(chain, " → ") + `)`
}

// kindOf is the kind of the plugin file path, one of the app's kinds (compared without case): a name
// `<name>.<kind>.<ext>`, or a facet file `<kind>.<ext>` at the top of a directory plugin (or of the plugin package
// packageTop, when it is not empty). It reports false for a file of no kind.
func (rules *importRules) kindOf(path, packageTop string) (string, bool) {
	parts := strings.Split(filepath.Base(path), ".")
	if len(parts) >= 3 {
		return rules.kindNamed(parts[len(parts)-2])
	}
	dir := filepath.Dir(path)
	top := packageTop != "" && dir == packageTop
	if !top {
		for _, each := range append([]model.Plugin{*rules.own}, rules.others...) {
			if each.Layout == model.LayoutDirectory && dir == each.Dir {
				top = true
				break
			}
		}
	}
	if len(parts) == 2 && top {
		return rules.kindNamed(parts[0])
	}
	return "", false
}

// kindNamed is the app's kind that name is, compared without case.
func (rules *importRules) kindNamed(name string) (string, bool) {
	for _, kind := range rules.options.Kinds {
		if sameKind(kind, name) {
			return kind, true
		}
	}
	return "", false
}

// chainOf is the files from the entry to file, by their first importers; an import cycle ends the walk.
func (rules *importRules) chainOf(file string) []string {
	rules.mu.Lock()
	defer rules.mu.Unlock()
	chain := []string{file}
	seen := map[string]bool{file: true}
	for at, ok := rules.importers[file]; ok && !seen[at]; at, ok = rules.importers[at] {
		seen[at] = true
		chain = slices.Insert(chain, 0, at)
	}
	return chain
}

// shown is file as an error names it: relative to the plugin's directory.
func (rules *importRules) shown(file string) string {
	rest, err := model.Relative(rules.own.Dir, file)
	if err != nil {
		rest = file
	}
	return filepath.ToSlash(rest)
}

// checkFile is why the plugin file path (resolved from importer's import of specifier) may not be inlined: outside the
// plugin's root, in no plugin of it, or, from a copied file, outside that file's own plugin; "" when it may (another
// plugin's file of the root is recorded as copied).
func (rules *importRules) checkFile(path, importer, specifier string) string {
	from := rules.place(importer)
	to := rules.place(path)
	if to.kind == placeOwn && from.kind == placeOwn {
		return ""
	}
	if to.kind == placeCopied && (from.kind == placeOwn || (from.kind == placeCopied && from.plugin == to.plugin)) {
		rules.mu.Lock()
		rules.readCopied[path] = true
		rules.mu.Unlock()
		return ""
	}
	if from.kind == placeCopied {
		return `"` + specifier + `": ` + copiedRule(*from.plugin)
	}
	if to.kind == placeOutside && model.Inside(rules.own.Root, path) {
		return `"` + specifier + `": not a file of a plugin in this root`
	}
	base := "a host package"
	if app := rules.options.Api; app != nil {
		base = `the app's API ("` + app.Name + "/" + newestVersion(app) + `")`
	}
	return `"` + specifier + `": outside the plugin's root (base code is reached through ` + base + `, other repositories' plugins through dependencies)`
}

// pluginCodeOf is what is known of a package file that is plugin code.
type pluginCodeOf struct {
	// top is the file's own directory when that is the package's (a facet file there is of its kind), else empty.
	top string
}

// pluginCode is whether the package file path is plugin code: its nearest `package.json` (walking up to the plugin's
// `node_modules`) has a `chord` field, or is a plugin folder the next `package.json` above lists in its `chord.plugins` (a
// plugin of a repository of several, reached as a dependency); a library (even one nested in a plugin package) is not. It
// returns nil for a library.
func (rules *importRules) pluginCode(path string) (*pluginCodeOf, error) {
	var nearest string
	for dir := filepath.Dir(path); model.Inside(rules.modules, dir); dir = filepath.Dir(dir) {
		manifest := rules.manifestIn(dir)
		if manifest == manifestNone {
			continue
		}
		if nearest == "" && manifest == manifestChord {
			if dir == filepath.Dir(path) {
				return &pluginCodeOf{top: dir}, nil
			}
			return &pluginCodeOf{}, nil
		}
		if nearest == "" {
			nearest = dir
			continue
		}
		listed := rules.listedIn(dir)
		if listed.err != nil {
			return nil, listed.err
		}
		if manifest == manifestPlain || !listed.dirs[nearest] {
			return nil, nil
		}
		if nearest == filepath.Dir(path) {
			return &pluginCodeOf{top: nearest}, nil
		}
		return &pluginCodeOf{}, nil
	}
	return nil, nil
}

// listedIn is the plugin folders the package in dir lists in `chord.plugins` (discovery's reading of it; once per build).
func (rules *importRules) listedIn(dir string) listedPlugins {
	return rules.listed.get(dir, func() listedPlugins {
		at := model.RootContext{Root: dir, Required: false, Kinds: rules.options.Kinds}
		discovery, err := model.DiscoverPackage(dir, at, rules.ctx)
		if err != nil {
			return listedPlugins{err: err}
		}
		dirs := map[string]bool{}
		for _, each := range discovery.Plugins {
			dirs[each.Dir] = true
		}
		return listedPlugins{dirs: dirs}
	})
}

// manifestIn is what dir's `package.json` is: absent, without a `chord` field, or with one (read once per build).
func (rules *importRules) manifestIn(dir string) manifestKind {
	return rules.manifests.get(dir, func() manifestKind {
		text, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil {
			return manifestNone
		}
		// A trust boundary: a package's manifest, read only for its `chord` field.
		var manifest map[string]json.RawMessage
		if json.Unmarshal(text, &manifest) != nil {
			return manifestPlain
		}
		if _, has := manifest["chord"]; has {
			return manifestChord
		}
		return manifestPlain
	})
}

// place is where path (a real path) lies; a package is one of the plugin's own `node_modules`, any other is outside.
func (rules *importRules) place(path string) place {
	if model.Inside(rules.modules, path) {
		return place{kind: placePackage}
	}
	if model.Inside(rules.own.Dir, path) {
		return place{kind: placeOwn}
	}
	// Another plugin's own packages are no one's to import; a root may itself lie under a `node_modules` (an app installed
	// from npm with its plugins), so only the part below a plugin's directory counts.
	for i := range rules.others {
		other := &rules.others[i]
		if model.Inside(other.Dir, path) && !model.Inside(filepath.Join(other.Dir, "node_modules"), path) {
			if other.Root == rules.own.Root {
				return place{kind: placeCopied, plugin: other}
			}
			return place{kind: placeOtherRoot, plugin: other}
		}
	}
	return place{kind: placeOutside}
}

// sameKind is whether two kind names are the same kind: compared without case (`contract.Web.ts` is a web file).
func sameKind(a, b string) bool {
	return strings.EqualFold(a, b)
}

// copiedRule is the rule a copied file broke.
func copiedRule(plugin model.Plugin) string {
	return `a file copied from plugin "` + plugin.Name + `" may import only host packages and files of its own plugin`
}

// describe is one build error as `file:line:column: text`, its file relative to the plugin, with the hint where it
// applies.
func describe(plugin model.Plugin, message api.Message) string {
	text := message.Text
	if message.ID == "empty-import-meta" {
		text = noModuleIdentity
	} else if strings.HasPrefix(text, "Top-level await") {
		text = "a plugin script has no top-level await (a facet declares synchronously)"
	}
	location := message.Location
	if location == nil {
		return text
	}
	file := location.File
	if filepath.IsAbs(file) {
		if rest, err := model.Relative(plugin.Dir, file); err == nil {
			file = rest
		}
	}
	// The column is esbuild's own, in bytes: its JavaScript API reports the same number (`location.column`, build.ts:399).
	return file + ":" + strconv.Itoa(location.Line) + ":" + strconv.Itoa(location.Column+1) + ": " + text
}

// packageName is the package a bare specifier names: `@scope/name` or `name`, without a subpath (evaluate.ts).
func packageName(specifier string) string {
	parts := strings.Split(specifier, "/")
	if strings.HasPrefix(specifier, "@") {
		return strings.Join(parts[:min(2, len(parts))], "/")
	}
	return parts[0]
}

// builtinModules are Node's builtin modules (`module.builtinModules` of Node 22), importable with or without `node:`.
var builtinModules = map[string]bool{}

// nodePrefixOnly are the builtin modules importable only as `node:<name>`.
var nodePrefixOnly = map[string]bool{"sea": true, "sqlite": true, "test": true, "test/reporters": true}

func init() {
	for name := range strings.FieldsSeq(`_http_agent _http_client _http_common _http_incoming _http_outgoing _http_server
		_stream_duplex _stream_passthrough _stream_readable _stream_transform _stream_wrap _stream_writable _tls_common
		_tls_wrap assert assert/strict async_hooks buffer child_process cluster console constants crypto dgram
		diagnostics_channel dns dns/promises domain events fs fs/promises http http2 https inspector inspector/promises
		module net os path path/posix path/win32 perf_hooks process punycode querystring readline readline/promises repl
		stream stream/consumers stream/promises stream/web string_decoder sys timers timers/promises tls trace_events tty
		url util util/types v8 vm wasi worker_threads zlib`) {
		builtinModules[name] = true
	}
}

// isBuiltin is Node's `module.isBuiltin(specifier)`.
func isBuiltin(specifier string) bool {
	if name, ok := strings.CutPrefix(specifier, "node:"); ok {
		return builtinModules[name] || nodePrefixOnly[name]
	}
	return builtinModules[specifier]
}
