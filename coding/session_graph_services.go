// pig-specific: serves the two steps of Pi's createAgentSessionServices (packages/coding-agent/src/core/agent-session-services.ts) that need the host to the Go, Python and Rust extension SDKs. The SDK builds the services' objects as Pi's function does, and these functions resolve the paths and apply what the loaded extensions queued.

package coding

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

func graphAgentSessionServicesStatic(ctx context.Context, scope subprocess.SessionGraphScope, method string, raw json.RawMessage) (any, error) {
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	switch method {
	case subprocess.SessionGraphServicesResolve:
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		agentDir, err := args.OptString(1)
		if err != nil {
			return nil, err
		}
		return graphServicesResolve(cwd, agentDir)
	case subprocess.SessionGraphServicesApply:
		return graphServicesApply(ctx, scope, args)
	}
	return nil, fmt.Errorf("AgentSessionServices has no static function %q", method)
}

// graphServicesResolve is agent-session-services.ts:138-139. A cwd Pi's resolvePath cannot resolve is the call's error, as the thrown error is Pi's.
func graphServicesResolve(cwd string, agentDir *string) (any, error) {
	resolvedCwd, err := resolvepath.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	// `options.agentDir ? resolvePath(options.agentDir) : getAgentDir()`: an empty string is falsy.
	resolvedAgentDir := icodingagent.AgentDir()
	if agentDir != nil && *agentDir != "" {
		if resolvedAgentDir, err = resolvepath.Resolve(*agentDir, ""); err != nil {
			return nil, err
		}
	}
	return map[string]any{"cwd": resolvedCwd, "agentDir": resolvedAgentDir}, nil
}

// graphServicesApply runs the end of createAgentSessionServices over the objects the extension built. The result is the diagnostics, which are the services' own: Pi appends them to the same list the registrations' errors go to.
func graphServicesApply(ctx context.Context, scope subprocess.SessionGraphScope, args icodingagent.GraphArgs) (any, error) {
	var objects [3]subprocess.SessionGraphObject
	for i := range objects {
		handle, err := args.String(i)
		if err != nil {
			return nil, err
		}
		object, ok := scope.Object(handle)
		if !ok {
			return nil, errors.New(subprocess.SessionGraphUnknownMessage)
		}
		objects[i] = object
	}
	runtime, isRuntime := objects[0].(*GraphModelRuntime)
	settings, isSettings := objects[1].(*icodingagent.GraphSettingsManager)
	loader, isLoader := objects[2].(*GraphResourceLoader)
	if !isRuntime || !isSettings || !isLoader {
		return nil, errors.New("AgentSessionServices.apply: the handles are not a ModelRuntime, a SettingsManager and a DefaultResourceLoader")
	}
	cwd, err := args.String(3)
	if err != nil {
		return nil, err
	}
	agentDir, err := args.String(4)
	if err != nil {
		return nil, err
	}
	var flags []ExtensionFlagValue
	if raw := args.Raw(5); raw != nil {
		var pairs [][2]json.RawMessage
		if err := json.Unmarshal(raw, &pairs); err != nil {
			return nil, fmt.Errorf("AgentSessionServices.apply: extension flag values: %w", err)
		}
		for _, pair := range pairs {
			var name string
			if json.Unmarshal(pair[0], &name) != nil {
				return nil, errors.New("AgentSessionServices.apply: an extension flag value is a [name, true|string] pair")
			}
			// The value is a boolean or a string (a JavaScript Map<string, boolean | string>).
			var value any
			var text string
			var flag bool
			switch {
			case json.Unmarshal(pair[1], &text) == nil:
				value = text
			case json.Unmarshal(pair[1], &flag) == nil:
				value = flag
			default:
				return nil, errors.New("AgentSessionServices.apply: an extension flag value is a [name, true|string] pair")
			}
			flags = append(flags, ExtensionFlagValue{Name: name, Value: value})
		}
	}
	// The objects stay the extension's (CreateAgentSessionServices closes neither a shared model runtime nor a shared loader), and the services built here are only the view that applies the load.
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: cwd, AgentDir: agentDir, SettingsManager: settings.Manager(), ModelRuntime: runtime.runtime, ResourceLoader: loader.Loader(),
		ExtensionFlagValues: flags, ModelRuntimeSignal: ctx,
	})
	if err != nil {
		return nil, err
	}
	diagnostics := services.Diagnostics()
	out := make([]map[string]any, len(diagnostics))
	for i, diagnostic := range diagnostics {
		out[i] = map[string]any{"type": diagnostic.Type, "message": diagnostic.Message}
	}
	return out, nil
}
