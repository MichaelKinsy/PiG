// Package codingtools is p2's `coding-tools` plugin as the web UI sees it: Pi's web facet (web/upstream/plugins/coding-tools/web.tsx:
// the labels and cards of the built-in tools' calls, "Ran pwd", "edited a file") is part of the embedded bundle. The tools
// themselves are the harness's built-ins and need no server or worker facet here, so the plugin is registered without any, as
// web-shell is, to make plugin selection and the bundle agree on it.
package codingtools

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	hosts.RegisterPlugin(hosts.Plugin{Name: "coding-tools"})
}
