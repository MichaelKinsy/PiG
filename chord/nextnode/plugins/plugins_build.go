package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/evanw/esbuild/pkg/api"
)

// Ports packages/chord/src/next-node/plugins/build.ts (`buildFacet`, `ImportRules`, `freeGlobals`, `describe`) on esbuild's
// Go API (`github.com/evanw/esbuild/pkg/api`, Pi's pin 0.28.2). Building (slice 7c §2): one facet of one plugin as one
// script, by esbuild from cold. Its own files, files of other discovered plugins imported by name (`@app/notes/contract`:
// copies) and its npm `dependencies` are inlined; host packages, this component, `chord.external` packages and Node
// builtins stay external, answered at evaluation by the `require` the script takes. The result is plain data; a failed
// build is a result, never an error.

// Platform is where a built script runs: Node (a server, a worker, a TUI) or a browser (a page).
type Platform string

const (
	// PlatformNode is a build for Node.
	PlatformNode Platform = "node"
	// PlatformBrowser is a build for a browser.
	PlatformBrowser Platform = "browser"
)

// BuildOptions is what a build takes besides the plugin and the kind.
type BuildOptions struct {
	// HostPackages are the host packages' specifiers (`@earendil-works/chord/next`, `react`, `react/jsx-runtime`,
	// `@app/core`): external.
	HostPackages []string
	// Platform is where the script runs.
	Platform Platform
	// Plugins are the discovered plugins: a file of one of them may be imported (a copy).
	Plugins []Plugin
	// PublicPath is where the app serves a browser build's assets: the URL prefix their imports evaluate to (nil: `/`).
	PublicPath *string
}

// BuildInput is a file a build read, with the hash of the bytes it handed its tool (the watcher's inputs).
type BuildInput struct {
	File string
	Hash string
}

// CSS is a browser build's stylesheet.
type CSS struct {
	Name string
	Text string
}

// Asset is a browser build's asset, by content-hashed file name.
type Asset struct {
	Name  string
	Bytes []byte
}

// BuiltKind tags a BuiltFacet.
type BuiltKind string

const (
	// BuiltFacetScript is a build that made a script.
	BuiltFacetScript BuiltKind = "script"
	// BuiltFacetFailed is a build that failed, with its errors.
	BuiltFacetFailed BuiltKind = "failed"
)

// BuiltFacet is a built facet: its script and what it used, or the build's errors. Plain data. Plugin and Facet (the kind)
// are set for both kinds; Source, Digest, Inputs, Copied, CSS and Assets for BuiltFacetScript, Errors for
// BuiltFacetFailed.
type BuiltFacet struct {
	Kind   BuiltKind
	Plugin string
	Facet  string
	// Source is the script: the builder's output, whatever its Evaluator takes.
	Source string
	// Digest is a hash of Source: the same on every machine and checkout for the same files.
	Digest string
	// Inputs is every plugin file it read (its own and copied ones; not packages), absolute paths, with their hashes.
	Inputs []BuildInput
	// Copied is the files of other plugins inlined into it, absolute paths.
	Copied []string
	// CSS is a browser build's stylesheet, when it imports CSS.
	CSS *CSS
	// Assets is a browser build's assets, by content-hashed file name.
	Assets []Asset
	// Errors is why the build failed, each naming its file and line where there is one.
	Errors []string
}

// pluginsSpecifier is the one specifier of this component a script imports (`pluginFile`): external always, answered per
// facet (evaluate.ts's PLUGINS_SPECIFIER).
const pluginsSpecifier = "@earendil-works/chord/next/plugins"

// globalMarkers are what a free `__dirname` and `__filename` become in a build: names no script has, found again by
// `freeGlobals`.
var globalMarkers = map[string]string{"__dirname": "__chord_plugin_dirname", "__filename": "__chord_plugin_filename"}

// BuildFacet builds plugin's facet of kind (one esbuild build from cold): a script wrapped as `(function (require,
// module, exports) { … })`, or the errors that refuse it (an import the rules of §2 do not admit, a syntax error,
// `import.meta`, `__dirname`, `__filename`, a literal dynamic `import("x")`, a top-level `await`), each naming its file and
// line. A computed `import(x)` becomes a call of the script's `require`. It returns an error only when ctx is aborted
// before the build (its cause, as `throwIfAborted`), or when the script's own source map cannot be read.
func BuildFacet(plugin Plugin, kind string, options BuildOptions, ctx context.Context) (BuiltFacet, error) {
	failed := func(errors []string) BuiltFacet {
		return BuiltFacet{Kind: BuiltFacetFailed, Plugin: plugin.Name, Facet: kind, Errors: errors}
	}
	entry, ok := plugin.Facets[kind]
	if !ok {
		return failed([]string{`plugin "` + plugin.Name + `" has no ` + kind + ` facet`}), nil
	}
	if ctx.Err() != nil {
		return BuiltFacet{}, context.Cause(ctx)
	}
	rules := newImportRules(plugin, options)
	browser := options.Platform == PlatformBrowser
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
		return BuiltFacet{}, err
	}
	if len(globals) > 0 {
		return failed(globals), nil
	}
	digest := sha256.Sum256([]byte(source))
	built := BuiltFacet{
		Kind:   BuiltFacetScript,
		Plugin: plugin.Name,
		Facet:  kind,
		Source: source,
		Digest: hex.EncodeToString(digest[:]),
		Inputs: rules.inputs(),
		Copied: rules.copied(),
		Assets: []Asset{},
	}
	if css != nil {
		built.CSS = &CSS{Name: filepath.Base(css.Path), Text: string(css.Contents)}
	}
	for i := range result.OutputFiles {
		file := &result.OutputFiles[i]
		if file == script || file == css {
			continue
		}
		name, err := relative(plugin.Dir, file.Path)
		if err != nil {
			return BuiltFacet{}, err
		}
		built.Assets = append(built.Assets, Asset{Name: name, Bytes: file.Contents})
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

// Where a resolved file lies, from the plugin being built: its own source, another plugin's (a copy), a package.
type placeKind uint8

const (
	placeOwn placeKind = iota
	placeCopied
	placePackage
	placeOutside
)

type place struct {
	kind placeKind
	// plugin is the other plugin of a placeCopied (identity: the same pointer is the same plugin).
	plugin *Plugin
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

// importRules is the import rules of one build (§2's table) as an esbuild plugin, and what the build read: every file of a
// plugin hashed as it is handed to esbuild (the exact bytes built, not a later read racing a save), and the copied files.
// esbuild runs its callbacks on several goroutines; mu guards what they record.
type importRules struct {
	plugin       api.Plugin
	own          *Plugin
	others       []Plugin
	options      BuildOptions
	hostPackages map[string]bool

	mu         sync.Mutex
	readInputs map[string]string
	readCopied map[string]bool
}

func newImportRules(own Plugin, options BuildOptions) *importRules {
	rules := &importRules{
		own:          &own,
		options:      options,
		hostPackages: map[string]bool{},
		readInputs:   map[string]string{},
		readCopied:   map[string]bool{},
	}
	for _, each := range options.Plugins {
		if each.Dir != own.Dir {
			rules.others = append(rules.others, each)
		}
	}
	for _, specifier := range options.HostPackages {
		rules.hostPackages[specifier] = true
	}
	rules.plugin = api.Plugin{Name: "chord-plugin-imports", Setup: rules.setup}
	return rules
}

func (rules *importRules) setup(builder api.PluginBuild) {
	builder.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
		// The plugin's files are resolved here, the entry too: esbuild then gives them no Node module type
		// (`"type": "module"`), whose interop would read a whole host package for a default import instead of honouring
		// the `__esModule` an ESM namespace is wrapped with (§3).
		if args.Kind == api.ResolveEntryPoint {
			return api.OnResolveResult{Path: resolvePath(rules.own.Dir, args.Path)}, nil
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
		case resolvedDefault:
			return api.OnResolveResult{}, nil
		}
		found := builder.Resolve(resolved.path, api.ResolveOptions{
			Kind:       args.Kind,
			Importer:   args.Importer,
			ResolveDir: args.ResolveDir,
			PluginData: resolving{},
		})
		if len(found.Errors) > 0 {
			return api.OnResolveResult{Errors: found.Errors}, nil
		}
		if rules.options.Platform == PlatformNode && browserOnly[extname(found.Path)] {
			return api.OnResolveResult{Errors: []api.Message{{Text: `"` + args.Path + `": stylesheets and assets are for browser builds`}}}, nil
		}
		if checked := rules.checkFile(found.Path, args.Importer, args.Path, resolved.named); checked != "" {
			return api.OnResolveResult{Errors: []api.Message{{Text: checked}}}, nil
		}
		result := api.OnResolveResult{Path: found.Path}
		if resolved.asset {
			result.PluginData = assetData{}
		}
		return result, nil
	})
	builder.OnLoad(api.OnLoadOptions{Filter: ".*"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
		if args.Namespace != "file" {
			return api.OnLoadResult{}, nil
		}
		loader, known := loaders[extname(args.Path)]
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
		text := string(contents)
		return api.OnLoadResult{Contents: &text, Loader: loader, ResolveDir: filepath.Dir(args.Path)}, nil
	})
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
func (rules *importRules) inputs() []BuildInput {
	rules.mu.Lock()
	defer rules.mu.Unlock()
	inputs := make([]BuildInput, 0, len(rules.readInputs))
	for file, hash := range rules.readInputs {
		inputs = append(inputs, BuildInput{File: file, Hash: hash})
	}
	slices.SortFunc(inputs, func(a, b BuildInput) int { return compareUTF16(a.File, b.File) })
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
	slices.SortFunc(copied, compareUTF16)
	return copied
}

type resolutionKind uint8

const (
	resolvedExternal resolutionKind = iota
	resolvedDefault
	resolvedPath
	resolvedError
)

// resolution is what an import becomes (see importRules.resolve).
type resolution struct {
	kind  resolutionKind
	path  string
	asset bool
	// named is the plugin an import by name named (`@app/notes/contract`).
	named *Plugin
	text  string
}

// resolve is what an import of specifier from importer becomes: external, refused, esbuild's own resolution (a package of
// the plugin's `dependencies`, or anything a package imports), or a path esbuild resolves and the rules check.
func (rules *importRules) resolve(specifier, importer string, kind api.ResolveKind) resolution {
	browser := rules.options.Platform == PlatformBrowser
	failure := func(text string) resolution { return resolution{kind: resolvedError, text: text} }
	if kind == api.ResolveJSDynamicImport {
		return failure(`import("` + specifier + `"): a plugin script has no dynamic import (` + pluginFileHint + `)`)
	}
	if specifier == pluginsSpecifier || rules.hostPackages[specifier] {
		return resolution{kind: resolvedExternal}
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
	from := rules.place(importer)
	if from.kind == placePackage {
		return resolution{kind: resolvedDefault}
	}
	if strings.HasPrefix(specifier, ".") || filepath.IsAbs(specifier) {
		return resolution{kind: resolvedPath, path: specifier}
	}
	name := packageName(specifier)
	var named *Plugin
	if name == rules.own.Name {
		named = rules.own
	} else {
		for i := range rules.others {
			if rules.others[i].Name == name {
				named = &rules.others[i]
				break
			}
		}
	}
	if named != nil {
		return rules.byName(specifier, named, from)
	}
	if from.kind == placeCopied {
		return failure(`"` + specifier + `": ` + copiedRule(*from.plugin))
	}
	if slices.Contains(rules.own.External, name) {
		if !browser {
			return resolution{kind: resolvedExternal}
		}
		return failure(`"` + specifier + `": chord.external packages are for Node builds`)
	}
	if slices.Contains(rules.own.Dependencies, name) {
		return resolution{kind: resolvedDefault}
	}
	return failure(`"` + specifier + `": neither a host package, in the dependencies of plugin "` + rules.own.Name + `" nor a discovered plugin "` + name + `"`)
}

// byName is an import by name of a file of plugin named (`@app/notes/contract`, decision "Plugins import each other by
// name"): its path in that plugin's directory, wherever that is; from a copied file only its own plugin's.
func (rules *importRules) byName(specifier string, named *Plugin, from place) resolution {
	file := ""
	if len(specifier) > len(named.Name) {
		file = specifier[len(named.Name)+1:]
	}
	if file == "" {
		return resolution{kind: resolvedError, text: `"` + specifier + `": names plugin "` + named.Name + `", not a file of it ("` + named.Name + `/contract")`}
	}
	if from.kind == placeCopied && from.plugin != named {
		return resolution{kind: resolvedError, text: `"` + specifier + `": ` + copiedRule(*from.plugin)}
	}
	return resolution{kind: resolvedPath, path: filepath.Join(named.Dir, file), named: named}
}

// checkFile is why the file path (resolved from importer's import of specifier, by name of plugin named or a relative
// path) may not be inlined: a relative path into another plugin's directory, outside every discovered plugin, or, from a
// copied file, outside that file's own plugin; "" when it may (another plugin's file is recorded as copied).
func (rules *importRules) checkFile(path, importer, specifier string, named *Plugin) string {
	from := rules.place(importer)
	to := rules.place(path)
	if to.kind == placeOwn && from.kind == placeOwn {
		return ""
	}
	if to.kind == placeCopied && (to.plugin == named || (from.kind == placeCopied && from.plugin == to.plugin)) {
		rules.mu.Lock()
		rules.readCopied[path] = true
		rules.mu.Unlock()
		return ""
	}
	if from.kind == placeCopied {
		return `"` + specifier + `": ` + copiedRule(*from.plugin)
	}
	if to.kind == placeCopied {
		return `"` + specifier + `": a relative path into plugin "` + to.plugin.Name + `" (import its files by name: "` + to.plugin.Name + `/…")`
	}
	return `"` + specifier + `": outside the plugin (base code is reached through a host package, packages through dependencies)`
}

// place is where path lies.
func (rules *importRules) place(path string) place {
	if slices.Contains(strings.Split(path, string(filepath.Separator)), "node_modules") {
		return place{kind: placePackage}
	}
	if inside(rules.own.Dir, path) {
		return place{kind: placeOwn}
	}
	for i := range rules.others {
		if inside(rules.others[i].Dir, path) {
			return place{kind: placeCopied, plugin: &rules.others[i]}
		}
	}
	return place{kind: placeOutside}
}

// copiedRule is the rule a copied file broke.
func copiedRule(plugin Plugin) string {
	return `a file copied from plugin "` + plugin.Name + `" may import only host packages and files of its own plugin`
}

// describe is one build error as `file:line:column: text`, its file relative to the plugin, with the hint where it
// applies.
func describe(plugin Plugin, message api.Message) string {
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
		if rest, err := relative(plugin.Dir, file); err == nil {
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

// resolvePath is Node's `path.resolve(dir, path)` for an absolute dir.
func resolvePath(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, path)
}

// extname is Node's `path.extname`: the last `.` of the base name on, empty when it is the name's first character.
func extname(path string) string {
	base := filepath.Base(path)
	i := strings.LastIndex(base, ".")
	if i <= 0 {
		return ""
	}
	return base[i:]
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

// inside reports whether path lies inside directory dir (both absolute): not dir itself. Upstream's test is `relative(dir,
// path)` not empty, not starting with `..` (a file named `..x` is outside) and not absolute.
func inside(dir, path string) bool {
	rest, err := relative(dir, path)
	return err == nil && rest != "" && !hasDotDotPrefix(rest) && !filepath.IsAbs(rest)
}

// relative is Node's `path.relative(from, to)`: both resolved against the working directory first.
func relative(from, to string) (string, error) {
	absoluteFrom, err := filepath.Abs(from)
	if err != nil {
		return "", err
	}
	absoluteTo, err := filepath.Abs(to)
	if err != nil {
		return "", err
	}
	rest, err := filepath.Rel(absoluteFrom, absoluteTo)
	if rest == "." {
		// Node's is empty for the same path.
		rest = ""
	}
	return rest, err
}

func hasDotDotPrefix(path string) bool { return strings.HasPrefix(path, "..") }
