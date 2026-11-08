package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/src/node/plugins/testing.ts (`testPlugin`, slice 12 §6, "Testing a plugin alone"): a plugin built as
// production builds it, evaluated with the app's real API modules, loaded on an in-memory host beside stub facets serving
// the contracts it uses. The registries it contributes to that nobody serves are served by a collecting facet, which
// gathers their entries for the test; the contracts its requests wait for unserved are listed.
//
// Upstream runs it all in one process, with live JavaScript values. Here the plugin's host is the Node subprocess (the only
// place a plugin evaluates, docs/plan/chord-plugins-node-evaluator.md), so the helper has the production topology: the plugin's
// objects live on the NodeHost (`TestedPlugin.Host`, reached through NodeHost.Get and NodeHost.Hold), the stubs are Go facets
// on a Go host that reach the plugin through the bridge (BridgedFacet) or JavaScript facets in modules loaded beside it
// (CoreFacet), and the helper's own reads of the host's internals (the collection's fixed point, `unservedNames`) are control
// requests of node/testing.mjs. The residue that cannot cross the process boundary (a live closure as a stub, `root`'s
// closure, a registry token) is D502 and D503 (docs/parity/divergences).

// pig divergence (D502): Pi's stubs are live JavaScript facets of one in-process host.
//
// TestStub is a stub facet of a Go test: the Go facet that serves the contracts a tested plugin uses, loaded on the helper's
// Go host and bridged to the plugin's host (BridgedFacet), so its contracts must be local types without keyed members. A stub
// that serves or contributes to a registry, or a remote type, is a JavaScript module (TestPluginOptions.Core).
type TestStub struct {
	// Name is the stub's plugin name (`@acme/notes`).
	Name string
	// Facet is the stub, an ordinary Go facet.
	Facet host.Facet
	// Contracts are the local types the facet serves that the plugin uses.
	Contracts []*contracts.Contract
}

// TestPluginOptions is what TestPlugin takes besides the plugin's directory.
type TestPluginOptions struct {
	// Build builds the plugin's facet (build.BuildFacet): required.
	Build Builder
	// Kind is the facet kind to test (`worker`): the plugin's facet of it is built and loaded.
	Kind string
	// Modules are the app's API modules by specifier (files the Node host imports: definitions only), as the kind's
	// production host has them.
	Modules HostPackages
	// Api is the app's API, checked as production checks it; required when Modules hold versioned API modules.
	Api *model.AppApi
	// Kinds are the app's facet kinds, for the per-kind file check: by default Kind and every kind the files of the plugin's
	// root name (`contract.web.tsx`: `web`), so the check is active as in production.
	Kinds []string
	// Platform is where the kind's builds run (PlatformNode by default).
	Platform model.Platform
	// Stubs are Go facets serving what the plugin uses (other plugins' contracts, core objects), bridged to its host.
	Stubs []TestStub
	// Core are JavaScript facets loaded beside the plugin on its host: stubs that serve or contribute to registries, or
	// serve remote types.
	Core []CoreFacet
	// Principal is the plugin host's principal, as the kind's production host has one.
	Principal data.Principal
	// Address is the plugin host's address, as the kind's production host has one.
	Address *contracts.Address
	// Node are the Node subprocess's options (its CacheDir, ...); its OnError also gets what the host reports.
	Node NodeOptions
}

// Contribution is one entry of a collected registry's table: the contributing plugin, its object type for an object
// contributor (empty for a facet), the registry version it was built against, and the value as data.
type Contribution struct {
	Plugin  string
	Type    string
	Version int
	Value   delta.JsonValue
}

// TestedPlugin is a plugin loaded alone: its live host, the registries collected for it, and what it waits for.
type TestedPlugin struct {
	// Host is the Node host the plugin and the JavaScript stubs are loaded on: get its objects (NodeHost.Get, NodeHost.Hold),
	// call them, watch their state.
	Host *NodeHost
	// Stubs is the Go host the Go stubs are loaded on.
	Stubs host.Host
	// Plugin is the plugin as discovered.
	Plugin model.Plugin

	runtime *PluginRuntime

	mu     sync.Mutex
	errors []NodeHostError
}

// testPluginPeer is who the Go side is, as the plugin's host stamps it: the stubs' host, when no principal is given.
var testPluginPeer = delta.JsonObjectOf("kind", "test-plugin")

// versionedModule is a host package named with a version of an API (`@acme/app/v3`, `@acme/app/v3/worker`).
var versionedModule = regexp.MustCompile(`/v\d+(?:/|$)`)

// pig divergence (D502): the plugin's host is the Node subprocess, the stubs' a Go host (TestedPlugin.Stubs).
//
// TestPlugin builds the plugin in dir as production does (BuildFacet with the kind's options), loads its facet of
// options.Kind with options.Modules on a new Node host with an in-memory store, the stubs beside it, then serves the registries
// it contributes to that nobody serves with a collecting facet. It fails when the build, the evaluation or a setup fails
// (naming it). Close the result.
func TestPlugin(dir string, options TestPluginOptions, ctx context.Context) (*TestedPlugin, error) {
	modules := slices.Sorted(func(yield func(string) bool) {
		for specifier := range options.Modules {
			if !yield(specifier) {
				return
			}
		}
	})
	if i := slices.IndexFunc(modules, versionedModule.MatchString); i >= 0 && options.Api == nil {
		return nil, fmt.Errorf("testPlugin: modules hold the app's versioned API (%q): pass api, as production does", modules[i])
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	if real, err = filepath.Abs(real); err != nil {
		return nil, err
	}
	root, err := testPluginRoot(real)
	if err != nil {
		return nil, err
	}
	kinds := options.Kinds
	if kinds == nil {
		named, err := kindsNamedIn(root)
		if err != nil {
			return nil, err
		}
		kinds = []string{options.Kind}
		for _, kind := range named {
			if !slices.Contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
		}
	}
	found, others, err := testPluginIn(real, root, kinds, ctx)
	if err != nil {
		return nil, err
	}
	platform := options.Platform
	if platform == "" {
		platform = model.PlatformNode
	}
	mappings := newMappingEvaluator(NodeOptions{})
	defer mappings.close()
	built, err := options.Build(found, options.Kind, model.BuildOptions{
		HostPackages: modules, Platform: platform, Kinds: kinds, Plugins: others, Api: options.Api, Mappings: mappings,
	}, ctx)
	if err != nil {
		return nil, err
	}
	if built.Kind == model.BuiltFacetFailed {
		return nil, fmt.Errorf("testPlugin: %q failed to build:\n%s", found.Name, strings.Join(built.Errors, "\n"))
	}

	t := &TestedPlugin{Plugin: found}
	peerPrincipal := testPluginPeer
	if options.Principal != nil {
		peerPrincipal = options.Principal
	}
	stubs, bridged, err := t.loadStubs(options, peerPrincipal, ctx)
	if err != nil {
		return nil, err
	}
	t.Stubs = stubs

	node := options.Node
	forward := node.OnError
	node.OnError = func(event NodeHostError) {
		t.mu.Lock()
		t.errors = append(t.errors, event)
		t.mu.Unlock()
		if forward != nil {
			forward(event)
		}
	}
	t.runtime = NewPluginRuntime(node)
	nodeHost, report, err := t.runtime.Load(RuntimeLoad{
		Kind: options.Kind, Core: options.Core, Scripts: []PluginScript{{Plugin: found, Built: built}},
		Modules: options.Modules, Address: options.Address, Principal: options.Principal, Peer: peerPrincipal,
		Store: &StoreOptions{Path: ":memory:"}, Bridged: bridged,
	}, ctx)
	if nodeHost != nil {
		t.Host = nodeHost
	}
	if err == nil && len(report.Failed) > 0 {
		err = t.Host.loadFailed(report.Failed, ctx)
	}
	if err == nil {
		_, err = t.Host.collect(ctx)
	}
	if err != nil {
		_ = t.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return t, nil
}

// loadStubs loads the Go stubs on a new Go host and returns it with the bridged facets for the plugin's host.
func (t *TestedPlugin) loadStubs(options TestPluginOptions, principal data.Principal, ctx context.Context) (host.Host, []BridgedFacet, error) {
	stubs, err := host.CreateHost(host.HostOptions{Principal: principal, OnError: func(host.HostError) {}})
	if err != nil {
		return nil, nil, err
	}
	facets := make([]host.HostFacet, len(options.Stubs))
	bridged := make([]BridgedFacet, len(options.Stubs))
	for i, stub := range options.Stubs {
		facets[i] = host.HostFacet{Name: stub.Name, Facet: stub.Facet}
		bridged[i] = BridgedFacet{Name: stub.Name, Host: stubs, Contracts: stub.Contracts}
	}
	report, err := stubs.Load(facets, ctx)
	if err == nil {
		err = setupFailed(report.Failed)
	}
	if err != nil {
		_ = stubs.Close(context.WithoutCancel(ctx))
		return nil, nil, err
	}
	return stubs, bridged, nil
}

// setupFailed is the error of a load whose plugins failed: a test of a plugin whose setup (or a stub's) failed has nothing to
// test. nil for none.
func setupFailed(failed []host.PluginFailure) error {
	if len(failed) == 0 {
		return nil
	}
	messages := make([]string, len(failed))
	for i, failure := range failed {
		messages[i] = failure.Error.Error()
	}
	return &testSetupError{message: "testPlugin: setup failed: " + strings.Join(messages, "; "), cause: failed[0].Error}
}

// loadFailed is the error of the plugin's own load having failed plugins: a plugin that never reached the host failed in its
// evaluation, and its error is returned as it is, as testing.ts's `evaluateFacet` throws it before the host exists; the others
// are setups that threw (setupFailed).
func (h *NodeHost) loadFailed(failed []host.PluginFailure, ctx context.Context) error {
	call, err := h.send("testOffered", &offeredRequest{})
	if err != nil {
		return err
	}
	reply, err := h.await(call, ctx)
	if err != nil {
		return err
	}
	var answer struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return err
	}
	for _, failure := range failed {
		if !slices.Contains(answer.Names, failure.Plugin) {
			return failure.Error
		}
	}
	return setupFailed(failed)
}

// testSetupError is a failed setup: the plugins' messages, and the first one's error as its cause (testing.ts's `cause`).
type testSetupError struct {
	message string
	cause   error
}

func (e *testSetupError) Error() string { return e.message }

func (e *testSetupError) Unwrap() error { return e.cause }

// testPluginRoot is the root the plugin at real (a directory, or a single-file plugin's file) is in, as production lists it:
// its directory's parent, or, for a plugin in a checkout of several (a parent with its own `package.json` or `.git`), that
// checkout's parent.
func testPluginRoot(real string) (string, error) {
	parent := filepath.Dir(real)
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return parent, nil
	}
	for _, marker := range []string{"package.json", ".git"} {
		if _, err := os.Stat(filepath.Join(parent, marker)); err == nil {
			return filepath.Dir(parent), nil
		}
	}
	return parent, nil
}

// kindsNamedIn is the kinds files under root name (`contract.web.tsx`: `web`; not `*.test.*`, `*.spec.*` or `*.d.ts`),
// `node_modules` and hidden directories skipped; one walk per TestPlugin call.
func kindsNamedIn(root string) ([]string, error) {
	var kinds []string
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			if entry.IsDir() {
				if err := walk(filepath.Join(dir, name)); err != nil {
					return err
				}
			}
			parts := strings.Split(name, ".")
			if len(parts) < 3 || !entry.Type().IsRegular() {
				continue
			}
			// Tests and declaration files name no kind (`stats.test.ts`, `env.d.ts`).
			if segment := parts[len(parts)-2]; segment != "test" && segment != "spec" && segment != "d" && !slices.Contains(kinds, segment) {
				kinds = append(kinds, segment)
			}
		}
		return nil
	}
	return kinds, walk(root)
}

// testPluginIn is the plugin at real (its directory, or a single-file plugin's file), discovered with the others of root.
func testPluginIn(real, root string, kinds []string, ctx context.Context) (model.Plugin, []model.Plugin, error) {
	discovery, err := model.DiscoverPlugins([]model.PluginRoot{{Path: root}}, model.DiscoveryOptions{Kinds: kinds}, ctx)
	if err != nil {
		return model.Plugin{}, nil, err
	}
	for _, each := range discovery.Plugins {
		if each.Layout == model.LayoutDirectory && each.Dir == real || each.Layout != model.LayoutDirectory && slices.Contains(mapValues(each.Facets), real) {
			return each, discovery.Plugins, nil
		}
	}
	reason := ""
	for _, problem := range discovery.Problems {
		if problem.Path == real {
			reason = ": " + problem.Error
			break
		}
	}
	return model.Plugin{}, nil, fmt.Errorf("testPlugin: %s is no plugin%s", real, reason)
}

// mapValues are the values of m.
func mapValues(m map[string]string) []string {
	values := make([]string, 0, len(m))
	for _, value := range m {
		values = append(values, value)
	}
	return values
}

// collectRequest and its siblings are the control requests of node/testing.mjs.
type (
	collectRequest       struct{ envelope }
	contributionsRequest struct {
		envelope
		Name string `json:"name"`
	}
	unservedRequest struct{ envelope }
	offeredRequest  struct{ envelope }
	rootRequest     struct {
		envelope
		Principal data.Principal `json:"principal,omitempty"`
	}
)

// collect is `testCollect`: the registries with contributions and no provider are served by a collecting facet, until no new
// one appears. The names collected.
func (h *NodeHost) collect(ctx context.Context) ([]string, error) {
	call, err := h.send("testCollect", &collectRequest{})
	if err != nil {
		return nil, err
	}
	reply, err := h.await(call, ctx)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, err
	}
	return answer.Names, nil
}

// Errors is what the plugin's host reported (`onError`), in order.
func (t *TestedPlugin) Errors() []NodeHostError {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.errors)
}

// pig divergence (D503): the registry is named, not a token, and a value crosses as data.
//
// Contributions is the current entries of registry name, one the plugin contributes to that neither it nor a stub serves (the
// helper serves it, collecting), sorted by plugin as the registry's table is. A registry it does not collect is an error. A
// value crosses as data.
func (t *TestedPlugin) Contributions(name string, ctx context.Context) ([]Contribution, error) {
	call, err := t.Host.send("testContributions", &contributionsRequest{Name: name})
	if err != nil {
		return nil, err
	}
	reply, err := t.Host.await(call, ctx)
	if err != nil {
		if node, ok := errors.AsType[*NodeError](err); ok {
			return nil, errors.New(node.Message)
		}
		return nil, err
	}
	var answer struct {
		Table []struct {
			Plugin  string          `json:"plugin"`
			Type    string          `json:"type"`
			Version int             `json:"version"`
			Value   json.RawMessage `json:"value"`
		} `json:"table"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, err
	}
	table := make([]Contribution, len(answer.Table))
	for i, entry := range answer.Table {
		value, err := unmarshalWire(entry.Value)
		if err != nil {
			return nil, err
		}
		table[i] = Contribution{Plugin: entry.Plugin, Type: entry.Type, Version: entry.Version, Value: value}
	}
	return table, nil
}

// Unserved is the names of the contracts (and registries) requests on the plugin's host wait for unbound because nobody
// serves them, sorted.
func (t *TestedPlugin) Unserved(ctx context.Context) ([]string, error) {
	call, err := t.Host.send("testUnserved", &unservedRequest{})
	if err != nil {
		return nil, err
	}
	reply, err := t.Host.await(call, ctx)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, err
	}
	return answer.Names, nil
}

// pig divergence (D503): Pi's `root` takes a closure.
//
// RootPrincipal is the principal code outside objects runs as on the plugin's host: `host.root`'s context principal, the host's
// own when principal is nil, else principal (RootOptions' `principal`). The closure `root` takes upstream cannot cross the
// process boundary.
func (t *TestedPlugin) RootPrincipal(principal data.Principal, ctx context.Context) (data.Principal, error) {
	call, err := t.Host.send("testRoot", &rootRequest{Principal: principal})
	if err != nil {
		return nil, err
	}
	reply, err := t.Host.await(call, ctx)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Principal data.Principal `json:"principal"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, err
	}
	return answer.Principal, nil
}

// Close closes the plugin's host (and its store), then the stubs' host.
func (t *TestedPlugin) Close(ctx context.Context) error {
	var errs []error
	if t.Host != nil {
		errs = append(errs, t.Host.Close(ctx))
	}
	if t.Stubs != nil {
		errs = append(errs, t.Stubs.Close(ctx))
	}
	if t.runtime != nil {
		errs = append(errs, t.runtime.Close(ctx))
	}
	return errors.Join(errs...)
}
