package plugins

import "fmt"

// Ports `pluginFile` of packages/chord/src/node/plugins/evaluate.ts: inside a facet script the evaluation hands each facet its own
// (the absolute path of a file of the facet's plugin, `..` out of its directory refused), answered in the Node subprocess where Pi's
// evaluation runs; imported anywhere else it throws. The Go function is that anywhere else: a Go program is no facet script.

// PluginFile is `pluginFile(path)` outside a facet script evaluated by the plugins component: it has no file to give, and returns
// the error Pi throws.
func PluginFile(path string) (string, error) {
	return "", fmt.Errorf(`pluginFile("%s"): only a plugin's facet script, evaluated by the plugins component, has one`, path)
}
