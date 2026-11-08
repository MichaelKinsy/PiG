package hosts

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/src/hosts/plugins.ts under D473: p2's plugins are compiled-in Go packages registered at start, not
// directories built on their own at run time. A plugin package registers its facets of kind `server`, `worker` and `tui` in an
// init function (RegisterPlugin); the p2 command links the plugin packages it ships, a test links the ones it needs. The
// server loads the selected plugins' server facets, the TUI host their tui facets (TUIFacets), and each session worker (the same executable) the worker facets of
// the selection the server sends it (the `P2_PLUGINS` environment variable). Web facets stay in p2's own
// bundles (bundles.go).

// Plugin is a compiled-in plugin: its name (the directory name upstream) and its facets; nil for a kind it has none of.
type Plugin struct {
	Name   string
	Server *host.Facet
	Worker *host.Facet
	// TUI is the plugin's terminal-client facet (`tui.ts` upstream): the slots, commands and renderers the TUI host loads
	// into its own Chord host (TUIFacets). The server and the session workers never load it.
	TUI *host.Facet
}

var (
	pluginsMu  sync.Mutex
	registered = map[string]Plugin{}
)

// RegisterPlugin registers plugin; it panics for a name registered already (a program error: two packages claim a name).
func RegisterPlugin(plugin Plugin) {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	if _, ok := registered[plugin.Name]; ok {
		panic(fmt.Sprintf("p2 plugin %q is registered twice", plugin.Name))
	}
	registered[plugin.Name] = plugin
}

// ListPlugins are the registered plugins, alphabetical.
func ListPlugins() []Plugin {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	plugins := make([]Plugin, 0, len(registered))
	for _, plugin := range registered {
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	return plugins
}

// SelectPlugins are exactly the requested plugins (nil: all), alphabetical. A missing provider fails at activation, by
// name.
func SelectPlugins(requested []string) ([]Plugin, error) {
	listing := ListPlugins()
	if requested == nil {
		return listing, nil
	}
	for _, name := range requested {
		if !slices.ContainsFunc(listing, func(plugin Plugin) bool { return plugin.Name == name }) {
			return nil, fmt.Errorf("Unknown p2 plugin: %s", name)
		}
	}
	return slices.DeleteFunc(listing, func(plugin Plugin) bool { return !slices.Contains(requested, plugin.Name) }), nil
}

// PluginsEnv is the environment variable that tells a worker which plugins to load (comma-separated names).
const PluginsEnv = "P2_PLUGINS"

// serverFacets are the selection's server facets as Chord loads them.
func serverFacets(selection []Plugin) []host.HostFacet {
	var facets []host.HostFacet
	for _, plugin := range selection {
		if plugin.Server != nil {
			facets = append(facets, host.HostFacet{Name: plugin.Name, Facet: *plugin.Server})
		}
	}
	return facets
}

// TUIFacets are the selection's TUI facets as the TUI host loads them into its Chord host.
func TUIFacets(selection []Plugin) []host.HostFacet {
	var facets []host.HostFacet
	for _, plugin := range selection {
		if plugin.TUI != nil {
			facets = append(facets, host.HostFacet{Name: plugin.Name, Facet: *plugin.TUI})
		}
	}
	return facets
}

// workerFacets are the selection's worker facets as Chord loads them.
func workerFacets(selection []Plugin) []host.HostFacet {
	var facets []host.HostFacet
	for _, plugin := range selection {
		if plugin.Worker != nil {
			facets = append(facets, host.HostFacet{Name: plugin.Name, Facet: *plugin.Worker})
		}
	}
	return facets
}

func pluginNames(selection []Plugin) string {
	names := make([]string, len(selection))
	for i, plugin := range selection {
		names[i] = plugin.Name
	}
	return strings.Join(names, ",")
}
