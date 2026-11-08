package hosts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports the reload of apps/p2/src/hosts/server.ts (`performReload`) under D473. Pi builds the named plugins' sources
// and rolls the build out to the server host and every running worker; the code of a Go plugin is compiled in, so a
// reload re-evaluates its facets: each named plugin's facets are Renewed (a new facet of the same setup, which
// `Host.Reload` counts as changed, as it counts a module evaluated again), the server host reloads with them, and the
// workers get a script set whose digests carry the plugin's reload counter, so a second reload of the same code ships as
// Pi's second build of a changed file does. A worker applies the set through `WorkerMainOptions.Reload`
// (worker_reload.go) with the same Renew on its worker facets.

// ReloadResult is what a reload did (Pi's RolloutResult without `stopped`: a stop is the error of Reload).
type ReloadResult struct {
	// Server is what the server host's reload applied.
	Server host.ReloadReport
	// Workers is one entry per worker the set was shipped to.
	Workers []plugins.RolloutHost
}

// reloader is the server's reload state: the facets it loaded and each plugin's reload counter.
type reloader struct {
	mu         sync.Mutex
	running    bool
	facets     []host.HostFacet
	selection  []Plugin
	generation map[string]int
}

// scriptsOf is the complete set the workers receive: a script per selected plugin with a worker facet, its digest
// `<name>#<reloads>`.
func scriptsOf(selection []Plugin, generation map[string]int) []plugins.PluginScript {
	var scripts []plugins.PluginScript
	for _, plugin := range selection {
		if plugin.Worker == nil {
			continue
		}
		scripts = append(scripts, plugins.PluginScript{
			Plugin: plugins.Plugin{Name: plugin.Name},
			Built: plugins.BuiltFacet{
				Kind: plugins.BuiltFacetScript, Plugin: plugin.Name, Facet: "worker", Digest: digestOf(plugin.Name, generation[plugin.Name]),
			},
		})
	}
	return scripts
}

func digestOf(name string, reloads int) string { return fmt.Sprintf("%s#%d", name, reloads) }

// Reload re-evaluates the named plugins (an empty list: all selected) on the server and on every running worker. A
// plugin whose new facet fails keeps its old code (Report.Failed); an error is a reload that did not run or stopped at the
// server.
func (s *P2Server) Reload(names []string, ctx context.Context) (ReloadResult, error) {
	r := &s.reloads
	for _, name := range names {
		if !slices.Contains(s.Plugins, name) {
			if slices.ContainsFunc(ListPlugins(), func(plugin Plugin) bool { return plugin.Name == name }) {
				return ReloadResult{}, fmt.Errorf("Cannot reload unloaded p2 plugin: %s", name)
			}
			return ReloadResult{}, fmt.Errorf("Unknown p2 plugin: %s", name)
		}
	}
	if len(names) == 0 {
		names = s.Plugins
	}
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ReloadResult{}, errors.New("A p2 plugin reload is already running")
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	next := slices.Clone(r.facets)
	for index, entry := range next {
		plugin, ok := pluginNamed(r.selection, entry.Name)
		if ok && plugin.Server != nil && slices.Contains(names, entry.Name) {
			next[index].Facet = entry.Facet.Renew()
		}
	}
	generation := make(map[string]int, len(r.generation))
	for name, reloads := range r.generation {
		generation[name] = reloads
	}
	for _, name := range names {
		generation[name]++
	}
	report, err := s.Host.Reload(next, ctx, host.ReloadOptions{})
	if err != nil {
		return ReloadResult{}, err
	}
	failed := make(map[string]bool, len(report.Failed))
	for _, each := range report.Failed {
		failed[each.Plugin] = true
	}
	// What the server host kept is what its reload committed: a failed plugin keeps its old facet.
	for index, entry := range next {
		if failed[entry.Name] {
			next[index] = r.facets[index]
		}
	}
	for name := range failed {
		generation[name] = r.generation[name]
	}
	r.mu.Lock()
	r.facets, r.generation = next, generation
	r.mu.Unlock()
	hosts, err := s.workers.Workers().Rollout(scriptsOf(r.selection, generation), ctx)
	if err != nil {
		return ReloadResult{Server: report}, err
	}
	return ReloadResult{Server: report, Workers: hosts}, nil
}

func pluginNamed(selection []Plugin, name string) (Plugin, bool) {
	for _, plugin := range selection {
		if plugin.Name == name {
			return plugin, true
		}
	}
	return Plugin{}, false
}
