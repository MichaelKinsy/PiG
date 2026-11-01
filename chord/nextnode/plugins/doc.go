// Package plugins ports packages/chord/src/next-node/plugins: the plugins component of Chord v2 (slice 7c, design §21
// "Loading plugins and the reload loop"), the plugin directories found, each facet built, evaluated, loaded and reloaded
// per plugin, watched and rolled out. Upstream builds a facet into a script with esbuild and evaluates it on Node's `vm`;
// Go runs neither (AGENTS.md: no embedded JavaScript runtime), so the build and the evaluation are two seams the consumer
// owns: a Builder turns a discovered plugin's facet file of a kind into the plain data the component and the watcher
// consume (BuiltFacet: a script with its digest, the files it read and the files it copied, or its errors), and an
// Evaluator turns a built script into a host.Facet. Discovery, installation, the watcher with its marks and the plugin
// and facet identities (the digest, ScriptFilename, PluginFile) are the port.
//
// The files (plugins_<stem>.go) mirror upstream's: discover.ts, install.ts and watcher.ts are ported; build.ts keeps
// BuiltFacet, BuildFacet and what the watcher uses of it; evaluate.ts keeps the filename and `pluginFile` rules; stack.ts
// (source maps of evaluated JavaScript) is designed out with the evaluation it serves. Nothing in the package waits on
// the loop: the watcher's methods run in the host's sections, and the file events of its watches enter the loop through
// the batch's section (state.Batch.UseSection) as a timer or a link read does.
package plugins
