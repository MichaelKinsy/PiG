// Package plugins ports packages/chord/src/next-node/plugins: the plugins component of Chord v2 (slice 7c, design §21
// "Loading plugins and the reload loop"), the plugin directories found, each facet built, evaluated, loaded and reloaded
// per plugin, watched and rolled out.
//
// Upstream builds a facet into a script with esbuild and evaluates it on Node's `vm` in the process of the host that loads
// it. Go builds with esbuild's own Go API (build.BuildFacet: a 1:1 port of build.ts), and has no JavaScript runtime in process
// (AGENTS.md), so the host that loads the plugins is Pi's own chord host in a Node subprocess, started by a PluginRuntime
// (a NodeHost) whose host is the child of a Go host over the chord link wire, as a forked worker is of its server (the app
// routes the NodeHost's kind to NodeHost.Link, or accepts it); Pi's evaluate.ts, stack.ts and runtime.ts run there,
// vendored verbatim under node/ (docs/plan/chord-plugins-node-evaluator.md).
//
// Three packages: model holds the data and discovery (discover.ts), build the esbuild builder (build.ts, css-modules.ts,
// base-inputs.ts and the inline source maps stack.ts also reads), and this one the runtime and the component, which takes the
// builder as PluginsOptions.Build, so a worker links no esbuild. The files (plugins_<stem>.go) mirror upstream's: install.ts and
// watcher.ts are ported, plugins.ts is the Plugins component,
// runtime.ts is the Go client of the subprocess (plugins_runtime.go, plugins_node.go); evaluate.ts and stack.ts are the
// subprocess's. Nothing in the package waits on the loop: the watcher's methods run in the host's sections, and the file
// events of its watches enter the loop through the batch's section (state.Batch.UseSection) as a timer or a link read does.
//
// The plugin model (slice 12): roots are PluginRoots (a Required root's plugin failing at load, or a problem discovery
// reported in it, makes Load return a *PluginError once the host's load settled, the loaded host returned with it), the
// app's API (PluginsOptions.Api) is checked at build and, for a computed `require`, at run time in the subprocess (the
// shipped plugin record carries its layout, root and whether it is required, the shipped build its API), a single-file
// plugin (LayoutFile) is a facet file at a root's top, and InstallPlugin adds plugins to a root.
package plugins
