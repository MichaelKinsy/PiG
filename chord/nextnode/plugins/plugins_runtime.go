package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/next-node/plugins/runtime.ts. The plugin runtime (slice 10 §3, D11): loading and reloading hosts
// with shipped scripts, without building (no esbuild, no watcher), so a worker evaluates what the server built. Pi's runtime
// itself runs, unchanged, in the Node subprocess of the host that loads the plugins (docs/plan/chord-plugins-node-evaluator.md):
// per host it keeps the facet loaded for every plugin and passes the host its complete set at each reload (unchanged plugins,
// the same digest, as the same facets; removed ones left out; a plugin whose build or evaluation failed keeps its loaded
// facet, left out at startup, and is reported with the host's report), and one reload runs after another per host. This
// file is what the Go side keeps of it: starting that host, shipping scripts to it as data and reading its reports.
// Upstream turns V8's compilation cache off in createPluginRuntime: that runs in the subprocess.

// PluginScript is one plugin's facet of a kind as shipped to a host (plain data): its build and its plugin record.
type PluginScript struct {
	Plugin Plugin
	Built  BuiltFacet
}

// HostPackages are the host packages a script's imports are answered with, by specifier: the file of the module (an absolute
// path) the subprocess imports once, and shares between the scripts of its host. `@earendil-works/chord/next` is always
// Pi's own, the subprocess's; naming it here replaces nothing.
type HostPackages map[string]string

// CoreFacet is one of the app's own facets: its name (a plugin name) and the module whose default export is the facet,
// a file the subprocess imports.
type CoreFacet struct {
	Name   string
	Module string
}

// RuntimeLoad is what PluginRuntime.Load takes.
type RuntimeLoad struct {
	Kind string
	// Core are the app's own facets, loaded before the plugins.
	Core []CoreFacet
	// Scripts is the complete set of Kind.
	Scripts []PluginScript
	// Modules are the host packages the scripts' imports are answered with; kept for the host's reloads.
	Modules HostPackages
	// Address is the subprocess host's address (`createHost`'s `address`), nil for a client: the one a remote type's
	// `hostAddress` places objects at, and the one the app routes to the host's link.
	Address *contracts.Address
	// Principal is who the subprocess host acts as for its own requests (`createHost`'s `principal`); none for a client. The
	// app stamps the host with it when it attaches the link (RouteOptions.Resolve's, LinkOptions').
	Principal data.Principal
	// Peer is who the Go host that attaches the link is, as the subprocess's host stamps it: the `principal` of its
	// `connect` (trusted), as the server's is to a forked worker. Required.
	Peer data.Principal
	// Heartbeat is the subprocess host's heartbeat (`createHost`'s `heartbeat`): it closes a link silent for Close, 30 s by
	// default, its handshake included, and the process with it, so the app attaches NodeHost.Link in time.
	Heartbeat *host.HeartbeatOptions
	// CloseTimeout and ReadyTimeout are the subprocess host's `closeTimeout` and `readyTimeout` (host.HostOptions'): how long
	// a native's close and an ending object's running calls are waited for (30 s by default), and how long an activation
	// attempt and a `get` may take until the object is live (60 s). Nil: the host's defaults.
	CloseTimeout *time.Duration
	ReadyTimeout *time.Duration
	// Compression makes the subprocess host compress as Pi's Node hosts do (`createHost({ compression: nodeCompression })`):
	// the JSON parts above 16 KiB it originates are deflated, and the deflated parts it owns inflated in place. The Go
	// host the link is attached to compresses its own parts when it is created with host.HostOptions.Compression
	// (nextnode.NodeCompression): a compressor is a host's, not a link's, and a host without one inflates what it gets.
	Compression bool
	// Store is the subprocess host's store (`createHost`'s `store`); nil: none (StoreOptions).
	Store *StoreOptions
}

// PluginRuntime starts the Node subprocesses that host plugins, and loads and reloads them with complete sets of shipped
// scripts. It is Pi's `createPluginRuntime()`.
type PluginRuntime struct {
	node NodeOptions
}

// NewPluginRuntime is upstream's `createPluginRuntime()`: how its subprocesses are started.
func NewPluginRuntime(node NodeOptions) *PluginRuntime { return &PluginRuntime{node: node} }

// loadRequest is the `load` control request of host.mjs.
type loadRequest struct {
	envelope
	Kind      string            `json:"kind"`
	Core      []coreWire        `json:"core"`
	Scripts   []scriptWire      `json:"scripts"`
	Modules   map[string]string `json:"modules"`
	Address   *addressWire      `json:"address,omitempty"`
	Principal data.Principal    `json:"principal,omitempty"`
	Peer      data.Principal    `json:"peer"`
	Heartbeat *heartbeatWire    `json:"heartbeat,omitempty"`
	// CloseTimeout and ReadyTimeout are milliseconds.
	CloseTimeout *int64     `json:"closeTimeout,omitempty"`
	ReadyTimeout *int64     `json:"readyTimeout,omitempty"`
	Compression  bool       `json:"compression,omitempty"`
	Store        *storeWire `json:"store,omitempty"`
}

// heartbeatWire is a host.HeartbeatOptions in milliseconds, as `createHost`'s `heartbeat`.
type heartbeatWire struct {
	Ping  *int64 `json:"ping,omitempty"`
	Close *int64 `json:"close,omitempty"`
}

type coreWire struct {
	Name   string `json:"name"`
	Module string `json:"module"`
}

type addressWire struct {
	Kind   string            `json:"kind"`
	Params *delta.JsonObject `json:"params"`
	Key    string            `json:"key"`
}

// scriptWire is a PluginScript as data: what Pi's evaluation reads of the plugin and of its build.
type scriptWire struct {
	Plugin pluginWire `json:"plugin"`
	Built  builtWire  `json:"built"`
}

type pluginWire struct {
	Name         string            `json:"name"`
	Dir          string            `json:"dir"`
	Facets       map[string]string `json:"facets"`
	Dependencies []string          `json:"dependencies"`
	External     []string          `json:"external"`
}

type builtWire struct {
	Kind   BuiltKind `json:"kind"`
	Plugin string    `json:"plugin"`
	Facet  string    `json:"facet"`
	Source string    `json:"source,omitempty"`
	Digest string    `json:"digest,omitempty"`
	Errors []string  `json:"errors,omitempty"`
}

func scriptsWire(scripts []PluginScript) []scriptWire {
	wire := make([]scriptWire, len(scripts))
	for i, script := range scripts {
		plugin := script.Plugin
		wire[i] = scriptWire{
			Plugin: pluginWire{
				Name: plugin.Name, Dir: plugin.Dir, Facets: plugin.Facets,
				Dependencies: nonNil(plugin.Dependencies), External: nonNil(plugin.External),
			},
			Built: builtWire{
				Kind: script.Built.Kind, Plugin: script.Built.Plugin, Facet: script.Built.Facet,
				Source: script.Built.Source, Digest: script.Built.Digest, Errors: script.Built.Errors,
			},
		}
	}
	return wire
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// Load starts a Node subprocess whose host evaluates options.Scripts (the complete set of options.Kind) with options.Modules
// and loads them with options.Core. A plugin whose build or evaluation failed is left out and reported with the host's
// report (the failures of the scripts first), each error a *NodeError whose Stack names the plugin's files and lines. It
// returns the host's error as it is: the subprocess is stopped then.
//
// The host is the child of the Go side, as a forked worker is of its server (next-node/workers/main.ts): it connects the
// link to its parent at load. The app attaches the other end, NodeHost.Link, to a Go host as Pi's app does: a route of the
// host's kind (`host.Route`, whose Resolve returns it) or `host.Accept`, in time (RuntimeLoad.Heartbeat). A NodeHost needs no
// Go host to load, so one Go host can own any number of them, and a Go host that has a parent of its own can load plugins.
func (r *PluginRuntime) Load(options RuntimeLoad, ctx context.Context) (*NodeHost, host.LoadReport, error) {
	if options.Peer == nil {
		return nil, host.LoadReport{}, errors.New("plugins.load: a Peer is required")
	}
	request := &loadRequest{
		Kind: options.Kind, Core: make([]coreWire, len(options.Core)), Scripts: scriptsWire(options.Scripts),
		Modules: map[string]string(options.Modules), Principal: options.Principal, Peer: options.Peer,
	}
	if request.Modules == nil {
		request.Modules = map[string]string{}
	}
	for i, core := range options.Core {
		request.Core[i] = coreWire(core)
	}
	if options.Address != nil {
		request.Address = &addressWire{Kind: options.Address.Kind, Params: options.Address.Params, Key: options.Address.Key}
	}
	if options.Heartbeat != nil {
		request.Heartbeat = &heartbeatWire{}
		if options.Heartbeat.Ping != nil {
			ping := options.Heartbeat.Ping.Milliseconds()
			request.Heartbeat.Ping = &ping
		}
		if options.Heartbeat.Close != nil {
			closing := options.Heartbeat.Close.Milliseconds()
			request.Heartbeat.Close = &closing
		}
	}
	request.Compression = options.Compression
	if options.CloseTimeout != nil {
		closing := options.CloseTimeout.Milliseconds()
		request.CloseTimeout = &closing
	}
	if options.ReadyTimeout != nil {
		ready := options.ReadyTimeout.Milliseconds()
		request.ReadyTimeout = &ready
	}
	store, err := storeToWire(options.Store)
	if err != nil {
		return nil, host.LoadReport{}, err
	}
	request.Store = store
	h, err := startNode(r.node, ctx)
	if err != nil {
		return nil, host.LoadReport{}, err
	}
	h.kind = options.Kind
	call, err := h.send("load", request)
	if err == nil {
		var reply nodeReply
		if reply, err = h.await(call, ctx); err == nil {
			var report host.LoadReport
			if report, err = loadReport(reply.Result); err == nil {
				return h, report, nil
			}
		}
	}
	_ = h.Close(context.WithoutCancel(ctx))
	return nil, host.LoadReport{}, err
}

// Reload reloads h (loaded by Load) with scripts, the complete set now, after its earlier reloads: unchanged plugins (the same
// digest) as the same facets, new scripts evaluated, a plugin whose build or evaluation failed passed as its loaded facet (an
// added one left out; both given to the host's closure rule), loaded plugins not in scripts left out. What the host committed
// is the loaded set, also when the reload returns `aborted` after its commit (a *NodeError wrapping the ChordError, whose
// details.committed says what the commit applied). It blocks until the report.
func (r *PluginRuntime) Reload(h *NodeHost, scripts []PluginScript, ctx context.Context) (host.ReloadReport, error) {
	call, err := h.reload(scripts)
	if err != nil {
		return host.ReloadReport{}, err
	}
	return h.reloaded(call, ctx)
}

// reloadRequest is the `reload` control request of host.mjs.
type reloadRequest struct {
	envelope
	Scripts []scriptWire `json:"scripts"`
}

// reload sends a reload of h; the requests of two calls reach the subprocess in the order of the calls, which its runtime
// runs one after another.
func (h *NodeHost) reload(scripts []PluginScript) (*nodeCall, error) {
	return h.send("reload", &reloadRequest{Scripts: scriptsWire(scripts)})
}

// reloaded waits for the report of a reload sent.
func (h *NodeHost) reloaded(call *nodeCall, ctx context.Context) (host.ReloadReport, error) {
	reply, err := h.await(call, ctx)
	if err != nil {
		return host.ReloadReport{}, err
	}
	return reloadReport(reply.Result)
}

// failureWire is a host.PluginFailure as host.mjs sends it.
type failureWire struct {
	Plugin string     `json:"plugin"`
	Error  *wireError `json:"error"`
}

func failures(wire []failureWire) []host.PluginFailure {
	out := make([]host.PluginFailure, len(wire))
	for i, failure := range wire {
		out[i] = host.PluginFailure{Plugin: failure.Plugin, Error: failure.Error.err()}
	}
	return out
}

func loadReport(result json.RawMessage) (host.LoadReport, error) {
	var wire struct {
		Failed []failureWire `json:"failed"`
	}
	if err := json.Unmarshal(result, &wire); err != nil {
		return host.LoadReport{}, err
	}
	return host.LoadReport{Failed: failures(wire.Failed)}, nil
}

func reloadReport(result json.RawMessage) (host.ReloadReport, error) {
	var wire struct {
		Plugins struct {
			Added   []string `json:"added"`
			Changed []string `json:"changed"`
			Removed []string `json:"removed"`
		} `json:"plugins"`
		Failed  []failureWire `json:"failed"`
		Objects []struct {
			Object string          `json:"object"`
			Kind   string          `json:"kind"`
			Cause  json.RawMessage `json:"cause"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(result, &wire); err != nil {
		return host.ReloadReport{}, err
	}
	report := host.ReloadReport{
		Plugins: host.PluginChanges{Added: nonNil(wire.Plugins.Added), Changed: nonNil(wire.Plugins.Changed), Removed: nonNil(wire.Plugins.Removed)},
		Failed:  failures(wire.Failed),
		Objects: make([]host.ReloadedObject, len(wire.Objects)),
	}
	for i, object := range wire.Objects {
		report.Objects[i] = host.ReloadedObject{Object: object.Object, Kind: host.ReloadedObjectKind(object.Kind)}
		if len(object.Cause) == 0 {
			continue
		}
		var cause struct {
			Kind  data.FailureKind `json:"kind"`
			Error json.RawMessage  `json:"error"`
		}
		if err := json.Unmarshal(object.Cause, &cause); err != nil {
			return host.ReloadReport{}, err
		}
		chord, ok := chordErrorOf(cause.Error)
		if !ok {
			return host.ReloadReport{}, errors.New("a reload's parked object has a cause that is no ChordError")
		}
		report.Objects[i].Cause = data.FailureCause{Kind: cause.Kind, Error: chord}
	}
	return report, nil
}
