package subprocess

// Ports packages/coding-agent/src/experimental/plugins/bundled.ts (isolated Node host connection).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// FacetBridgeOptions selects a private facet driver for one isolated Node extension connection. Module is a host-materialized module, not an authored extension manifest. Call receives the existing connection-parented call context, including its initiation marker; asynchronous delegates mark initiation only after their synchronous prefix has applied.
type FacetBridgeOptions struct {
	Directory          string
	TemporaryDirectory string
	Module             string
	Call               func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

// FacetBridge owns a selected Node facet driver through the ordinary extension Host. It never restarts a loaded generation after a process failure or retargets its captured connection to a replacement.
type FacetBridge struct {
	host      *Host
	member    *managedExt
	directory string
	call      func(context.Context, string, json.RawMessage) (json.RawMessage, error)
	close     sync.Once
	closeErr  error
}

// OpenFacetBridge loads a private host-derived factory through the existing Node launcher, socket, registration, heartbeat and cancellation machinery. The context controls startup; the returned loaded-generation connection remains alive until Close so cancelled callers cannot preempt facet teardown. Individual calls retain their own contexts. Closing the bridge joins that same Host before removing its materialized launcher.
func OpenFacetBridge(ctx context.Context, options FacetBridgeOptions) (_ *FacetBridge, err error) {
	module, err := filepath.Abs(options.Module)
	if err != nil {
		return nil, err
	}
	if options.Module == "" {
		return nil, errors.New("facet bridge module is required")
	}
	cwd := options.Directory
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	directory, err := os.MkdirTemp(options.TemporaryDirectory, "pig-facet-")
	if err != nil {
		return nil, err
	}
	bridge := &FacetBridge{directory: directory, call: options.Call}
	bridge.host = NewHostWithConfigRoot(cwd, filepath.Join(directory, "host"))
	defer func() {
		if err != nil {
			err = errors.Join(err, bridge.Close())
		}
	}()
	launcher := filepath.Join(directory, "node")
	entry := filepath.Join(directory, "entry.mjs")
	helper := filepath.Join(launcher+".runtime", "facet-bridge.mjs")
	moduleURL, err := nodeFileURL(module)
	if err != nil {
		return nil, err
	}
	// Native require retains the same private selector module and AsyncLocalStorage as Runtime, rather than a second jiti evaluation of the host implementation.
	source := "import { createRequire } from 'node:module';\n" +
		"const { selectFacetBridge } = createRequire(import.meta.url)(" + strconv.Quote(helper) + ");\n" +
		"export default () => selectFacetBridge(" + strconv.Quote(moduleURL) + ");\n"
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		return nil, err
	}
	if err := buildNode(ctx, filepath.Join(directory, "host", "cache", "ext"), entry, launcher); err != nil {
		return nil, err
	}
	config := ExtConfig{Name: "facet", Path: launcher, Enabled: true, RuntimeLanguage: "node", Isolation: "isolated"}
	supervision := DefaultSupervisorConfig()
	// A captured facet generation cannot be reconstructed by replaying its factory after a crash. The existing supervisor reports and disables it on the first failure.
	supervision.MaxCrashes = 1
	member := &managedExt{config: config, host: bridge.host, supervisor: NewSupervisor(supervision), facetBridge: bridge}
	bridge.member = member
	bridge.host.recordLoadOrder([]ExtConfig{config}, false)
	bridge.host.mu.Lock()
	bridge.host.exts[config.Name] = member
	bridge.host.mu.Unlock()
	if _, err := bridge.host.startExt(context.WithValue(ctx, runtimeParentKey{}, context.WithoutCancel(ctx)), member, false); err != nil {
		return nil, err
	}
	member.livenessOwnerMu.Lock()
	member.releaseLiveness = member.connection().holdLiveness()
	member.livenessOwnerMu.Unlock()
	return bridge, nil
}

// Call awaits an operation owned by the selected facet driver. Args is a private driver payload inside the existing request envelope; absent and null results remain distinguishable to its codec.
func (bridge *FacetBridge) Call(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	return bridge.request(ctx, "facet", args)
}

// CallSync invokes only a synchronous driver callback through the existing restricted Node pump. Nested synchronous host calls use this callback's own pending request identity instead of the ambient blocked caller's lane. Callers run off the TUI input/render loop. The driver must not await a Promise in this dispatcher.
func (bridge *FacetBridge) CallSync(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	return bridge.request(ctx, "facet_sync", args)
}

func (bridge *FacetBridge) request(ctx context.Context, method string, args json.RawMessage) (json.RawMessage, error) {
	var conn *Conn
	if bridge.member != nil {
		conn = bridge.member.connection()
	}
	if conn == nil {
		return nil, errors.New("facet bridge is not connected")
	}
	envelope := &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: method, Args: args}}
	if method == "facet_sync" {
		// Reverse synchronous callbacks need their own pending parent, rather than the ambient outer Node handler's blocked call lane.
		envelope.ID = fmt.Sprintf("facet%d", conn.nextID.Add(1))
		payload, err := json.Marshal(struct {
			RequestID string          `json:"requestId"`
			Args      json.RawMessage `json:"args,omitempty"`
		}{RequestID: envelope.ID, Args: args})
		if err != nil {
			return nil, err
		}
		envelope.Request.Args = payload
	}
	response, err := conn.Request(ctx, envelope)
	if err != nil {
		return nil, err
	}
	if response.Response == nil {
		return nil, errors.New("facet bridge returned no response")
	}
	if response.Response.Error != nil {
		return nil, fmt.Errorf("%s", response.Response.Error.Message)
	}
	return response.Response.Result, nil
}

// Close joins the existing extension shutdown before releasing private files. Concurrent callers observe the same cleanup result.
func (bridge *FacetBridge) Close() error {
	bridge.close.Do(func() {
		bridge.host.Shutdown("facet generation released")
		bridge.closeErr = os.RemoveAll(bridge.directory)
	})
	return bridge.closeErr
}

func (bridge *FacetBridge) handleCall(ctx context.Context, call *CallPayload) (*CallResultPayload, error) {
	if bridge.call == nil {
		return nil, errors.New("facet bridge has no host callback delegate")
	}
	result, err := bridge.call(ctx, call.Method, call.Args)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}
