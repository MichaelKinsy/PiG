package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Go core facets in the plugins' closure (docs/plan/chord-plugins-node-evaluator.md 10.4). No Pi file corresponds to this one:
// in Pi the app's own facets (`core`) and the plugins' load into one host, and Pi's closure rule and the plugin runtime's
// `adopt` rule see them alike. Here the plugins' host is in a Node subprocess and the app's own facets are loaded on a Go
// host, so each Go core facet that serves local types a plugin may use is represented in the Node host by a proxy facet that
// serves the same contracts (node/proxy.mjs) and forwards to the Go host's own objects over the control channel (the
// reference RPC of plugins_node_reference.go, in the other direction). The proxy is shipped as an ordinary script of the
// plugins' runtime (named as the Go core facet), so it is in the Node host's load and reload sets: Pi's closure rule keeps a
// plugin that needs a type of a Go core facet old when that facet's new code failed (it is shipped as a failed build), and
// `adopt` keeps the proxy's loaded facet.
//
// Async contract of the Node host's requests to the Go host (`get`, `watch`, `bridgeCall`): each runs in its own
// goroutine, concurrently with the others, with a context that ends when the request's `abort` arrives or the process ends
// and answers with one `bridgeAnswer` (a ChordError crosses as data). The names are the bridge's own: a Go function call
// of the callbacks (`call`, answered by `answer`, plugins_node_callbacks.go) has its own ids, and the two share the channel. A `watch` lives until its `unwatch`, the end of its source
// (`watchEnd`) or the process's end; its frames are sent in the order the Go host's watch delivers them, over the one channel,
// by that watch's own delivery. `release` and `unwatch` are notifications: the Go reference or watch is released after the
// request that was running; a reference's watches end with it.

// bridgePackage is the host package the proxy scripts import (node/bridge.mjs's `GO_PACKAGE`).
const bridgePackage = "@earendil-works/chord/next/go"

// BridgedFacet is a Go core facet whose local types plugins on a Node host may use: the Node host loads a proxy facet named
// Name that serves Contracts, and its objects forward to Host's own (RuntimeLoad.Bridged).
type BridgedFacet struct {
	// Name is the Go core facet's name: its plugin name on the Go host, and the proxy's on the Node host.
	Name string
	// Host is the Go host that loaded the facet: the objects of Contracts live there, and a proxy object holds one by
	// `Host.Get`.
	Host host.Host
	// Contracts are the local types the Go core facet serves that plugins use. A contract with a keyed member is refused: a
	// keyed member is the document handle of its object and cannot be forwarded.
	Contracts []*contracts.Contract
	// Failed is why the Go core facet failed on the Go host, nil when it loaded or reloaded. The proxy is then shipped as a
	// failed facet: the Node host keeps the proxy it has loaded (none at startup) and, by Pi's closure rule, the plugins that
	// need what its new code serves keep their old code.
	Failed error
}

// bridgedWire is the proxy script's argument: the Go core facet's name and its contracts as data.
type bridgedWire struct {
	Name      string          `json:"name"`
	Contracts []*ContractData `json:"contracts"`
}

// validBridged checks the facets and returns their names' set: names are unique, a facet that has not failed has a host and
// contracts that are local and without keyed members.
func validBridged(facets []BridgedFacet) error {
	names := map[string]bool{}
	for _, facet := range facets {
		if facet.Name == "" {
			return errors.New("plugins: a bridged facet needs a name")
		}
		if names[facet.Name] {
			return fmt.Errorf("plugins: bridged facet %q is named twice", facet.Name)
		}
		names[facet.Name] = true
		if facet.Failed != nil {
			continue
		}
		if facet.Host == nil {
			return fmt.Errorf("plugins: bridged facet %q has no Go host", facet.Name)
		}
		for _, contract := range facet.Contracts {
			declared, err := contracts.ContractDataOf(contract)
			if err != nil {
				return fmt.Errorf("plugins: bridged facet %q: %w", facet.Name, err)
			}
			if declared.Kind != contracts.KindLocal {
				return fmt.Errorf("plugins: bridged facet %q: %s is a remote type; only local types are bridged", facet.Name, declared.Name)
			}
			current := declared.Versions[len(declared.Versions)-1].Variants.Value(contracts.LocalVariant)
			if current.Keyed.Len() > 0 {
				return fmt.Errorf("plugins: bridged facet %q: %s has the keyed member %q, which cannot be forwarded", facet.Name, declared.Name, current.Keyed.Keys()[0])
			}
		}
	}
	return nil
}

// bridgedScripts are the proxy scripts of facets for the host of kind: one per facet, shipped with the plugins' scripts. A
// facet that failed is a failed build.
func bridgedScripts(kind string, facets []BridgedFacet) ([]scriptWire, error) {
	scripts := make([]scriptWire, 0, len(facets))
	for _, facet := range facets {
		plugin := pluginWire{Name: facet.Name, Dir: "/", Facets: map[string]string{kind: "/" + kind + ".go"}, Dependencies: []string{}, External: []string{}}
		if facet.Failed != nil {
			scripts = append(scripts, scriptWire{Plugin: plugin, Built: builtWire{
				Kind: BuiltFacetFailed, Plugin: facet.Name, Facet: kind, Errors: []string{facet.Failed.Error()},
			}})
			continue
		}
		spec := bridgedWire{Name: facet.Name, Contracts: make([]*ContractData, len(facet.Contracts))}
		for i, contract := range facet.Contracts {
			wire, err := ContractDataOf(contract)
			if err != nil {
				return nil, err
			}
			spec.Contracts[i] = wire
		}
		encoded, err := jsstring.MarshalJSON(spec)
		if err != nil {
			return nil, err
		}
		source := `(function (require, module, exports) { module.exports = { default: require(` + quoteJS(bridgePackage) + `).proxyFacet(` + string(encoded) + `) }; })`
		sum := sha256.Sum256([]byte(source))
		scripts = append(scripts, scriptWire{Plugin: plugin, Built: builtWire{
			Kind: BuiltFacetScript, Plugin: facet.Name, Facet: kind, Source: source, Digest: hex.EncodeToString(sum[:]),
		}})
	}
	return scripts, nil
}

// quoteJS is text as a JavaScript string literal, as JSON.stringify writes it (it leaves <, > and & as they are).
func quoteJS(text string) string {
	encoded, _ := jsstring.MarshalJSON(text)
	return string(encoded)
}

// SetBridged replaces the Go core facets the next reloads ship as proxies (RuntimeLoad.Bridged is the first set): the app calls
// it when its Go core facets changed or failed on the Go host, then reloads the plugins (Plugins.Reload, PluginRuntime.Reload)
// so the Node host applies the set with the plugins' own, by Pi's closure rule. A facet whose contracts did not change is
// the same proxy (its script's digest is the same). A facet that failed without a Host or Contracts of its own keeps the ones
// it had: the proxy the Node host keeps loaded goes on forwarding to the Go host's old code. It fails for facets that cannot
// be bridged (BridgedFacet).
func (h *NodeHost) SetBridged(facets []BridgedFacet) error {
	if err := validBridged(facets); err != nil {
		return err
	}
	h.bridge.mu.Lock()
	defer h.bridge.mu.Unlock()
	next := slices.Clone(facets)
	for i := range next {
		if next[i].Failed == nil || next[i].Host != nil {
			continue
		}
		for _, previous := range h.bridge.bridged {
			if previous.Name == next[i].Name {
				next[i].Host, next[i].Contracts = previous.Host, previous.Contracts
			}
		}
	}
	h.bridge.bridged = next
	return nil
}

// bridgedScripts are the proxy scripts of the current Go core facets, for a reload.
func (h *NodeHost) bridgedScripts() ([]scriptWire, error) {
	h.bridge.mu.Lock()
	facets := slices.Clone(h.bridge.bridged)
	h.bridge.mu.Unlock()
	return bridgedScripts(h.kind, facets)
}

// ReloadOptions are the plugins this Node host's reload report says failed (their new code did not build or evaluate, or
// the closure kept them old), as the options a Go host's Reload takes: its facets named like those plugins (the Go side's
// own form of what the plugins serve) keep their loaded code too, and the Go host's closure rule keeps every facet that
// needs what their new code would have served old. The Go core facets of RuntimeLoad.Bridged are not in it: they failed
// on the Go host, not on the Node host.
func (h *NodeHost) ReloadOptions(report host.ReloadReport) host.ReloadOptions {
	h.bridge.mu.Lock()
	bridged := h.bridge.bridged
	h.bridge.mu.Unlock()
	var failed []host.PluginFailure
	for _, failure := range report.Failed {
		if !slices.ContainsFunc(bridged, func(facet BridgedFacet) bool { return facet.Name == failure.Plugin }) {
			failed = append(failed, failure)
		}
	}
	return host.ReloadOptions{Failed: failed}
}

// bridgedServing is what the Go side keeps of the Node host's requests to it: the references and watches it holds for the
// proxies, and the requests running, until they end.
type bridgedServing struct {
	mu         sync.Mutex
	life       context.Context
	nextHandle int
	references map[int]*host.Reference
	watches    map[int]chord.WatchHandle[delta.JsonValue]
	running    map[int]context.CancelFunc
}

// serving is the Go side's state for the Node host's requests, made when the first arrives: its context ends with the
// process.
func (h *NodeHost) serving() *bridgedServing {
	h.bridge.mu.Lock()
	defer h.bridge.mu.Unlock()
	if h.bridge.served == nil {
		served := &bridgedServing{
			life: h.life, references: map[int]*host.Reference{}, watches: map[int]chord.WatchHandle[delta.JsonValue]{}, running: map[int]context.CancelFunc{},
		}
		h.bridge.served = served
		//portlint:allow golifetime ends with the process: the context of every request the Node host made ends with it
		go func() {
			<-h.life.Done()
			served.releaseAll()
		}()
	}
	return h.bridge.served
}

// releaseAll ends the holds the proxies had when the process ended, as Pi ends a host's holds when the host closes: each
// Go reference is released and each watch stopped, so a Go host that outlives the Node host keeps nothing for it. The
// context of `life` is already done, so a request still running releases what it acquires after this ran.
func (served *bridgedServing) releaseAll() {
	served.mu.Lock()
	references, watches := served.references, served.watches
	served.references = map[int]*host.Reference{}
	served.watches = map[int]chord.WatchHandle[delta.JsonValue]{}
	served.mu.Unlock()
	for _, watch := range watches {
		_, _ = watch.Stop()
	}
	for _, reference := range references {
		reference.Release()
	}
}

// bridgedEvent handles an event of the Node host that is a request to the Go host (`get`, `watch`, `bridgeCall`: each
// answered), or a notification of it (`unwatch`, `release`, `abort`). It runs on the goroutine that reads the channel: the
// work of a request runs on a goroutine of its own, so a request never delays another or the reading.
func (h *NodeHost) bridgedEvent(event string, payload []byte) {
	switch event {
	case "get", "watch", "bridgeCall":
		var request struct {
			ID     int             `json:"id"`
			Facet  string          `json:"facet"`
			Type   string          `json:"contract"`
			Params json.RawMessage `json:"params"`
			Handle int             `json:"handle"`
			Member string          `json:"member"`
			Method string          `json:"method"`
			Args   json.RawMessage `json:"args"`
			Watch  int             `json:"watch"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			return
		}
		serving := h.serving()
		ctx, cancel := context.WithCancel(serving.life)
		serving.mu.Lock()
		serving.running[request.ID] = cancel
		serving.mu.Unlock()
		//portlint:allow golifetime ends with its request: its context ends with the request's abort or the process
		go func() {
			defer func() {
				serving.mu.Lock()
				delete(serving.running, request.ID)
				serving.mu.Unlock()
				cancel()
			}()
			var result any
			var err error
			var start func()
			switch event {
			case "get":
				result, err = h.bridgedGet(ctx, request.Facet, request.Type, request.Params)
			case "watch":
				result, start, err = h.bridgedWatch(ctx, request.Handle, request.Member, request.Watch)
			case "bridgeCall":
				result, err = h.bridgedCall(ctx, request.Handle, request.Method, request.Args)
			}
			h.answerBridged(request.ID, result, err)
			if start != nil {
				start()
			}
		}()
	case "abort":
		var message struct {
			Target int `json:"target"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			return
		}
		serving := h.serving()
		serving.mu.Lock()
		cancel := serving.running[message.Target]
		serving.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case "release":
		var message struct {
			Handle int `json:"handle"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			return
		}
		serving := h.serving()
		serving.mu.Lock()
		reference := serving.references[message.Handle]
		delete(serving.references, message.Handle)
		serving.mu.Unlock()
		if reference != nil {
			reference.Release()
		}
	case "unwatch":
		var message struct {
			Watch int `json:"watch"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			return
		}
		serving := h.serving()
		serving.mu.Lock()
		watch := serving.watches[message.Watch]
		delete(serving.watches, message.Watch)
		serving.mu.Unlock()
		if watch != nil {
			_, _ = watch.Stop()
		}
	}
}

// answerBridged answers request id of the Node host with result, or with err as data.
func (h *NodeHost) answerBridged(id int, result any, err error) {
	message := map[string]any{"op": "bridgeAnswer", "id": id, "ok": err == nil}
	if err != nil {
		message["error"] = errorData(err)
	} else {
		message["result"] = result
	}
	if port := h.port.Load(); port != nil {
		_ = port.control(message)
	}
}

// errorData is err as the Node host's proxies read it: a ChordError keeps its wire form, anything else is its message.
func errorData(err error) map[string]any {
	if chord, ok := errors.AsType[*data.ChordError](err); ok {
		return map[string]any{"name": "ChordError", "message": chord.Message, "chord": chord.ToJSON()}
	}
	return map[string]any{"name": "Error", "message": err.Error()}
}

// bridgedGet holds the Go object of type name of Go core facet facet for params: `Host.Get`, so it waits while the object is
// not live as a Go host's own getter does. The reference is kept under a handle the proxy releases.
func (h *NodeHost) bridgedGet(ctx context.Context, facet, name string, params json.RawMessage) (any, error) {
	h.bridge.mu.Lock()
	var found *BridgedFacet
	for i := range h.bridge.bridged {
		if h.bridge.bridged[i].Name == facet {
			found = &h.bridge.bridged[i]
		}
	}
	var contract *contracts.Contract
	if found != nil {
		for _, each := range found.Contracts {
			if each.Name == name {
				contract = each
			}
		}
	}
	var goHost host.Host
	if found != nil {
		goHost = found.Host
	}
	h.bridge.mu.Unlock()
	if contract == nil || goHost == nil {
		return nil, fmt.Errorf("%s: no Go core facet %q bridges it", name, facet)
	}
	decoded, err := delta.DecodeJson(params)
	if err != nil {
		return nil, err
	}
	reference, err := goHost.Get(ctx, contract, decoded, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	serving := h.serving()
	serving.mu.Lock()
	serving.nextHandle++
	handle := serving.nextHandle
	serving.references[handle] = reference
	serving.mu.Unlock()
	if ctx.Err() != nil {
		serving.mu.Lock()
		delete(serving.references, handle)
		serving.mu.Unlock()
		reference.Release()
		return nil, context.Cause(ctx)
	}
	return map[string]any{"handle": handle}, nil
}

func (h *NodeHost) bridgedReference(handle int) (*host.Reference, error) {
	serving := h.serving()
	serving.mu.Lock()
	defer serving.mu.Unlock()
	if reference := serving.references[handle]; reference != nil {
		return reference, nil
	}
	return nil, fmt.Errorf("no Go reference %d is held", handle)
}

func (h *NodeHost) bridgedMember(handle int, name string) (*host.ReferenceMember, error) {
	reference, err := h.bridgedReference(handle)
	if err != nil {
		return nil, err
	}
	member := reference.Member(name)
	if member == nil {
		return nil, fmt.Errorf("%s: not a state member of the reference", name)
	}
	return member, nil
}

// bridgedWatch watches a state member of a held Go reference: the answer is its value, then its frames and its end are sent
// as `frame` and `watchEnd` notifications under id (chosen by the proxy). The watch lives with the reference, until `unwatch`
// or the process's end; the request's own context only bounds the acquisition. It returns the function that starts the
// frames, which the caller runs once the answer is out, so the frames follow it on the channel.
func (h *NodeHost) bridgedWatch(ctx context.Context, handle int, name string, id int) (result any, start func(), err error) {
	member, err := h.bridgedMember(handle, name)
	if err != nil {
		return nil, nil, err
	}
	serving := h.serving()
	watch, err := member.Watch(serving.life)
	if err != nil {
		return nil, nil, err
	}
	if ctx.Err() != nil {
		_, _ = watch.Stop()
		return nil, nil, context.Cause(ctx)
	}
	serving.mu.Lock()
	serving.watches[id] = watch
	serving.mu.Unlock()
	return map[string]any{"value": wireEncode(watch.Value())}, func() { h.startBridgedWatch(id, watch) }, nil
}

// startBridgedWatch sends the frames of watch and its end.
func (h *NodeHost) startBridgedWatch(id int, watch chord.WatchHandle[delta.JsonValue]) {
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		wire := map[string]any{"kind": string(call.Kind)}
		if call.Kind == chord.WatchCallFailed {
			wire["error"] = errorData(call.Error)
		} else {
			ops := make([]any, len(call.Ops))
			for i, op := range call.Ops {
				ops[i] = op
			}
			wire["value"], wire["ops"], wire["retired"] = wireEncode(call.Value), wireEncode(ops), call.Retired
		}
		if port := h.port.Load(); port != nil {
			return port.control(map[string]any{"op": "frame", "watch": id, "call": wire})
		}
		return errors.New("the node subprocess's channel is gone")
	})
	//portlint:allow golifetime ends when the watch does: stopped, ended by its source or by the process
	go func() {
		<-watch.Closed()
		serving := h.serving()
		serving.mu.Lock()
		_, held := serving.watches[id]
		delete(serving.watches, id)
		serving.mu.Unlock()
		if !held {
			// Stopped by the proxy's own `unwatch`: nothing is left to tell it.
			return
		}
		end := watch.End()
		wire := map[string]any{"kind": string(end.Kind)}
		if end.Error != nil {
			wire["error"] = errorData(end.Error)
		}
		if port := h.port.Load(); port != nil {
			_ = port.control(map[string]any{"op": "watchEnd", "watch": id, "end": wire})
		}
	}()
}

// bridgedCall calls a method of a held Go reference.
func (h *NodeHost) bridgedCall(ctx context.Context, handle int, method string, args json.RawMessage) (any, error) {
	reference, err := h.bridgedReference(handle)
	if err != nil {
		return nil, err
	}
	decoded, err := unmarshalWire(args)
	if err != nil {
		return nil, err
	}
	result, err := reference.Call(method, decoded, ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"result": wireEncode(result)}, nil
}
