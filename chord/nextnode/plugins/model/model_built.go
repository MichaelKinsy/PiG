package model

import "context"

// AppApi is the app's API (slice 12 §1): the package plugins import it as and the versions it provides, newest first
// (`{ name: "@acme/app", versions: ["v3", "v2"] }`). Its modules are host packages (`@acme/app/v3`, `@acme/app/v3/web`).
type AppApi struct {
	Name     string
	Versions []string
}

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
	// HostPackages are the host packages' specifiers (`@acme/app/v3`, `@acme/app/v3/web`, `react`, `react/jsx-runtime`):
	// external.
	HostPackages []string
	// Platform is where the script runs.
	Platform Platform
	// Api is the app's API: its imports checked by version and kind, Chord refused. Without one (nil), host packages as
	// listed.
	Api *AppApi
	// Kinds are the app's facet kinds: a file named `<name>.<kind>.<ext>` is one of that kind, refused in a facet of another.
	Kinds []string
	// Plugins are the discovered plugins: a file of a directory plugin of the same root may be imported by relative path
	// (a copy).
	Plugins []Plugin
	// PublicPath is where the app serves a browser build's assets: the URL prefix their imports evaluate to (nil: `/`).
	PublicPath *string
	// Mappings reads the mapping of each CSS module the build scopes (the Node subprocess is the caller's: one for as many builds
	// as it likes, ended by it). Nil: a CSS module fails the build.
	Mappings MappingEvaluator
}

// MappingEvaluator evaluates the mapping module esbuild makes of a CSS module (CommonJS) and answers its mapping as JSON text
// (`JSON.stringify` of the spread of the module's default export). PiG's process has no JavaScript engine, so the Node
// subprocess that does it belongs to the plugins component, not to the builder.
type MappingEvaluator interface {
	Evaluate(ctx context.Context, js string) (string, error)
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

// BuiltApi is the app's API a build checked against and the version the script imports (when it imports one): evaluation
// refuses other versions and Chord to a computed `require` as the build did to imports.
type BuiltApi struct {
	Name string
	// Version is the version the script imports; empty is upstream's `undefined` (it imports none).
	Version string
}

// BuiltFacet is a built facet: its script and what it used, or the build's errors. Plain data. Plugin and Facet (the kind)
// are set for both kinds; Source, Digest, Inputs, Copied, CSS, Assets and Api for BuiltFacetScript, Errors for
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
	// Api is the app's API the build checked against, when the build had one (nil otherwise).
	Api *BuiltApi
	// Errors is why the build failed, each naming its file and line where there is one.
	Errors []string
}
