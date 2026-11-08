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

// Ports the reload of apps/p2/src/hosts/server.ts (`performReload`) under D473. Pi builds the named plugins' sources and
// rolls the build out to the server host and every running worker only for the plugins whose build digest changed: a
// reload of unchanged code is a no-op (nothing reaches the server host or the workers). The code of a Go plugin is
// compiled in, so its digest is `<name>#<revision>` where the revision is what `ServerOptions.BuildRevision` reports for
// the plugin (empty: the build the server and workers started with). A plugin whose revision differs from the one last
// rolled out has its facets Renewed (a new facet of the same setup, which `Host.Reload` counts as changed, as it counts a
// module evaluated again), the server host reloads with them, and the workers get a script set carrying the new digests.
// A worker applies the set through `WorkerMainOptions.Reload` (worker_reload.go) with the same Renew on its worker facets.

// ReloadResult is what a reload did (Pi's RolloutResult without `stopped`: a stop is the error of Reload).
type ReloadResult struct {
	// Server is what the server host's reload applied.
	Server host.ReloadReport
	// Workers is one entry per worker the set was shipped to.
	Workers []plugins.RolloutHost
}

// reloader is the server's reload state: the facets it loaded and the revision of each plugin last rolled out.
type reloader struct {
	mu        sync.Mutex
	running   bool
	facets    []host.HostFacet
	selection []Plugin
	revision  map[string]string
	build     func(plugin string) string
}

// scriptsOf is the complete set the workers receive: a script per selected plugin with a worker facet, its digest
// `<name>#<revision>`.
func scriptsOf(selection []Plugin, revision map[string]string) []plugins.PluginScript {
	var scripts []plugins.PluginScript
	for _, plugin := range selection {
		if plugin.Worker == nil {
			continue
		}
		scripts = append(scripts, plugins.PluginScript{
			Plugin: plugins.Plugin{Name: plugin.Name},
			Built: plugins.BuiltFacet{
				Kind: plugins.BuiltFacetScript, Plugin: plugin.Name, Facet: "worker", Digest: digestOf(plugin.Name, revision[plugin.Name]),
			},
		})
	}
	return scripts
}

func digestOf(name, revision string) string { return name + "#" + revision }

// Reload rolls out the named plugins (an empty list: all selected) whose build revision changed to the server and to
// every running worker; when none changed it does nothing (an empty result). A
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

	revision := make(map[string]string, len(r.revision))
	for name, rev := range r.revision {
		revision[name] = rev
	}
	changed := map[string]bool{}
	for _, name := range names {
		if rev := r.build(name); rev != revision[name] {
			revision[name] = rev
			changed[name] = true
		}
	}
	if len(changed) == 0 {
		return ReloadResult{}, nil
	}
	next := slices.Clone(r.facets)
	for index, entry := range next {
		plugin, ok := pluginNamed(r.selection, entry.Name)
		if ok && plugin.Server != nil && changed[entry.Name] {
			next[index].Facet = entry.Facet.Renew()
		}
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
		revision[name] = r.revision[name]
	}
	r.mu.Lock()
	r.facets, r.revision = next, revision
	r.mu.Unlock()
	hosts, err := s.workers.Workers().Rollout(scriptsOf(r.selection, revision), ctx)
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
