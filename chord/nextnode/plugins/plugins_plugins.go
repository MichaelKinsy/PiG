package plugins

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next-node/plugins/plugins.ts. The plugins component (slice 7c §4, §5; decision "Slice 7c's
// points"; slice 10 D11, D12; slice 12 §1, §5): discovery, builds checked against the app's API, installs and the watcher,
// and per kind the complete current set of scripts (built on first use, updated by every rollout that ships the kind),
// which its plugin runtime loads and reloads hosts with: unchanged plugins as the same facets, removed ones left out, a
// failed build or evaluation keeping the plugin's loaded facet. `Rollout` runs the app's steps in order (one result per
// host) and clears the marks of plugins applied on every host of every step.
//
// Execution model. The component's own state and its watcher are touched only in sections of a loop of its own (host.Loop:
// the batch of its watcher runs on it, the file events enter it), as Pi's component runs on the one event loop; what blocks
// (discovery, builds, installs, reloads of hosts) runs outside a section. Every method may be called from any goroutine;
// two Reloads of one host run one after another, in the order they decided their sets. BuildFacet is called from several
// goroutines at once (one build per facet of the plugins to build: Pi's Promise.all).

// KindDefinition is one facet kind: Node or browser builds, and the host packages (by specifier) its builds keep external.
type KindDefinition struct {
	Platform     Platform
	HostPackages []string
}

// KindEntry is one entry of upstream's `kinds` record: the kind (`server`, `worker`, `web`), the record's key, and its definition.
// A record keeps its keys in insertion order and a Go map does not, so the kinds are a slice of entries in the order the app gave them.
type KindEntry struct {
	Name       string
	Definition KindDefinition
}

// PluginsErrorKind tags a PluginsError: a failure of the batch of the marks (the kinds of state.BatchError) or of the
// watcher.
type PluginsErrorKind string

const (
	// PluginsListenerError is a watch listener of the marks that failed.
	PluginsListenerError PluginsErrorKind = PluginsErrorKind(state.BatchListenerError)
	// PluginsDeliveryFailed is a frame delivery of the marks that failed.
	PluginsDeliveryFailed PluginsErrorKind = PluginsErrorKind(state.BatchDeliveryFailed)
	// PluginsWatchFailed is the watcher that cannot watch: reported once, after which the app builds on demand.
	PluginsWatchFailed PluginsErrorKind = "watch_failed"
)

// PluginsError is what the component reports.
type PluginsError struct {
	Kind  PluginsErrorKind
	Error error
}

// PluginsOptions is what CreatePlugins takes.
type PluginsOptions struct {
	// Roots are the directories plugins are listed in, in order: the first root's plugin wins a duplicate name. A root marked
	// Required (the app's own) makes Load return a *PluginError when one of its plugins fails.
	Roots []PluginRoot
	// Api is the app's API (`{Name: "@acme/app", Versions: ["v3", "v2"]}`, newest first): plugins import it at a listed
	// version, its per-kind modules from their kind only, one version per facet, and never Chord directly. Nil: plugins
	// import the host packages as listed.
	Api *AppApi
	// Kinds are the app's facet kinds (one file per kind in a plugin), each with where its builds run and the host packages
	// they keep external, in the app's order.
	Kinds []KindEntry
	// Base is the server's own base code, files and directories, hashed when the component is created: `base` is set while one
	// differs from it or a source file is new in one of the directories (the app restarts its hosts; Chord restarts nothing).
	Base []string
	// PublicPath is where the app serves browser builds' assets: the URL prefix their imports evaluate to (nil: `/`).
	PublicPath *string
	// Install is how InstallDependencies runs npm.
	Install InstallOptions
	// OnError gets the watcher's failure to watch (reported once) and the marks' watch listeners that failed; it runs in a
	// section of the component's loop and must not block.
	OnError func(PluginsError)
	// Node is how the Node subprocesses that host the plugins are started (docs/plan/chord-plugins-node-evaluator.md).
	Node NodeOptions
}

// Built is a build of some plugins, plain data (a copy of it reloads the same): every facet of each, the plugins as its
// discovery saw them by name (what a reload evaluates with), and the plugins no longer discovered. Only the pointer Build
// returned clears marks when rolled out: a copy carries none.
type Built struct {
	Facets  []BuiltFacet
	Plugins map[string]Plugin
	Removed []string

	// self is the Built that Build made: a copy of the struct has another address.
	self *Built
	// snapshot is the marks the build started from.
	snapshot MarkSnapshot
}

// RolloutHostKind tags a RolloutHost.
type RolloutHostKind string

const (
	// RolloutReported is a host that reloaded: Report.
	RolloutReported RolloutHostKind = "report"
	// RolloutStopped is a host that stopped cleanly during the step (it gets the current set at its next start).
	RolloutStopped RolloutHostKind = "stopped"
	// RolloutErrored is a host that crashed or whose reload failed (every plugin of the step's kinds in the build counts as
	// failed there): Error.
	RolloutErrored RolloutHostKind = "error"
)

// RolloutHost is what a rollout step did on one of its hosts (Host named by the step).
type RolloutHost struct {
	Kind   RolloutHostKind
	Host   string
	Report host.ReloadReport
	// Stopped is true for a RolloutStopped host (Pi's `stopped: true`).
	Stopped bool
	Error   error
}

// StepResult is what a rollout step's reload returns: one result per host (D12).
type StepResult struct {
	Hosts []RolloutHost
}

// RolloutStep is what a rollout step does: reloads its hosts with the facets of its kinds. An error stops the rollout.
type RolloutStep struct {
	Name  string
	Kinds []string
	// Reload applies built to the step's hosts and returns once they did. applied is the current set of each of the step's
	// kinds as THIS rollout left it (Pi's step reads `plugins.current(kind)` in the synchronous part that follows its
	// `ship`, before another rollout can change it: no await lies between them there; a Go step cannot read it atomically
	// with the ship, so it is handed over).
	Reload func(built *Built, applied map[string][]PluginScript, ctx context.Context) (StepResult, error)
}

// RolloutStepResult is one step's hosts, in a RolloutResult.
type RolloutStepResult struct {
	Name  string
	Hosts []RolloutHost
}

// StoppedStep is the step a rollout stopped at (its reload returned an error), and that error.
type StoppedStep struct {
	Step  string
	Error error
}

// RolloutResult is what a rollout did: each step's hosts in order, and the step it stopped at, if it stopped.
type RolloutResult struct {
	Steps   []RolloutStepResult
	Stopped *StoppedStep
}

// PluginsLoad is what Plugins.Load takes.
type PluginsLoad struct {
	Kind string
	// Core are the app's own facets that are JavaScript (RuntimeLoad.Core); Go facets are Bridged.
	Core []CoreFacet
	// Modules are the host packages the scripts' imports are answered with, by specifier; kept for the host's reloads.
	Modules HostPackages
	// Address, Principal, Peer and Heartbeat are the Node host's address and principal, who the Go host that attaches its
	// link is, and the host's heartbeat (RuntimeLoad).
	Address   *contracts.Address
	Principal data.Principal
	Peer      data.Principal
	Heartbeat *host.HeartbeatOptions
	// CloseTimeout, ReadyTimeout and Compression are the Node host's (RuntimeLoad).
	CloseTimeout *time.Duration
	ReadyTimeout *time.Duration
	// ReloadDrainTimeout is the Node host's `reloadDrainTimeout` (RuntimeLoad).
	ReloadDrainTimeout *time.Duration
	Compression        bool
	// Store is the Node host's store (RuntimeLoad).
	Store *StoreOptions
	// GoPackages, Telemetry, PrincipalKey and RevokedBy are the app's callbacks the Node host calls (RuntimeLoad).
	GoPackages   []GoPackage
	Telemetry    telemetry.TelemetryContext
	PrincipalKey *data.PrincipalKeyFunction
	RevokedBy    host.RevokedBy[delta.JsonValue]
	// Bridged are the app's Go core facets whose local types the plugins use (RuntimeLoad.Bridged); ParentServes is the remote
	// types the Go host serves that the plugins use (RuntimeLoad.ParentServes).
	Bridged      []BridgedFacet
	ParentServes func(name string) bool
}

// CreatePlugins creates the plugins component (and its plugin runtime) and starts the watcher. Discovery runs at first use.
func CreatePlugins(options PluginsOptions, _ context.Context) *Plugins {
	c := &Plugins{
		options:   options,
		kindNames: make([]string, len(options.Kinds)),
		kinds:     make(map[string]KindDefinition, len(options.Kinds)),
		runtime:   NewPluginRuntime(options.Node),
		mappings:  newMappingEvaluator(options.Node),
		loop:      host.NewLoop(),
		built:     map[string]BuiltFacet{},
		current:   map[string]*scriptSet{},
	}
	for i, kind := range options.Kinds {
		c.kindNames[i] = kind.Name
		c.kinds[kind.Name] = kind.Definition
	}
	report := func(kind PluginsErrorKind, err error) {
		if options.OnError != nil {
			options.OnError(PluginsError{Kind: kind, Error: err})
		}
	}
	batch := state.NewBatch(func(e state.BatchError) { report(PluginsErrorKind(e.Kind), e.Error) }, nil).
		UseScheduler(chord.NewDeliveryScheduler()).UseMicrotasks(c.loop.Queue).UseSection(c.loop.Run, c.loop.InSection)
	c.watcher = newPluginWatcher(watcherOptions{
		Roots: rootPaths(options.Roots), Base: options.Base, Kinds: c.kindNames,
		Failed: func(err error) { report(PluginsWatchFailed, err) },
	}, batch)
	return c
}

// rootPaths are the paths of roots: upstream's `roots.map((root) => (typeof root === "string" ? root : root.path))`.
func rootPaths(roots []PluginRoot) []string {
	paths := make([]string, len(roots))
	for i, root := range roots {
		paths[i] = root.Path
	}
	return paths
}

// scriptSet is a kind's current set, Pi's `Map<string, PluginScript>` by plugin name: insertion order kept.
type scriptSet struct {
	order []string
	items map[string]PluginScript
}

func newScriptSet() *scriptSet { return &scriptSet{items: map[string]PluginScript{}} }

func (s *scriptSet) set(name string, script PluginScript) {
	if _, ok := s.items[name]; !ok {
		s.order = append(s.order, name)
	}
	s.items[name] = script
}

func (s *scriptSet) delete(name string) {
	if _, ok := s.items[name]; !ok {
		return
	}
	delete(s.items, name)
	s.order = slices.DeleteFunc(s.order, func(each string) bool { return each == name })
}

// values are the scripts in order, a list of its own.
func (s *scriptSet) values() []PluginScript {
	out := make([]PluginScript, len(s.order))
	for i, name := range s.order {
		out[i] = s.items[name]
	}
	return out
}

// Plugins is the plugins component.
type Plugins struct {
	options   PluginsOptions
	kindNames []string
	kinds     map[string]KindDefinition
	runtime   *PluginRuntime
	// mappings reads the mapping of every CSS module its builds scope: one Node subprocess, ended by Close.
	mappings *mappingEvaluator
	// loop is where the state below and the watcher are touched: one section at a time.
	loop    *host.Loop
	watcher *pluginWatcher
	// discovery is the last one; discovered whether there was one.
	discovery  Discovery
	discovered bool
	// built is the latest build of every (plugin, kind) built so far, by `plugin\x00kind`.
	built map[string]BuiltFacet
	// current is the current set of each kind used so far.
	current map[string]*scriptSet
}

// section runs work as a section of the component's loop.
func (c *Plugins) section(work func()) { c.loop.Run(work) }

// stateView is `plugins.state`: the watcher as a watchable of its JSON value, read in the loop.
type stateView struct{ c *Plugins }

func (v stateView) Value() delta.JsonValue {
	var value delta.JsonValue
	v.c.section(func() { value, _ = v.c.watcher.Value() })
	return value
}

func (v stateView) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	var watch chord.WatchHandle[delta.JsonValue]
	var err error
	v.c.section(func() { watch, err = v.c.watcher.Watch(ctx) })
	return watch, err
}

// State is the marks, `{ dirty, install, set, base }`, a JSON watchable.
func (c *Plugins) State() chord.Watchable[delta.JsonValue] { return stateView{c} }

// Marks is the marks now.
func (c *Plugins) Marks() PluginsState {
	var marks PluginsState
	c.section(func() { marks = c.watcher.marks() })
	return marks
}

// Discovery is the plugins of the last discovery.
func (c *Plugins) Discovery() Discovery {
	var discovery Discovery
	c.section(func() { discovery = c.discovery })
	return discovery
}

// Build builds every facet of names (and of plugins discovered since the last build; discovery runs again first when the
// plugin set changed): plain data. It changes no current set: a rollout ships it.
func (c *Plugins) Build(names []string, ctx context.Context) (*Built, error) {
	var before []string
	previous := map[string]Plugin{}
	var needs bool
	c.section(func() {
		for _, plugin := range c.discovery.Plugins {
			before = append(before, plugin.Name)
			previous[plugin.Name] = plugin
		}
		needs = !c.discovered || c.watcher.needsDiscovery()
	})
	if needs {
		if err := c.discover(ctx); err != nil {
			return nil, err
		}
	}
	var wanted []Plugin
	var plugins []Plugin
	removed := []string{}
	var snapshot MarkSnapshot
	c.section(func() {
		plugins = c.discovery.Plugins
		for _, name := range before {
			if !slices.ContainsFunc(plugins, func(plugin Plugin) bool { return plugin.Name == name }) {
				removed = append(removed, name)
			}
		}
		// A plugin whose files moved (a single file became a directory, a facet file came or went) is built again too.
		for _, plugin := range plugins {
			prior, known := previous[plugin.Name]
			if slices.Contains(names, plugin.Name) || !known || !SameFiles(prior, plugin) {
				wanted = append(wanted, plugin)
			}
		}
		asked := make([]string, 0, len(wanted)+len(removed))
		for _, plugin := range wanted {
			asked = append(asked, plugin.Name)
		}
		snapshot = c.watcher.snapshot(append(asked, removed...))
	})
	var jobs []buildJob
	for _, plugin := range wanted {
		for _, kind := range c.kindNames {
			if _, ok := plugin.Facets[kind]; ok {
				jobs = append(jobs, buildJob{plugin: plugin, kind: kind})
			}
		}
	}
	facets, err := c.buildAll(jobs, plugins, ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Plugin, len(wanted))
	for _, plugin := range wanted {
		byName[plugin.Name] = plugin
	}
	built := &Built{Facets: facets, Plugins: byName, Removed: removed, snapshot: snapshot}
	built.self = built
	return built, nil
}

// Scripts is the complete current set of kind: every discovered plugin's facet of it (built on first use), as the last
// rollout or reload shipping kind left it; what a worker starts with.
func (c *Plugins) Scripts(kind string, ctx context.Context) ([]PluginScript, error) {
	set, err := c.set(kind, ctx)
	if err != nil {
		return nil, err
	}
	var scripts []PluginScript
	c.section(func() { scripts = set.values() })
	return scripts, nil
}

// Current is kind's current set now, without blocking (false before its first use): a rollout step reads it as the rollout
// applied its build, before another rollout can change it.
func (c *Plugins) Current(kind string) ([]PluginScript, bool) {
	var scripts []PluginScript
	var ok bool
	c.section(func() {
		if set, found := c.current[kind]; found {
			scripts, ok = set.values(), true
		}
	})
	return scripts, ok
}

// Load starts a Node subprocess that hosts options.Kind's current set, evaluated with options.Modules, and options.Core
// (the app's own facets): a plugin whose build or evaluation fails is left out and reported with the host's report. The app
// attaches the returned host's Link to a Go host (PluginRuntime.Load). A required root's plugin failing, and a problem
// discovery reported in a required root (missing, an invalid package, a name another root has), return a *PluginError once
// the host's load settled: the host is returned with it, loaded, and the caller's to stop.
func (c *Plugins) Load(options PluginsLoad, ctx context.Context) (*NodeHost, host.LoadReport, error) {
	scripts, err := c.Scripts(options.Kind, ctx)
	if err != nil {
		return nil, host.LoadReport{}, err
	}
	// A required root guarantees its plugins: what discovery reported in one is as fatal as a plugin that failed.
	var problems []DiscoveryProblem
	c.section(func() {
		for _, problem := range c.discovery.Problems {
			if problem.Required {
				problems = append(problems, problem)
			}
		}
	})
	h, report, err := c.runtime.Load(RuntimeLoad{
		Kind: options.Kind, Core: options.Core, Scripts: scripts, Modules: options.Modules,
		Address: options.Address, Principal: options.Principal, Peer: options.Peer, Heartbeat: options.Heartbeat,
		CloseTimeout: options.CloseTimeout, ReadyTimeout: options.ReadyTimeout, ReloadDrainTimeout: options.ReloadDrainTimeout, Compression: options.Compression,
		Store:      options.Store,
		GoPackages: options.GoPackages, Telemetry: options.Telemetry, PrincipalKey: options.PrincipalKey, RevokedBy: options.RevokedBy,
		Bridged: options.Bridged, ParentServes: options.ParentServes,
	}, ctx)
	if h == nil {
		return nil, host.LoadReport{}, err
	}
	h.owner = c
	if failure, ok := errors.AsType[*PluginError](err); ok {
		if len(problems) == 0 {
			return h, report, err
		}
		return h, report, NewPluginError(failure.Required, failure.Failed, problems...)
	}
	if err != nil {
		return nil, host.LoadReport{}, err
	}
	if len(problems) > 0 {
		return h, report, NewPluginError([]host.PluginFailure{}, report.Failed, problems...)
	}
	return h, report, nil
}

// kindOf is the kind h was loaded with by this component.
func (c *Plugins) kindOf(h *NodeHost, what string) (string, error) {
	if h == nil || h.owner != c {
		return "", errors.New("plugins." + what + ": the host was not loaded by this component")
	}
	return h.kind, nil
}

// Reload applies built to the current set of h's kind (loaded by Load) and reloads h with that complete set, after its
// earlier reloads (PluginRuntime.Reload). The host's report.
func (c *Plugins) Reload(h *NodeHost, built *Built, ctx context.Context) (host.ReloadReport, error) {
	kind, err := c.kindOf(h, "reload")
	if err != nil {
		return host.ReloadReport{}, err
	}
	if err := h.refuseSyncCallback(ctx); err != nil {
		return host.ReloadReport{}, err
	}
	set, err := c.set(kind, ctx)
	if err != nil {
		return host.ReloadReport{}, err
	}
	// Applied, read and sent in one step: two reloads started together each pass the set their own build made, and reach the
	// subprocess in the order they were decided.
	var call *nodeCall
	c.section(func() {
		ship(set, kind, built)
		call, err = h.reload(set.values())
	})
	if err != nil {
		return host.ReloadReport{}, err
	}
	return h.reloaded(call, ctx)
}

// Step is a rollout step named name: Reload(h, built), one host named name.
func (c *Plugins) Step(name string, h *NodeHost) (RolloutStep, error) {
	kind, err := c.kindOf(h, "step")
	if err != nil {
		return RolloutStep{}, err
	}
	return RolloutStep{
		Name:  name,
		Kinds: []string{kind},
		Reload: func(built *Built, _ map[string][]PluginScript, ctx context.Context) (StepResult, error) {
			report, err := c.Reload(h, built, ctx)
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{Hosts: []RolloutHost{{Kind: RolloutReported, Host: name, Report: report}}}, nil
		},
	}, nil
}

// Rollout runs steps in order with built, applying it to the current set of each step's kinds right before that step, and
// stops at one that returns an error (Stopped); failed plugins and hosts do not stop it. It clears the dirty marks of the
// built plugins applied on every host of every step (not failed on any, every built kind of theirs shipped by a step) that
// did not change since the build; given a copy of a build (not the pointer Build returned), it clears none.
func (c *Plugins) Rollout(steps []RolloutStep, built *Built, ctx context.Context) (RolloutResult, error) {
	done := []RolloutStepResult{}
	failed := map[string]struct{}{}
	shipped := map[string]struct{}{}
	for _, step := range steps {
		result, err := c.rollStep(step, built, ctx)
		if err != nil {
			return RolloutResult{Steps: done, Stopped: &StoppedStep{Step: step.Name, Error: err}}, nil
		}
		for _, kind := range step.Kinds {
			shipped[kind] = struct{}{}
		}
		for _, entry := range result.Hosts {
			switch entry.Kind {
			case RolloutReported:
				for _, failure := range entry.Report.Failed {
					failed[failure.Plugin] = struct{}{}
				}
			case RolloutErrored:
				for _, plugin := range builtOf(built, step.Kinds) {
					failed[plugin] = struct{}{}
				}
			}
		}
		done = append(done, RolloutStepResult{Name: step.Name, Hosts: result.Hosts})
	}
	// A plugin with a built kind no step shipped is not applied everywhere it runs: it stays dirty.
	for _, facet := range built.Facets {
		if _, ok := shipped[facet.Facet]; !ok {
			failed[facet.Plugin] = struct{}{}
		}
	}
	// A copy of a build carries no snapshot of its own: it clears no marks.
	if built.self == built {
		var names []string
		for _, name := range built.snapshot.Names {
			if _, out := failed[name]; !out {
				names = append(names, name)
			}
		}
		c.section(func() { c.watcher.applied(names, built.snapshot) })
	}
	return RolloutResult{Steps: done}, nil
}

// rollStep applies built to the current set of step's kinds (all of them in one section, with no wait between the ship and
// the step), then runs step with the sets it applied: an error from either stops the rollout.
func (c *Plugins) rollStep(step RolloutStep, built *Built, ctx context.Context) (StepResult, error) {
	sets := make(map[string]*scriptSet, len(step.Kinds))
	for _, kind := range step.Kinds {
		set, err := c.set(kind, ctx)
		if err != nil {
			return StepResult{}, err
		}
		sets[kind] = set
	}
	applied := make(map[string][]PluginScript, len(step.Kinds))
	c.section(func() {
		for _, kind := range step.Kinds {
			ship(sets[kind], kind, built)
			applied[kind] = sets[kind].values()
		}
	})
	return step.Reload(built, applied, ctx)
}

// InstallDependencies installs plugin name's dependencies (InstallDependencies): its install mark clears, and it is dirty.
func (c *Plugins) InstallDependencies(name string, ctx context.Context) error {
	var discovered bool
	c.section(func() { discovered = c.discovered })
	if !discovered {
		if err := c.discover(ctx); err != nil {
			return err
		}
	}
	var plugin Plugin
	var found bool
	c.section(func() {
		if index := slices.IndexFunc(c.discovery.Plugins, func(each Plugin) bool { return each.Name == name }); index >= 0 {
			plugin, found = c.discovery.Plugins[index], true
		}
	})
	if !found {
		return fmt.Errorf(`plugins.installDependencies: no plugin "%s"`, name)
	}
	if err := InstallDependencies(plugin, ctx, c.options.Install); err != nil {
		return err
	}
	c.section(func() { c.watcher.installed(name) })
	return nil
}

// WatchBase adds a running base artifact's record (a base bundle's inputs, beside Base; see Recorder): `base` is set while one
// of its files differs from the bytes it read or is gone; each is compared once now. Files under `node_modules` are never
// watched.
func (c *Plugins) WatchBase(inputs BaseInputs) { c.WatchBaseCompared(inputs) }

// WatchBaseCompared is WatchBase that returns a channel closed once the comparisons of the record ended: where Pi's app reads
// nothing of `watchBase`, the Go app's `BaseWatched` waits for them.
func (c *Plugins) WatchBaseCompared(inputs BaseInputs) <-chan struct{} {
	var compared <-chan struct{}
	c.section(func() { compared = c.watcher.recordBase(inputs) })
	return compared
}

// InstallPlugin installs new plugins from git or npm into root, one of the roots (InstallPlugin), and marks `set`: the next
// build discovers and builds them. It returns their names.
func (c *Plugins) InstallPlugin(source PluginSource, root string, ctx context.Context) ([]string, error) {
	if !slices.Contains(rootPaths(c.options.Roots), root) {
		return nil, fmt.Errorf("plugins.installPlugin: %s is not one of the roots", root)
	}
	options := InstallPluginOptions{InstallOptions: c.options.Install, Kinds: c.kindNames, Roots: c.options.Roots}
	names, err := InstallPlugin(source, root, ctx, options)
	if err != nil {
		return nil, err
	}
	c.section(c.watcher.added)
	return names, nil
}

// Warm starts a spare Node process for the next Load (PluginRuntime.Warm).
func (c *Plugins) Warm(ctx context.Context) error { return c.runtime.Warm(ctx) }

// Close stops the watcher, closes the spare Node process of its runtime (PluginRuntime.Close) and waits for what it started.
// The hosts it loaded are their owners': it closes none. The spare's close error is dropped: its process never loaded
// anything, and Close, as Pi's `plugins.close()`, returns nothing.
func (c *Plugins) Close() {
	c.section(c.watcher.close)
	c.watcher.join()
	c.loop.Wait()
	c.mappings.close()
	//portlint:allow ctxbackground Pi's plugins.close() takes no signal and returns nothing; the spare's close is bounded by the process's own stop grace
	_ = c.runtime.Close(context.Background())
}

// discover discovers the plugins again; the changes of the plugin set and of `package.json`s seen until now clear.
func (c *Plugins) discover(ctx context.Context) error {
	var seen DiscoverySnapshot
	c.section(func() { seen = c.watcher.discoverySnapshot() })
	discovery, err := DiscoverPlugins(c.options.Roots, DiscoveryOptions{Kinds: c.kindNames}, ctx)
	if err != nil {
		return err
	}
	c.section(func() {
		c.discovery = discovery
		c.discovered = true
		// A plugin no longer discovered loses its builds: a name added again is built anew, never served an old build.
		for key := range c.built {
			name := key[:strings.IndexByte(key, 0)]
			if !slices.ContainsFunc(discovery.Plugins, func(plugin Plugin) bool { return plugin.Name == name }) {
				delete(c.built, key)
			}
		}
		c.watcher.know(discovery, seen)
	})
	return nil
}

// set is kind's current set, made at its first use from every discovered plugin's facet of it (built if needed).
func (c *Plugins) set(kind string, ctx context.Context) (*scriptSet, error) {
	var set *scriptSet
	var discovered bool
	c.section(func() { set, discovered = c.current[kind], c.discovered })
	if set != nil {
		return set, nil
	}
	if !discovered {
		if err := c.discover(ctx); err != nil {
			return nil, err
		}
	}
	var plugins []Plugin
	var all []Plugin
	var cached []BuiltFacet
	c.section(func() {
		all = c.discovery.Plugins
		for _, plugin := range all {
			if _, ok := plugin.Facets[kind]; ok {
				plugins = append(plugins, plugin)
				cached = append(cached, c.built[buildKey(plugin.Name, kind)])
			}
		}
	})
	scripts := make([]PluginScript, len(plugins))
	var jobs []buildJob
	var at []int
	for i, plugin := range plugins {
		if cached[i].Kind != "" {
			scripts[i] = PluginScript{Plugin: plugin, Built: cached[i]}
		} else {
			jobs = append(jobs, buildJob{plugin: plugin, kind: kind})
			at = append(at, i)
		}
	}
	built, err := c.buildAll(jobs, all, ctx)
	if err != nil {
		return nil, err
	}
	for j, i := range at {
		scripts[i] = PluginScript{Plugin: plugins[i], Built: built[j]}
	}
	// Built concurrently: a set made meanwhile (by a concurrent first use) wins.
	c.section(func() {
		set = c.current[kind]
		if set == nil {
			set = newScriptSet()
			for _, script := range scripts {
				set.set(script.Plugin.Name, script)
			}
			c.current[kind] = set
		}
	})
	return set, nil
}

// buildJob is one facet to build.
type buildJob struct {
	plugin Plugin
	kind   string
}

// buildKey is the key of a plugin's facet of a kind in c.built.
func buildKey(plugin, kind string) string { return plugin + "\x00" + kind }

// buildAll builds jobs concurrently (Promise.all), each with plugins as the discovered ones, and returns the results in the
// order of jobs; the error of the first job that failed, once every build ended.
func (c *Plugins) buildAll(jobs []buildJob, plugins []Plugin, ctx context.Context) ([]BuiltFacet, error) {
	results := make([]BuiltFacet, len(jobs))
	errs := make([]error, len(jobs))
	var group sync.WaitGroup
	for i, job := range jobs {
		group.Go(func() { results[i], errs[i] = c.buildOne(job.plugin, job.kind, plugins, ctx) })
	}
	group.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

// buildOne builds plugin's facet of kind and keeps it as the latest; the watcher learns what it read. A build that fails is a
// failed result; an error is the abort of ctx only.
func (c *Plugins) buildOne(plugin Plugin, kind string, plugins []Plugin, ctx context.Context) (BuiltFacet, error) {
	// Discovery finds facet files of the app's kinds only.
	definition := c.kinds[kind]
	options := BuildOptions{
		HostPackages: definition.HostPackages, Platform: definition.Platform, Kinds: c.kindNames, Plugins: plugins,
		Api: c.options.Api, PublicPath: c.options.PublicPath, Node: c.options.Node, mappings: c.mappings,
	}
	built, err := BuildFacet(plugin, kind, options, ctx)
	if err != nil {
		return BuiltFacet{}, err
	}
	c.section(func() {
		c.built[buildKey(plugin.Name, kind)] = built
		c.watcher.used([]BuiltFacet{built})
	})
	return built, nil
}

// ship applies built to set, kind's current set: its removed plugins and those rebuilt without a facet of kind leave it, its
// facets of kind replace theirs.
func ship(set *scriptSet, kind string, built *Built) {
	for _, name := range built.Removed {
		set.delete(name)
	}
	for name, plugin := range built.Plugins {
		if _, ok := plugin.Facets[kind]; !ok {
			set.delete(name)
		}
	}
	for _, facet := range built.Facets {
		if plugin, ok := built.Plugins[facet.Plugin]; ok && facet.Facet == kind {
			set.set(facet.Plugin, PluginScript{Plugin: plugin, Built: facet})
		}
	}
}

// builtOf are the plugins of built with a facet of one of kinds.
func builtOf(built *Built, kinds []string) []string {
	var names []string
	for _, facet := range built.Facets {
		if slices.Contains(kinds, facet.Facet) {
			names = append(names, facet.Plugin)
		}
	}
	return names
}
