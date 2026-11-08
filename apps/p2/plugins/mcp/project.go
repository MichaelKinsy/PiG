package mcp

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
)

// Ports apps/p2/plugins/mcp/project.ts: a project's MCP configuration, `<cwd>/.pi/mcp.json` as a session's worker read it
// at one location (a trust boundary: a repository wrote it, and the agent can edit it). It may add stdio servers, which run
// at that location only, and turn the owner's servers on or off there; everything else is refused and reported.

// ProjectServer is one stdio server a project adds, validated; it runs where the project is.
type ProjectServer struct {
	Name   string
	Config extension.McpServerConfig
}

// EnabledOverrides are a project's choices of `enabled` per owner server, in the file's order.
type EnabledOverrides struct {
	names  []string
	values map[string]bool
}

// Names are the overridden servers, in the file's order.
func (e EnabledOverrides) Names() []string { return e.names }

// Get is the project's choice for server name.
func (e EnabledOverrides) Get(name string) (enabled, set bool) {
	enabled, set = e.values[name]
	return
}

func (e *EnabledOverrides) set(name string, enabled bool) {
	if e.values == nil {
		e.values = map[string]bool{}
	}
	if _, seen := e.values[name]; !seen {
		e.names = append(e.names, name)
	}
	e.values[name] = enabled
}

// ProjectConfig is a project's own servers, its choice of `enabled` per owner server, and why entries were refused.
type ProjectConfig struct {
	Servers []ProjectServer
	Enabled EnabledOverrides
	Errors  []string
}

// ParseProjectConfig is the project configuration in text, checked against owner's servers:
//
//   - an entry without `command`, `url` or `type` overrides only `enabled` of the owner's server of that name (as in pi);
//   - an entry defining a server the owner defines is refused (the owner's entry wins);
//   - stdio servers only, without `runOn` and `shared`: a project's server runs at its location, where the agent already
//     runs commands, never on the p2 host or another machine for a repository's sake;
//   - no `auth` or `oauth`: a repository does not choose where a credential goes;
//   - `${NAME}` is never expanded: `env` values reach the program as written, in the location's own environment.
func ParseProjectConfig(text string, owner OwnerConfig) ProjectConfig {
	entries, err := ServerEntries(text, ".pi/mcp.json")
	if err != nil {
		return ProjectConfig{Errors: []string{err.Error()}}
	}
	config := ProjectConfig{}
	namespaces := map[string]string{}
	for _, server := range owner.Servers {
		namespaces[pimcp.McpNamespace(server.Name)] = server.Name
	}
	for _, name := range entries.Keys() {
		raw, _ := entries.Get(name)
		entry := projectEntry(name, raw, owner)
		switch entry.kind {
		case entryInvalid:
			config.Errors = append(config.Errors, ".pi/mcp.json: "+entry.problem)
		case entryOverride:
			if entry.hasEnabled {
				config.Enabled.set(name, entry.enabled)
			}
		}
		if entry.kind != entryServer {
			continue
		}
		if taken, ok := namespaces[pimcp.McpNamespace(name)]; ok {
			config.Errors = append(config.Errors, fmt.Sprintf(`.pi/mcp.json: server "%s": its tools would be named like those of "%s"`, name, taken))
			continue
		}
		namespaces[pimcp.McpNamespace(name)] = name
		config.Servers = append(config.Servers, entry.server)
	}
	return config
}

type entryKind int

const (
	entryServer entryKind = iota
	entryOverride
	entryDisabled
	entryInvalid
)

// projectEntryResult is one entry of a project's configuration: a server, an override, disabled, or why it is refused.
type projectEntryResult struct {
	kind       entryKind
	server     ProjectServer
	enabled    bool
	hasEnabled bool
	problem    string
}

// ownerOnly are the owner's fields a project may not set, and why.
var ownerOnly = [][2]string{
	{"runOn", "a project's server runs where the project is"},
	{"shared", "a project's server runs for its session alone"},
	{"auth", "credentials are for the owner's mcp.json"},
	{"oauth", "credentials are for the owner's mcp.json"},
}

// projectEntry is one entry checked against the rules of ParseProjectConfig.
func projectEntry(name string, raw json.RawMessage, owner OwnerConfig) projectEntryResult {
	refused := func(why string) projectEntryResult {
		return projectEntryResult{kind: entryInvalid, problem: fmt.Sprintf(`server "%s": %s`, name, why)}
	}
	defined := slices.ContainsFunc(owner.Servers, func(server OwnerServer) bool { return server.Name == name })
	if orderedjson.IsObject(raw) {
		object, _ := orderedjson.Parse(raw)
		if pimcp.IsMcpServerOverride(object) {
			if !defined {
				return refused(`needs "command", or a server of the owner's mcp.json to override`)
			}
			if slices.ContainsFunc(object.Keys(), func(key string) bool { return key != "enabled" }) {
				return refused(`an override can only set "enabled"`)
			}
			value, has := object.Get("enabled")
			if has && string(value) != "true" && string(value) != "false" {
				return refused("enabled must be a boolean")
			}
			return projectEntryResult{kind: entryOverride, enabled: string(value) == "true", hasEnabled: has}
		}
	}
	if defined {
		return refused(`the owner's mcp.json defines it; a project can only set its "enabled"`)
	}
	result := pimcp.ValidateMcpServerConfig(name, raw, pimcp.McpServerConfigOptions{})
	if result.Kind == pimcp.McpServerConfigInvalid {
		return projectEntryResult{kind: entryInvalid, problem: result.Error}
	}
	if result.Kind == pimcp.McpServerConfigHTTP {
		return refused("HTTP servers are for the owner's mcp.json (the p2 host would make the requests)")
	}
	for _, field := range ownerOnly {
		if _, set := result.Fields[field[0]]; set {
			return refused(fmt.Sprintf(`"%s" is for the owner's mcp.json: %s`, field[0], field[1]))
		}
	}
	var config extension.McpServerConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return refused(err.Error())
	}
	if config.Enabled != nil && !*config.Enabled {
		return projectEntryResult{kind: entryDisabled}
	}
	return projectEntryResult{kind: entryServer, server: ProjectServer{Name: name, Config: config}}
}
