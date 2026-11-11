package web

// ViewKit is the view kit's script (plugins/views/kit/main.ts of the vendored sources, compiled by build/build.mjs as Pi's
// `buildBrowserScript` compiles it): the one script a custom view's frame loads beside its bridge, which adds rooms and methods
// to `window.p2view` (apps/p2/docs/rooms.md). It is no file of the web bundle: the views plugin serves it at /view-kit/<sha256>.js.
// It is absent from a dist that has no views plugin.
func ViewKit() ([]byte, error) { return distFiles.ReadFile("dist/view-kit.js") }
