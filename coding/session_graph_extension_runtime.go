// pig-specific: serves Pi's createExtensionRuntime() result (packages/coding-agent/src/core/extensions/loader.ts) to the Go, Python and Rust extension SDKs as a host-owned object an extension names by handle. The object is the runtime's own *extension.ExtensionRuntime: a DefaultResourceLoader given the handle loads its extensions against it, and an ExtensionRunner over those extensions binds its actions into it. The Node runtime serves Pi's own function.

package coding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// GraphExtensionRuntime is an ExtensionRuntime an extension created, or the runtime a loader's extensions share.
type GraphExtensionRuntime struct {
	runtime *extension.ExtensionRuntime
}

// Runtime is the runtime the handle names.
func (g *GraphExtensionRuntime) Runtime() *extension.ExtensionRuntime { return g.runtime }

var _ subprocess.SessionGraphObject = (*GraphExtensionRuntime)(nil)

// newGraphExtensionRuntime builds the object of the static factory op: "create" is `createExtensionRuntime()`; "of" is the runtime of the extensions a DefaultResourceLoader loaded (`loader.getExtensions().runtime`), whose handle is the first argument.
func newGraphExtensionRuntime(scope subprocess.SessionGraphScope, op string, raw json.RawMessage) (*GraphExtensionRuntime, error) {
	switch op {
	case "create":
		return &GraphExtensionRuntime{runtime: extension.CreateExtensionRuntime()}, nil
	case "of":
		args, err := icodingagent.ParseGraphArgs(raw)
		if err != nil {
			return nil, err
		}
		handle, err := args.String(0)
		if err != nil {
			return nil, err
		}
		object, ok := scope.Object(handle)
		loader, isLoader := object.(*GraphResourceLoader)
		if !ok || !isLoader {
			return nil, errors.New(subprocess.SessionGraphUnknownMessage)
		}
		runtime := loader.loader.GetExtensions().Runtime
		if runtime == nil {
			return nil, errors.New("the loader has loaded no extensions yet: call reload() first")
		}
		return &GraphExtensionRuntime{runtime: runtime}, nil
	}
	return nil, fmt.Errorf("ExtensionRuntime has no static factory %q", op)
}

// graphFlagValues is the runtime's flag values as a JSON object in name order; a JavaScript Map holds them in insertion order, which the runtime does not keep.
func graphFlagValues(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for _, name := range slices.Sorted(maps.Keys(values)) {
		out[name] = values[name]
	}
	return out
}

// Call runs a method of the runtime's data members: flagValues, the pending registration queues, mcpServers, and the stale state.
func (g *GraphExtensionRuntime) Call(_ context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	runtime := g.runtime
	switch method {
	case "getFlagValues":
		return graphFlagValues(runtime.FlagValuesSnapshot()), nil
	case "setFlagValue":
		name, err := args.String(0)
		if err != nil {
			return nil, err
		}
		value, err := graphFlagValueArg(args, 1)
		if err != nil {
			return nil, err
		}
		runtime.SetFlagValue(name, value)
		return nil, nil
	case "getPendingProviderRegistrations":
		out := []map[string]any{}
		for _, pending := range runtime.PendingProviderRegistrations() {
			out = append(out, map[string]any{"name": pending.Name, "config": pending.Config, "extensionPath": pending.ExtensionPath})
		}
		return out, nil
	case "getPendingNativeProviderRegistrations":
		out := []map[string]any{}
		for _, pending := range runtime.PendingNativeProviderRegistrations() {
			out = append(out, map[string]any{"provider": map[string]any{"id": pending.Provider.ID, "name": pending.Provider.Name}, "extensionPath": pending.ExtensionPath})
		}
		return out, nil
	case "getPendingVirtualModelRegistrations":
		out := []map[string]any{}
		for _, pending := range runtime.PendingVirtualModelRegistrations() {
			definition := pending.Definition
			out = append(out, map[string]any{"definition": map[string]any{
				"provider": definition.Provider, "id": definition.ID, "name": definition.Name, "thinkingLevels": definition.ThinkingLevels,
				"contextWindow": definition.ContextWindow, "maxTokens": definition.MaxTokens, "input": definition.Input,
			}, "extensionPath": pending.ExtensionPath})
		}
		return out, nil
	case "getMcpServers":
		return runtime.McpServers().List(), nil
	case "assertActive":
		return nil, runtime.AssertActive()
	case "invalidate":
		message, err := args.OptString(0)
		if err != nil {
			return nil, err
		}
		if message == nil {
			runtime.Invalidate()
		} else {
			runtime.Invalidate(*message)
		}
		return nil, nil
	}
	return nil, errors.New("ExtensionRuntime has no method " + method)
}

// graphFlagValueArg is argument i as a flag value: a boolean or a string (a JavaScript Map<string, boolean | string>).
func graphFlagValueArg(args icodingagent.GraphArgs, i int) (any, error) {
	var text string
	var flag bool
	var decoded any
	err := args.Unmarshal(i, func(raw json.RawMessage) error {
		switch {
		case string(raw) == "null":
		case json.Unmarshal(raw, &text) == nil:
			decoded = text
		case json.Unmarshal(raw, &flag) == nil:
			decoded = flag
		default:
			return errors.New("a flag value is a boolean or a string")
		}
		return nil
	})
	return decoded, err
}
