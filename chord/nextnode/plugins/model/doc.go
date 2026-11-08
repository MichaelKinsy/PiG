// Package model is the data of the plugins component and what reading it needs, with no builder and no JavaScript runtime: a
// plugin as discovered (Plugin, DiscoverPlugins, DiscoverPackage), a facet as built (BuiltFacet and what it holds, BuildOptions),
// what a base build read (BaseInputs, ContentHash, HashFile), and the path rules both discovery and the builder keep (Inside,
// Relative, ResolvePath, Posix).
//
// It is the lowest of three layers. plugins/build (esbuild: BuildFacet, the CSS module scoping, Recorder) and plugins (the
// component, the plugin runtime and the Node host) both import it; the runtime package does not import the builder, which the
// server hands the component (PluginsOptions.Build), so a worker, which evaluates what the server built, links no esbuild
// (workers_child_test.go: Pi's child.test.ts "loads no esbuild").
package model
