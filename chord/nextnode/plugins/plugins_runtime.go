package plugins

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/next-node/plugins/runtime.ts. The plugin runtime (slice 10 §3, D11): loading and reloading hosts
// with shipped scripts, without building (no Builder, no watcher), so a worker evaluates what the server built. Per host it
// keeps the facet loaded for every plugin and passes the host its complete set at each reload: unchanged plugins (the same
// digest) as the same facet values, removed ones (loaded, not shipped) left out; a plugin whose build or evaluation failed
// keeps its loaded facet (left out at startup) and is reported with the host's report. The plugins component uses one.
// Upstream turns V8's compilation cache off here (`setFlagsFromString`): there is none in Go, so nothing is ported of it.

// PluginScript is one plugin's facet of a kind as shipped to a host (plain data): its build and its plugin record.
type PluginScript struct {
	Plugin Plugin
	Built  BuiltFacet
}

// RuntimeLoad is what PluginRuntime.Load takes besides the host.
type RuntimeLoad struct {
	Kind string
	// Core are the app's own facets.
	Core []host.HostFacet
	// Scripts is the complete set of Kind.
	Scripts []PluginScript
	// Modules are the host packages the scripts' imports are answered with: this process's instances, kept for the host's
	// reloads.
	Modules HostPackages
}

// loadedFacet is a plugin's facet as a host loaded it, with its script's digest.
type loadedFacet struct {
	facet  host.Facet
	digest string
}

// hostPlugins is a host's plugins as the runtime loaded them: its core facets, its modules, each plugin's loaded facet, and
// its reloads, one after another (each computes its complete set once the previous one adopted its result).
type hostPlugins struct {
	core    []host.HostFacet
	modules HostPackages
	// loaded is read and replaced only by the reload whose turn it is.
	loaded map[string]loadedFacet
	// tail is closed when the latest queued reload ended; guarded by PluginRuntime.mu.
	tail chan struct{}
}

// preparedFacet is a plugin's facet passed to a host, or why it was not: a build or evaluation failure.
type preparedFacet struct {
	loadedFacet
	err error
}

// PluginRuntime loads and reloads hosts with complete sets of shipped scripts. Its Evaluator turns a script into a facet.
type PluginRuntime struct {
	evaluator Evaluator
	mu        sync.Mutex
	// hosts are the loaded hosts' plugins, until Forget (upstream holds them as long as the host lives).
	hosts map[host.Host]*hostPlugins
}

// NewPluginRuntime is upstream's `createPluginRuntime()`, with the evaluator of the app.
func NewPluginRuntime(evaluator Evaluator) *PluginRuntime {
	return &PluginRuntime{evaluator: evaluator, hosts: map[host.Host]*hostPlugins{}}
}

// Load evaluates options.Scripts (the complete set of options.Kind) with options.Modules and loads them on h with
// options.Core: a plugin whose build or evaluation failed is left out and reported with the host's report (the failures of
// the scripts first). It returns the host's error as it is, after which nothing is kept for h.
func (r *PluginRuntime) Load(h host.Host, options RuntimeLoad, ctx context.Context) (host.LoadReport, error) {
	type named struct {
		plugin   string
		prepared preparedFacet
	}
	prepared := make([]named, len(options.Scripts))
	for i, script := range options.Scripts {
		prepared[i] = named{script.Plugin.Name, r.prepare(script.Plugin, script.Built, options.Modules)}
	}
	failed := []host.PluginFailure{}
	facets := append([]host.HostFacet{}, options.Core...)
	for _, each := range prepared {
		if each.prepared.err != nil {
			failed = append(failed, host.PluginFailure{Plugin: each.plugin, Error: each.prepared.err})
		} else {
			facets = append(facets, host.HostFacet{Name: each.plugin, Facet: each.prepared.facet})
		}
	}
	report, err := h.Load(facets, ctx)
	if err != nil {
		return host.LoadReport{}, err
	}
	left := map[string]struct{}{}
	for _, failure := range report.Failed {
		left[failure.Plugin] = struct{}{}
	}
	loaded := map[string]loadedFacet{}
	for _, each := range prepared {
		if _, out := left[each.plugin]; each.prepared.err == nil && !out {
			loaded[each.plugin] = each.prepared.loadedFacet
		}
	}
	// The host's own facets, for its later reloads.
	r.mu.Lock()
	r.hosts[h] = &hostPlugins{core: append([]host.HostFacet{}, options.Core...), modules: options.Modules, loaded: loaded}
	r.mu.Unlock()
	return host.LoadReport{Failed: append(failed, report.Failed...)}, nil
}

// reloadTurn is a place in a host's queue of reloads: it runs once the reload queued before it ended.
type reloadTurn struct {
	state *hostPlugins
	prev  chan struct{}
	done  chan struct{}
}

// queue puts a reload of h at the end of its queue, in the order of the calls (upstream's promise chain): the component
// calls it in the step that decides the set, run then reloads. It fails when h was not loaded by this runtime.
func (r *PluginRuntime) queue(h host.Host) (*reloadTurn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.hosts[h]
	if !ok {
		return nil, errors.New("plugins.reload: the host was not loaded by this runtime")
	}
	turn := &reloadTurn{state: state, prev: state.tail, done: make(chan struct{})}
	state.tail = turn.done
	return turn, nil
}

// Reload reloads h (loaded by Load) with scripts, the complete set now, after its earlier reloads: its core facets and
// unchanged plugins (the same digest) as the same values, new scripts evaluated, a plugin whose build or evaluation failed
// passed as its loaded facet (an added one left out; both given to the host's closure rule), loaded plugins not in scripts
// left out. What the host committed is the loaded set, also when the reload returns `aborted` after its commit (its
// details.committed). It blocks until the report.
func (r *PluginRuntime) Reload(h host.Host, scripts []PluginScript, ctx context.Context) (host.ReloadReport, error) {
	turn, err := r.queue(h)
	if err != nil {
		return host.ReloadReport{}, err
	}
	return r.run(turn, h, scripts, ctx)
}

// run waits for turn's place and does its reload: one after another per host, as a reload computing its set from a loaded
// map an earlier one is about to change would pass that plugin's old facet and roll it back.
func (r *PluginRuntime) run(turn *reloadTurn, h host.Host, scripts []PluginScript, ctx context.Context) (host.ReloadReport, error) {
	if turn.prev != nil {
		<-turn.prev
	}
	defer close(turn.done)
	return r.reload(h, turn.state, scripts, ctx)
}

// reload is one reload of h with the complete set scripts.
func (r *PluginRuntime) reload(h host.Host, state *hostPlugins, scripts []PluginScript, ctx context.Context) (host.ReloadReport, error) {
	failed := []host.PluginFailure{}
	next := newLoadedSet()
	for _, script := range scripts {
		name := script.Plugin.Name
		loaded, isLoaded := state.loaded[name]
		if script.Built.Kind == BuiltFacetScript && isLoaded && script.Built.Digest == loaded.digest {
			next.set(name, loaded)
			continue
		}
		prepared := r.prepare(script.Plugin, script.Built, state.modules)
		if prepared.err == nil {
			next.set(name, prepared.loadedFacet)
			continue
		}
		failed = append(failed, host.PluginFailure{Plugin: name, Error: prepared.err})
		if isLoaded {
			next.set(name, loaded)
		}
	}
	facets := append([]host.HostFacet{}, state.core...)
	for _, name := range next.order {
		facets = append(facets, host.HostFacet{Name: name, Facet: next.items[name].facet})
	}
	// The plugins whose build or evaluation failed are the host's to report, and its closure rule's to consider.
	report, err := h.Reload(facets, ctx, host.ReloadOptions{Failed: failed})
	if err != nil {
		// Aborted after its commit: the commit stands, and is what the host runs now.
		if committed, ok := committedOf(err); ok {
			adopt(state, next, committed.Failed)
		}
		return host.ReloadReport{}, err
	}
	names := make([]string, len(report.Failed))
	for i, failure := range report.Failed {
		names[i] = failure.Plugin
	}
	adopt(state, next, names)
	return report, nil
}

// Forget drops what the runtime keeps of h: Go has no weak table keyed by an interface, so a host that is closed is
// forgotten by its owner. A later Reload of h fails as for a host never loaded.
func (r *PluginRuntime) Forget(h host.Host) {
	r.mu.Lock()
	delete(r.hosts, h)
	r.mu.Unlock()
}

// prepare is built evaluated for plugin with modules, or why not: its build failed, or its evaluation failed (an error, or a
// panic of the evaluator, which is a plugin's code throwing).
func (r *PluginRuntime) prepare(plugin Plugin, built BuiltFacet, modules HostPackages) preparedFacet {
	if built.Kind == BuiltFacetFailed {
		message := `plugin "` + plugin.Name + `": its ` + built.Facet + ` facet failed to build:` + "\n" + strings.Join(built.Errors, "\n")
		return preparedFacet{err: errors.New(message)}
	}
	var facet host.Facet
	err := try(func() (err error) {
		facet, err = r.evaluator.EvaluateFacet(built, EvaluateOptions{HostPackages: modules, Plugin: plugin})
		return err
	})
	if err != nil {
		return preparedFacet{err: err}
	}
	return preparedFacet{loadedFacet: loadedFacet{facet: facet, digest: built.Digest}}
}

// try runs work, returning its error, or a panic as an error (a throw of plugin code).
func try(work func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if asError, ok := recovered.(error); ok {
				err = asError
			} else {
				err = fmt.Errorf("%v", recovered)
			}
		}
	}()
	return work()
}

// loadedSet is Pi's `Map<string, LoadedFacet>`: insertion order kept.
type loadedSet struct {
	order []string
	items map[string]loadedFacet
}

func newLoadedSet() *loadedSet { return &loadedSet{items: map[string]loadedFacet{}} }

func (s *loadedSet) set(name string, facet loadedFacet) {
	if _, ok := s.items[name]; !ok {
		s.order = append(s.order, name)
	}
	s.items[name] = facet
}

func (s *loadedSet) delete(name string) {
	if _, ok := s.items[name]; !ok {
		return
	}
	delete(s.items, name)
	for i, each := range s.order {
		if each == name {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// adopt adopts as state's loaded facets the set next a reload committed: a plugin the host kept old (failed) stays loaded as
// it was, an added one it left out is not loaded.
func adopt(state *hostPlugins, next *loadedSet, failed []string) {
	for _, plugin := range failed {
		if loaded, ok := state.loaded[plugin]; ok {
			next.set(plugin, loaded)
		} else {
			next.delete(plugin)
		}
	}
	state.loaded = make(map[string]loadedFacet, len(next.items))
	maps.Copy(state.loaded, next.items)
}

// committedOf is what the commit of a reload that returned an error applied: an `aborted` error after its commit says
// (`details.committed`, the object of CommittedReload's lists).
func committedOf(err error) (host.CommittedReload, bool) {
	chordErr, ok := data.IsChordErrorCode(err, data.CodeAborted)
	if !ok || !chordErr.HasDetails {
		return host.CommittedReload{}, false
	}
	details, ok := chordErr.Details.(*delta.JsonObject)
	if !ok {
		return host.CommittedReload{}, false
	}
	committed, ok := details.Value("committed").(*delta.JsonObject)
	if !ok {
		return host.CommittedReload{}, false
	}
	names := func(key string) []string {
		out := []string{}
		items, _ := committed.Value(key).([]any)
		for _, item := range items {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return host.CommittedReload{Added: names("added"), Changed: names("changed"), Removed: names("removed"), Failed: names("failed")}, true
}
