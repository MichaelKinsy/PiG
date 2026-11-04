// Package plugins ports packages/chord/src/next-node/plugins: the plugins component of Chord v2 (slice 7c, design §21
// "Loading plugins and the reload loop"), the plugin directories found, each facet built, evaluated, loaded and reloaded
// per plugin, watched and rolled out.
//
// Upstream builds a facet into a script with esbuild and evaluates it on Node's `vm` in the process of the host that loads
// it. Go builds with esbuild's own Go API (BuildFacet: a 1:1 port of build.ts), and has no JavaScript runtime in process
// (AGENTS.md), so the host that loads the plugins is Pi's own chord host in a Node subprocess, started by a PluginRuntime
// (a NodeHost) whose host is the child of a Go host over the chord link wire, as a forked worker is of its server (the app
// routes the NodeHost's kind to NodeHost.Link, or accepts it); Pi's evaluate.ts, stack.ts and runtime.ts run there,
// vendored verbatim under node/ (docs/plan/chord-plugins-node-evaluator.md).
//
// The files (plugins_<stem>.go) mirror upstream's: build.ts is plugins_build.go (and plugins_sourcemap.go, the inline
// source maps stack.ts also reads), discover.ts, install.ts and watcher.ts are ported, plugins.ts is the Plugins component,
// runtime.ts is the Go client of the subprocess (plugins_runtime.go, plugins_node.go); evaluate.ts and stack.ts are the
// subprocess's. Nothing in the package waits on the loop: the watcher's methods run in the host's sections, and the file
// events of its watches enter the loop through the batch's section (state.Batch.UseSection) as a timer or a link read does.
package plugins
