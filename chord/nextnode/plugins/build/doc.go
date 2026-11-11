// Package build is the plugin builder: Pi's packages/chord/src/node/plugins/build.ts, css-modules.ts and base-inputs.ts on
// esbuild's Go API. BuildFacet builds one facet of a plugin into a script (plain data, model.BuiltFacet); Recorder records what a
// build of a base read. It is the only package of the component that links esbuild, and the server's, not a worker's: the
// component takes BuildFacet as PluginsOptions.Build and a worker never builds.
package build
