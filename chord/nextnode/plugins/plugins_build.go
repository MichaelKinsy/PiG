package plugins

import (
	"context"
	"path/filepath"
	"strings"
)

// Ports packages/chord/src/next-node/plugins/build.ts (BuildOptions, BuildInput, BuiltFacet, buildFacet's entry and
// `inside`). Building (slice 7c §2): one facet of one plugin as one script. Upstream builds it with esbuild from cold; Go
// runs no JavaScript tool in process (AGENTS.md), so the build is the consumer-owned Builder seam. What the component and
// the watcher consume is upstream's plain data, BuiltFacet: its digest is the facet's identity (the same on every machine
// and checkout for the same files), its inputs are the files the watcher dirties its plugin for, a failed build is a
// result and never an error.

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

// Builder builds a discovered plugin's facet. It is upstream's esbuild build, owned by the consumer.
type Builder interface {
	// Build builds plugin's facet of kind, whose file plugin.Facets[kind] exists: a script, or the errors that refuse it (an
	// import the rules of §2 do not admit, a syntax error, `import.meta`, `__dirname`, `__filename`, a literal dynamic
	// import, a top-level `await`), each naming its file and line, as a BuiltFacetFailed result. It returns an error only
	// when ctx aborts: a failed build is a result, never an error.
	Build(plugin Plugin, kind string, options BuildOptions, ctx context.Context) (BuiltFacet, error)
}

// BuildFacet builds plugin's facet of kind with builder: a plugin with no facet of kind is a failed result naming it,
// without a build; an aborted ctx is its reason (context.Cause), as `throwIfAborted` throws it.
func BuildFacet(builder Builder, plugin Plugin, kind string, options BuildOptions, ctx context.Context) (BuiltFacet, error) {
	if _, ok := plugin.Facets[kind]; !ok {
		return BuiltFacet{Kind: BuiltFacetFailed, Plugin: plugin.Name, Facet: kind, Errors: []string{`plugin "` + plugin.Name + `" has no ` + kind + ` facet`}}, nil
	}
	if err := ctx.Err(); err != nil {
		return BuiltFacet{}, context.Cause(ctx)
	}
	return builder.Build(plugin, kind, options, ctx)
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
	return filepath.Rel(absoluteFrom, absoluteTo)
}

func hasDotDotPrefix(path string) bool { return strings.HasPrefix(path, "..") }
