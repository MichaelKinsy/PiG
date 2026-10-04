package mcpext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Ports packages/coding-agent/src/extensions/mcp/config.ts.
//
// Servers are read from `mcp.json` in the agent directory and, for trusted
// projects, from `<project>/<config dir>/mcp.json`. Both use the `mcpServers`
// shape shared by other MCP clients, so existing configurations can be copied
// over. Project entries replace global entries with the same name.
//
// A project entry without `command`, `url`, or `type` overrides only
// `enabled`, `exposure`, and `toolExposure` of the global server with the same
// name, for example to turn it off in one project:
// `{ "mcpServers": { "internal-tools": { "enabled": false } } }`. The rest of
// the global entry is kept, including credentials the project could not set
// itself.
//
// HTTP servers without an `Authorization` header use OAuth when they answer
// 401 (sign in with `/mcp`). `"auth": { "provider": "<provider>" }` sends the
// token of a `/login` provider instead. Project files cannot use it, so a
// repository cannot pick where the credential goes.
//
// The top-level `autoEnableCodemode` (default true) activates the codemode tool
// when a server with `codemode` exposure connects. A project value overrides
// the global one.

// McpServerEntry is one configured server.
type McpServerEntry struct {
	Name   string
	Config extension.McpServerConfig
	// Source is the config file that defined the entry, or the path of the
	// extension that registered it.
	Source string
	// Scope is "global" or "project" for an `mcp.json`, or "extension" for
	// servers registered with `pi.registerMcpServer()`. Changes to extension
	// servers are not saved.
	Scope string
	// Override is the project `mcp.json` with an override of this global
	// server's `enabled`, `exposure`, or `toolExposure`.
	Override string
}

// LoadedMcpConfig is the result of [LoadMcpConfig].
type LoadedMcpConfig struct {
	Servers []McpServerEntry
	// AutoEnableCodemode activates the codemode tool when `codemode` servers
	// connect. Nil means the default, true.
	AutoEnableCodemode *bool
	Errors             []string
	// ProjectConfig is the project `mcp.json` when the project is trusted,
	// where `/mcp` saves project overrides.
	ProjectConfig string
}

// LoadOptions locate the configuration.
type LoadOptions struct {
	AgentDir string
	Cwd      string
	// ProjectTrusted reads the project's `mcp.json` too. Untrusted projects
	// cannot add or override servers, since stdio servers run commands.
	ProjectTrusted bool
	// ConfigDirName is the per-project configuration directory, `.pi` upstream.
	ConfigDirName string
}

type configState struct {
	names   []string
	servers map[string]McpServerEntry
	// raw is each server's entry as written, the base a project override merges over.
	raw                map[string]*orderedjson.Object
	autoEnableCodemode *bool
	errors             []string
}

func isJSONObject(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '{' }

// overrideKeys are the members a project override can set.
// upstream: packages/coding-agent/src/extensions/mcp/config.ts:OVERRIDE_KEYS
var overrideKeys = []string{"enabled", "exposure", "toolExposure"}

// isOverride reports whether an entry overrides a server defined elsewhere
// instead of defining one: it has no `command`, `url`, or `type` member.
func isOverride(value *orderedjson.Object) bool {
	return !value.Has("command") && !value.Has("url") && !value.Has("type")
}

func (s *configState) readFile(path, scope string) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		s.errors = append(s.errors, fmt.Sprintf("%s: %s", path, nodeReadError(path, err)))
		return
	}
	if !json.Valid(data) {
		message := "Unexpected end of JSON input"
		if err := jsonparse.Validate(data); err != nil {
			message = err.Error()
		}
		s.errors = append(s.errors, fmt.Sprintf("%s: %s", path, message))
		return
	}
	parsed, err := orderedjson.Parse(data)
	if err != nil {
		s.errors = append(s.errors, fmt.Sprintf(`%s: expected an object with an "mcpServers" object`, path))
		return
	}
	servers := (*orderedjson.Object)(nil)
	if raw, ok := parsed.Get("mcpServers"); ok {
		if !isJSONObject(raw) {
			s.errors = append(s.errors, fmt.Sprintf(`%s: expected an object with an "mcpServers" object`, path))
			return
		}
		servers, _ = orderedjson.Parse(raw)
	}
	if raw, ok := parsed.Get("autoEnableCodemode"); ok {
		switch string(raw) {
		case "true", "false":
			value := string(raw) == "true"
			s.autoEnableCodemode = &value
		default:
			s.errors = append(s.errors, fmt.Sprintf("%s: autoEnableCodemode must be a boolean", path))
		}
	}
	if servers == nil {
		return
	}
	for _, name := range servers.Keys() {
		raw, _ := servers.Get(name)
		if scope == "project" && isJSONObject(raw) {
			if value, err := orderedjson.Parse(raw); err == nil && isOverride(value) {
				s.readOverride(path, name, value)
				continue
			}
		}
		config, message := extension.ValidateMcpServerConfig(name, raw)
		if message != "" {
			s.errors = append(s.errors, fmt.Sprintf("%s: %s", path, message))
			continue
		}
		// Names that differ only in `-` and `_` would share a namespace.
		if clash := slices.IndexFunc(s.names, func(other string) bool {
			return other != name && extension.McpNamespace(other) == extension.McpNamespace(name)
		}); clash >= 0 {
			s.errors = append(s.errors, fmt.Sprintf(`%s: server "%s" conflicts with "%s"`, path, name, s.names[clash]))
			continue
		}
		if scope == "project" && config.IsHTTP() && config.Auth != nil {
			s.errors = append(s.errors, fmt.Sprintf(`%s: server "%s": auth is only allowed in the global mcp.json`, path, name))
			continue
		}
		if _, exists := s.servers[name]; !exists {
			s.names = append(s.names, name)
		}
		s.servers[name] = McpServerEntry{Name: name, Config: config, Source: path, Scope: scope}
		if object, err := orderedjson.Parse(raw); err == nil {
			s.raw[name] = object
		}
	}
}

// readOverride applies a project override of the server defined earlier
// under name: its `enabled`, `exposure`, and `toolExposure` replace the
// global entry's, which is validated again with them.
// upstream: packages/coding-agent/src/extensions/mcp/config.ts:readConfigFile
func (s *configState) readOverride(path, name string, value *orderedjson.Object) {
	base, ok := s.servers[name]
	var extra []string
	for _, key := range value.Keys() {
		if !slices.Contains(overrideKeys, key) {
			extra = append(extra, key)
		}
	}
	switch {
	case !ok:
		s.errors = append(s.errors, fmt.Sprintf(`%s: server "%s" needs "command" or "url", or a global server to override`, path, name))
	case len(extra) > 0:
		s.errors = append(s.errors, fmt.Sprintf(`%s: server "%s": an override can only set %s`, path, name, strings.Join(overrideKeys, ", ")))
	default:
		merged := orderedjson.New()
		if baseRaw := s.raw[name]; baseRaw != nil {
			for _, key := range baseRaw.Keys() {
				member, _ := baseRaw.Get(key)
				merged.Set(key, member)
			}
		}
		for _, key := range value.Keys() {
			member, _ := value.Get(key)
			merged.Set(key, member)
		}
		raw, _ := merged.MarshalJSON()
		config, message := extension.ValidateMcpServerConfig(name, raw)
		if message != "" {
			s.errors = append(s.errors, fmt.Sprintf("%s: %s", path, message))
			return
		}
		base.Config = config
		base.Override = path
		s.servers[name] = base
		s.raw[name] = merged
	}
}

// nodeReadError is the message readFileSync(path, "utf8") throws: an error
// opening the file names the path ("EACCES: permission denied, open
// '<path>'"), an error reading it does not ("EISDIR: illegal operation on a
// directory, read").
func nodeReadError(path string, err error) string {
	if pathErr, ok := errors.AsType[*os.PathError](err); ok && pathErr.Op == "read" {
		return tools.NodeFSError(err, "read", "")
	}
	return tools.NodeFSError(err, "open", path)
}

// LoadMcpConfig loads global and (when trusted) project MCP configuration.
// Disabled servers are included with `enabled: false`, so they can be enabled
// again.
func LoadMcpConfig(options LoadOptions) LoadedMcpConfig {
	state := &configState{servers: map[string]McpServerEntry{}, raw: map[string]*orderedjson.Object{}}
	state.readFile(filepath.Join(options.AgentDir, "mcp.json"), "global")
	projectConfig := ""
	if options.ProjectTrusted {
		projectConfig = filepath.Join(options.Cwd, options.ConfigDirName, "mcp.json")
		state.readFile(projectConfig, "project")
	}
	loaded := LoadedMcpConfig{AutoEnableCodemode: state.autoEnableCodemode, Errors: state.errors, ProjectConfig: projectConfig}
	for _, name := range state.names {
		loaded.Servers = append(loaded.Servers, state.servers[name])
	}
	return loaded
}

// McpServerConfigPatch is the settings `/mcp` changes. `Enabled: true` and
// `Exposure: codemode` are the defaults and remove the key.
type McpServerConfigPatch struct {
	Enabled  *bool
	Exposure extension.McpExposure
}

// UpdateMcpServerConfig changes one server's settings in the `mcp.json` that
// defines or overrides it. With options' Override (upstream's optional
// `options`), a missing entry is added as an override. Overrides keep default
// values, since they replace the global server's. Other content is kept; the
// file is rewritten with its indentation.
func UpdateMcpServerConfig(path, name string, patch McpServerConfigPatch, options ...UpdateMcpServerConfigOptions) error {
	var option UpdateMcpServerConfigOptions
	if len(options) > 0 {
		option = options[0]
	}
	return editMcpServers(path, func(servers, parsed *orderedjson.Object) (bool, error) {
		var server *orderedjson.Object
		present := false
		if servers != nil {
			if raw, ok := servers.Get(name); ok {
				present = true
				if isJSONObject(raw) {
					server, _ = orderedjson.Parse(raw)
				}
			}
		}
		created := false
		if !present && option.Override {
			// parsed.mcpServers = { ...servers, [name]: {} }
			if servers == nil {
				servers = orderedjson.New()
			}
			server = orderedjson.New()
			created = true
		}
		if server == nil {
			return false, fmt.Errorf(`%s does not define MCP server "%s"`, path, name)
		}
		keepDefaults := isOverride(server)
		if patch.Enabled != nil {
			if *patch.Enabled && !keepDefaults {
				server.Delete("enabled")
			} else {
				_ = server.SetValue("enabled", *patch.Enabled)
			}
		}
		if patch.Exposure != "" {
			if patch.Exposure == extension.McpExposureCodemode && !keepDefaults {
				server.Delete("exposure")
			} else {
				_ = server.SetValue("exposure", string(patch.Exposure))
			}
		}
		raw, _ := server.MarshalJSON()
		servers.Set(name, raw)
		if created {
			serversRaw, _ := servers.MarshalJSON()
			parsed.Set("mcpServers", serversRaw)
		}
		return true, nil
	})
}

// UpdateMcpServerConfigOptions are the options of [UpdateMcpServerConfig].
type UpdateMcpServerConfigOptions struct {
	// Override adds a missing entry as an override of a global server.
	Override bool
}

// AddMcpServerConfig adds a server to an `mcp.json`, creating the file when
// missing. An existing entry with the same name is replaced. It returns true
// when an entry was replaced.
func AddMcpServerConfig(path, name string, config extension.McpServerConfig) (bool, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return false, err
	}
	return AddMcpServerConfigJSON(path, name, raw)
}

// AddMcpServerConfigJSON is [AddMcpServerConfig] for a config given as JSON
// text: the entry is written with its members in the given order.
func AddMcpServerConfigJSON(path, name string, config json.RawMessage) (bool, error) {
	replaced := false
	err := editMcpServers(path, func(servers, parsed *orderedjson.Object) (bool, error) {
		if servers == nil {
			servers = orderedjson.New()
		}
		replaced = servers.Has(name)
		servers.Set(name, config)
		serversRaw, _ := servers.MarshalJSON()
		parsed.Set("mcpServers", serversRaw)
		return true, nil
	})
	return replaced, err
}

// RemoveMcpServerConfig removes a server from an `mcp.json`. It returns false
// when the file does not define it.
func RemoveMcpServerConfig(path, name string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	removed := false
	err := editMcpServers(path, func(servers, _ *orderedjson.Object) (bool, error) {
		if servers == nil || !servers.Has(name) {
			return false, nil
		}
		servers.Delete(name)
		removed = true
		return true, nil
	})
	return removed, err
}

var indentPattern = lazyregexp.New(`(?m)^([ \t]+)\S`)

// editMcpServers reads an `mcp.json` (an empty config when missing), lets edit
// change its `mcpServers`, and writes it back with its indentation when edit
// returns true. Other content is kept. The servers object edit receives is
// written back into the file's `mcpServers` member, in place.
func editMcpServers(path string, edit func(servers, parsed *orderedjson.Object) (bool, error)) error {
	var text []byte
	parsed := orderedjson.New()
	if data, err := os.ReadFile(path); err == nil {
		text = data
		if !json.Valid(data) {
			if err := jsonparse.Validate(data); err != nil {
				return err
			}
		}
		if parsed, err = orderedjson.Parse(data); err != nil {
			return fmt.Errorf(`%s: expected an object with an "mcpServers" object`, path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New(nodeReadError(path, err))
	}
	var servers *orderedjson.Object
	if raw, ok := parsed.Get("mcpServers"); ok {
		if !isJSONObject(raw) {
			return fmt.Errorf(`%s: expected an object with an "mcpServers" object`, path)
		}
		servers, _ = orderedjson.Parse(raw)
	}
	changed, err := edit(servers, parsed)
	if err != nil || !changed {
		return err
	}
	if servers != nil && parsed.Has("mcpServers") {
		raw, _ := servers.MarshalJSON()
		parsed.Set("mcpServers", raw)
	}
	indent := "  "
	if match := indentPattern.FindSubmatch(text); match != nil {
		indent = string(match[1])
	}
	// JSON.stringify uses at most the first 10 characters of a string indent.
	indent = indent[:min(len(indent), 10)]
	raw, err := parsed.MarshalJSON()
	if err != nil {
		return err
	}
	// JSON.stringify(parsed, null, indent): numbers and escapes are normalized.
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, canonical, "", indent); err != nil {
		return err
	}
	out.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	if err := os.WriteFile(path, out.Bytes(), 0o666); err != nil {
		return errors.New(tools.NodeFSError(err, "open", path))
	}
	return nil
}
