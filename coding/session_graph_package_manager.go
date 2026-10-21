// pig-specific: serves Pi's DefaultPackageManager (packages/coding-agent/src/core/package-manager.ts) to the Go, Python and Rust extension SDKs as a host-owned object an extension names by handle. The object is the runtime's own package manager over the settings object it was given. The Node runtime serves Pi's own class in the extension process.

package coding

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// GraphPackageManager is a DefaultPackageManager an extension constructed.
type GraphPackageManager struct {
	manager *packagemanager.PackageManager
	scope   subprocess.SessionGraphScope
}

func newGraphPackageManager(scope subprocess.SessionGraphScope, op string, raw json.RawMessage) (*GraphPackageManager, error) {
	if op != "new" {
		return nil, fmt.Errorf("DefaultPackageManager has no static factory %q", op)
	}
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	var options struct {
		Cwd               string   `json:"cwd"`
		AgentDir          string   `json:"agentDir"`
		SettingsManager   string   `json:"settingsManager"`
		BuiltinExtensions []string `json:"builtinExtensions"`
	}
	if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
		return nil, err
	}
	object, ok := scope.Object(options.SettingsManager)
	settings, isSettings := object.(*icodingagent.GraphSettingsManager)
	if !ok || !isSettings {
		return nil, errors.New(subprocess.SessionGraphUnknownMessage)
	}
	cwd, err := icodingagent.ResolvePath(options.Cwd, "")
	if err != nil {
		return nil, err
	}
	agentDir, err := icodingagent.ResolvePath(options.AgentDir, "")
	if err != nil {
		return nil, err
	}
	return &GraphPackageManager{manager: packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: settings.Manager(), BuiltinExtensions: options.BuiltinExtensions}), scope: scope}, nil
}

func graphResolved(resources []icodingagent.ResolvedResource) []map[string]any {
	out := make([]map[string]any, 0, len(resources))
	for _, resource := range resources {
		metadata := map[string]any{"source": resource.Metadata.Source, "scope": resource.Metadata.Scope, "origin": resource.Metadata.Origin}
		if resource.Metadata.BaseDir != "" {
			metadata["baseDir"] = resource.Metadata.BaseDir
		}
		out = append(out, map[string]any{"path": resource.Path, "enabled": resource.Enabled, "metadata": metadata})
	}
	return out
}

func graphResolvedPaths(paths packagemanager.ResolvedPaths) map[string]any {
	return map[string]any{"extensions": graphResolved(paths.Extensions), "skills": graphResolved(paths.Skills), "prompts": graphResolved(paths.Prompts), "themes": graphResolved(paths.Themes)}
}

// Call runs a DefaultPackageManager method.
func (g *GraphPackageManager) Call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	manager := g.manager
	local := func(i int) (bool, error) {
		var options struct {
			Local bool `json:"local"`
		}
		return options.Local, args.Unmarshal(i, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) })
	}
	switch method {
	case "setProgressCallback":
		id, err := args.OptString(0)
		if err != nil {
			return nil, err
		}
		if id == nil {
			manager.SetProgressCallback(nil)
			return nil, nil
		}
		manager.SetProgressCallback(func(event packagemanager.ProgressEvent) {
			// Pi's ProgressEvent members in its order: type, action, source, message.
			wire := struct {
				Type    string  `json:"type"`
				Action  string  `json:"action"`
				Source  string  `json:"source"`
				Message *string `json:"message,omitempty"`
			}{event.Type, event.Action, event.Source, event.Message}
			encoded, err := jsstring.AppendMarshalJSON([]byte{'['}, wire)
			if err != nil {
				return
			}
			// The callback outlives the call that set it: it reports to the extension for as long as the connection lives.
			//portlint:allow ctxbackground the progress callback is Pi's setProgressCallback: it reports for as long as the connection lives, with no call to cancel it
			_, _ = g.scope.Callback(context.Background(), *id, json.RawMessage(append(encoded, ']')))
		})
		return nil, nil
	case "addSourceToSettings", "removeSourceFromSettings":
		source, err := args.String(0)
		if err != nil {
			return nil, err
		}
		isLocal, err := local(1)
		if err != nil {
			return nil, err
		}
		if method == "addSourceToSettings" {
			return manager.AddSourceToSettings(source, isLocal)
		}
		return manager.RemoveSourceFromSettings(source, isLocal)
	case "getInstalledPath":
		source, err := args.String(0)
		if err != nil {
			return nil, err
		}
		scope, err := args.String(1)
		if err != nil {
			return nil, err
		}
		if path := manager.GetInstalledPath(source, scope); path != "" {
			return path, nil
		}
		return nil, nil
	case "resolve":
		id, err := args.OptString(0)
		if err != nil {
			return nil, err
		}
		var onMissing packagemanager.MissingSourceCallback
		if id != nil {
			onMissing = func(source string) (packagemanager.MissingSourceAction, error) {
				encoded, err := jsstring.AppendMarshalJSON([]byte{'['}, source)
				if err != nil {
					return "", err
				}
				result, err := g.scope.Callback(ctx, *id, json.RawMessage(append(encoded, ']')))
				if err != nil {
					return "", err
				}
				var action string
				if err := json.Unmarshal(result, &action); err != nil {
					return "", fmt.Errorf("onMissing returned %s, not an action", result)
				}
				return packagemanager.MissingSourceAction(action), nil
			}
		}
		paths, err := manager.Resolve(onMissing)
		if err != nil {
			return nil, err
		}
		return graphResolvedPaths(paths), nil
	case "resolveExtensionSources":
		var sources []string
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &sources) }); err != nil {
			return nil, err
		}
		var options struct {
			Local     bool `json:"local"`
			Temporary bool `json:"temporary"`
		}
		if err := args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		paths, err := manager.ResolveExtensionSources(sources, options.Local, options.Temporary)
		if err != nil {
			return nil, err
		}
		return graphResolvedPaths(paths), nil
	case "listConfiguredPackages":
		out := []map[string]any{}
		for _, pkg := range manager.ListConfiguredPackages() {
			entry := map[string]any{"source": pkg.Source.Source, "scope": pkg.Scope, "filtered": pkg.Source.WasObject}
			if pkg.InstalledPath != "" {
				entry["installedPath"] = pkg.InstalledPath
			}
			out = append(out, entry)
		}
		return out, nil
	case "install", "installAndPersist", "remove":
		source, err := args.String(0)
		if err != nil {
			return nil, err
		}
		isLocal, err := local(1)
		if err != nil {
			return nil, err
		}
		switch method {
		case "install":
			return nil, manager.Install(source, isLocal)
		case "installAndPersist":
			return nil, manager.InstallAndPersist(source, isLocal)
		}
		return nil, manager.Remove(source, isLocal)
	case "removeAndPersist":
		source, err := args.String(0)
		if err != nil {
			return nil, err
		}
		isLocal, err := local(1)
		if err != nil {
			return nil, err
		}
		return manager.RemoveAndPersist(source, isLocal)
	case "update":
		source, err := args.OptString(0)
		if err != nil {
			return nil, err
		}
		if source == nil {
			return nil, manager.Update("")
		}
		return nil, manager.Update(*source)
	case "checkForAvailableUpdates":
		out := []map[string]any{}
		for _, update := range manager.CheckForAvailableUpdates() {
			out = append(out, map[string]any{"source": update.Source, "displayName": update.DisplayName, "type": update.Type, "scope": update.Scope})
		}
		return out, nil
	}
	return nil, errors.New("DefaultPackageManager has no method " + method)
}

var _ subprocess.SessionGraphObject = (*GraphPackageManager)(nil)
