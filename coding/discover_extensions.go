package coding

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/nodefs"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// DiscoverAndLoadExtensions discovers extensions in the standard locations and loads them: the project's `<cwd>/<config dir>/extensions` (`.pi` in Pi), the global `<agentDir>/extensions`, then each configured path, deduplicated by absolute path in that order. An empty agentDir is the agent directory of the process. Load failures are in the result's Errors, as in Pi.
//
// Ports packages/coding-agent/src/core/extensions/loader.ts discoverAndLoadExtensions (:828-875), discoverExtensionsInDir and resolveExtensionEntries. Pi imports each file into its own process; PiG loads it in the Extension Host (D19).
//
// pig divergence (D117): Pi's optional event bus parameter does not exist; the Extension Host owns the routing of pi.events and a caller cannot supply a bus.
func DiscoverAndLoadExtensions(ctx context.Context, configuredPaths []string, cwd, agentDir string) (LoadExtensionsResult, error) {
	allPaths, resolvedCwd, err := discoverExtensionPaths(configuredPaths, cwd, agentDir)
	if err != nil {
		return LoadExtensionsResult{}, err
	}
	return loadExtensionPaths(ctx, allPaths, resolvedCwd), nil
}

// discoverExtensionPaths is the discovery half of DiscoverAndLoadExtensions: the extension paths in load order and the resolved cwd.
func discoverExtensionPaths(configuredPaths []string, cwd, agentDir string) ([]string, string, error) {
	resolvedCwd, err := icodingagent.ResolvePath(cwd, "")
	if err != nil {
		return nil, "", err
	}
	if agentDir == "" {
		agentDir = icodingagent.AgentDir()
	}
	resolvedAgentDir, err := icodingagent.ResolvePath(agentDir, "")
	if err != nil {
		return nil, "", err
	}
	var allPaths []string
	seen := map[string]bool{}
	addPaths := func(paths []string) {
		for _, p := range paths {
			resolved, err := nodepath.Resolve(p)
			if err != nil {
				resolved = p
			}
			if !seen[resolved] {
				seen[resolved] = true
				allPaths = append(allPaths, p)
			}
		}
	}
	addPaths(discoverExtensionsInDir(filepath.Join(resolvedCwd, icodingagent.ConfigDirName(), "extensions")))
	addPaths(discoverExtensionsInDir(filepath.Join(resolvedAgentDir, "extensions")))
	for _, p := range configuredPaths {
		resolved, err := icodingagent.ResolvePath(resolvepath.NormalizeUnicodeSpaces(p), resolvedCwd)
		if err != nil {
			return nil, "", err
		}
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			if entries := resolveExtensionEntries(resolved); entries != nil {
				addPaths(entries)
				continue
			}
			addPaths(discoverExtensionsInDir(resolved))
			continue
		}
		addPaths([]string{resolved})
	}
	return allPaths, resolvedCwd, nil
}

// loadExtensionPaths loads each path in a new Extension Host that the result owns. The result's runtime is the Host's: the loaded extensions queue their provider and virtual model registrations on it until a runner binds, as Pi's loadExtensionsInternal returns the runtime it loaded them into (loader.ts:689,710-714). A load of no paths gets a fresh runtime, as in Pi.
func loadExtensionPaths(ctx context.Context, paths []string, cwd string) LoadExtensionsResult {
	return loadExtensionPathsIn(ctx, paths, cwd, nil)
}

// loadExtensionPathsIn is loadExtensionPaths with the Host registering in runtime (nil: its own), as resource-loader.ts loadExtensionPaths takes the runtime parameter.
func loadExtensionPathsIn(ctx context.Context, paths []string, cwd string, runtime *extension.ExtensionRuntime) LoadExtensionsResult {
	configs := make([]subprocess.ExtConfig, 0, len(paths))
	for _, path := range paths {
		configs = append(configs, extensionPathConfigs(path)...)
	}
	if runtime == nil {
		runtime = extension.CreateExtensionRuntime()
	}
	result := LoadExtensionsResult{Runtime: runtime}
	if len(configs) == 0 {
		return result
	}
	host := subprocess.NewHostWithConfigRoot(cwd, icodingagent.ConfigRoot())
	host.UseRuntime(runtime)
	// A headless bridge: the loaded extensions' contexts read the Session state a Session binds to it (agent-session.ts _bindExtensionCore), and with no UI bound hasUI is false.
	bridge := subprocess.NewUIBridge(func() {})
	// The load-time pi-ai calls a module makes run through the global API providers until a Session binds its own model surface, as they do on a host with no bridge.
	icodingagent.WireEarlyModelStream(bridge, "")
	host.SetUIBridge(bridge)
	result.bridge = bridge
	host.SetMode(string(extension.ModePrint))
	host.Builder().SetStagedSDKVerifier(subprocess.DefaultStagedSDKVerifier(icodingagent.ConfigRoot()))
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return append([]subprocess.ExtConfig(nil), configs...), nil })
	loaded, errs := host.LoadAll(ctx, configs)
	result.Extensions = loaded
	for _, err := range errs {
		if loadErr, ok := errors.AsType[*subprocess.ExtensionLoadError](err); ok && loadErr.Path != "" {
			result.Errors = append(result.Errors, ExtensionLoadError{Path: loadErr.Path, Error: loadErr.Err.Error()})
			continue
		}
		result.Errors = append(result.Errors, ExtensionLoadError{Error: err.Error()})
	}
	result.Runtime = host.Runtime()
	result.host = host
	return result
}

// extensionPathConfigs is the host configuration of one discovered path.
func extensionPathConfigs(path string) []subprocess.ExtConfig {
	config, _, err := subprocess.ResolveExtConfigWithResolver(path, "", packagemanager.ConfiguredExtensionResolver(nil))
	if err == nil {
		return []subprocess.ExtConfig{config}
	}
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return []subprocess.ExtConfig{subprocess.UnresolvedExtConfig(path, err)}
	}
	return []subprocess.ExtConfig{{Name: filepath.Base(path), Enabled: true, Path: path}}
}

// resolveExtensionEntries is the entry points of a directory: the paths a package.json `pi.extensions` field declares that exist, else index.ts, else index.js; nil when there is none. The manifest is read by readPiManifest's port, so a byte-order mark is stripped and a field that is not an array of strings is ignored.
func resolveExtensionEntries(dir string) []string {
	if manifest := packagecontent.ReadPiManifest(filepath.Join(dir, "package.json")); manifest != nil && len(manifest.Extensions) > 0 {
		var entries []string
		for _, declared := range manifest.Extensions {
			resolved, err := nodepath.Resolve(dir, declared)
			if err != nil {
				continue
			}
			if _, err := os.Stat(resolved); err == nil {
				entries = append(entries, resolved)
			}
		}
		if len(entries) > 0 {
			return entries
		}
	}
	for _, name := range []string{"index.ts", "index.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return []string{filepath.Join(dir, name)}
		}
	}
	return nil
}

// discoverExtensionsInDir lists the extensions of a directory, one level deep: its .ts and .js files and symbolic links, and the entry points of each subdirectory or symbolic link. A directory that cannot be read has none. Like Pi it classifies an entry by its directory entry type, so a dangling link named *.ts is discovered and its load reports the error.
func discoverExtensionsInDir(dir string) []string {
	entries, err := nodefs.ReadDir(dir)
	if err != nil {
		return nil
	}
	var discovered []string
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		symlink := entry.Type()&fs.ModeSymlink != 0
		if (entry.Type().IsRegular() || symlink) && (strings.HasSuffix(entry.Name(), ".ts") || strings.HasSuffix(entry.Name(), ".js")) {
			discovered = append(discovered, entryPath)
			continue
		}
		if entry.IsDir() || symlink {
			discovered = append(discovered, resolveExtensionEntries(entryPath)...)
		}
	}
	return discovered
}
