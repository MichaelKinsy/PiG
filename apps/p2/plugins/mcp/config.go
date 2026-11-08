package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/coding/extension"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
)

// Ports apps/p2/plugins/mcp/config.ts: the owner's MCP configuration, `<P2_DATA_DIR>/mcp.json` (a trust boundary): the
// shared `mcpServers` shape checked by pi-mcp, p2's own fields (`shared`, `runOn`) checked here, and the file followed
// while the server runs.
//
// Go mechanic: Pi's `McpStdioServerConfig | McpHttpServerConfig` is coding/extension's McpServerConfig, which the coding
// agent's own MCP configuration uses (the same shared fields).

// RunOnKind is where a stdio server runs.
type RunOnKind string

// Where a stdio server runs: where the conversation works, on the p2 host, or on an environment of `envs.json`.
const (
	RunOnLocation RunOnKind = "location"
	RunOnHost     RunOnKind = "host"
	RunOnEnv      RunOnKind = "env"
)

// RunOn is `"location" | "host" | { env }`.
type RunOn struct {
	Kind RunOnKind
	// Env is the environment's name when Kind is RunOnEnv.
	Env string
}

// OwnerServer is one server of the owner's configuration, validated.
type OwnerServer struct {
	Name string
	// HTTP tells an HTTP server from a stdio one.
	HTTP   bool
	Config extension.McpServerConfig
	// Enabled: `"enabled": false` keeps it off, unless a project's `mcp.json` turns it on where it applies.
	Enabled bool
	// Shared: one process for every session (`"shared": true`), else one per session and location; an HTTP server is
	// shared by default.
	Shared bool
	// RunOn is where a stdio server runs (unused for HTTP).
	RunOn RunOn
}

// OwnerConfig is the owner's servers and why entries were skipped.
type OwnerConfig struct {
	Servers []OwnerServer
	Errors  []string
}

// OwnerConfigState is the file as last read: the last valid servers and their errors, and why the last read failed (nil:
// it did not).
type OwnerConfigState struct {
	OwnerConfig
	Failure *string
}

// ParseOwnerConfig is the owner's configuration in text (`{ "mcpServers": { name: entry } }`, comments allowed): invalid
// entries are skipped and reported in Errors; disabled ones are kept (a project may turn them on). It fails when the file
// itself is not such an object.
func ParseOwnerConfig(text string) (OwnerConfig, error) {
	entries, err := ServerEntries(text, "mcp.json")
	if err != nil {
		return OwnerConfig{}, err
	}
	config := OwnerConfig{Servers: []OwnerServer{}, Errors: []string{}}
	namespaces := map[string]string{}
	for _, name := range entries.Keys() {
		raw, _ := entries.Get(name)
		server, problem := ownerServer(name, raw)
		if problem != "" {
			config.Errors = append(config.Errors, problem)
			continue
		}
		// Tool names and code mode namespaces replace `-` by `_`: both spellings would collide.
		if taken, ok := namespaces[pimcp.McpNamespace(name)]; ok {
			config.Errors = append(config.Errors, fmt.Sprintf(`server "%s": its tools would be named like those of "%s"`, name, taken))
			continue
		}
		namespaces[pimcp.McpNamespace(name)] = name
		config.Servers = append(config.Servers, server)
	}
	return config, nil
}

// ServerEntries is the `mcpServers` entries of text, a configuration file named file (comments allowed), in the file's
// order; it fails when the file is not such an object.
func ServerEntries(text, file string) (*orderedjson.Object, error) {
	stripped := []byte(p2runtime.StripJSONComments(p2runtime.StripBOM(text)))
	if !json.Valid(stripped) {
		return nil, fmt.Errorf("Invalid %s syntax", file)
	}
	top, err := orderedjson.Parse(stripped)
	if err != nil {
		return nil, fmt.Errorf("%s must be an object", file)
	}
	raw, ok := top.Get("mcpServers")
	if !ok || string(raw) == "null" {
		return orderedjson.New(), nil
	}
	if !orderedjson.IsObject(raw) {
		return nil, fmt.Errorf(`%s: "mcpServers" must be an object`, file)
	}
	return orderedjson.Parse(raw)
}

// ownerServer is one entry: the server, or why it is invalid.
func ownerServer(name string, raw json.RawMessage) (OwnerServer, string) {
	invalid := func(problem string) (OwnerServer, string) {
		return OwnerServer{}, fmt.Sprintf(`server "%s": %s`, name, problem)
	}
	result := pimcp.ValidateMcpServerConfig(name, raw, pimcp.McpServerConfigOptions{})
	if result.Kind == pimcp.McpServerConfigInvalid {
		return OwnerServer{}, result.Error
	}
	// The entry keeps fields pi-mcp does not know (p2's `shared` and `runOn` among them).
	var config extension.McpServerConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return invalid(err.Error())
	}
	server := OwnerServer{Name: name, Config: config, Enabled: config.Enabled == nil || *config.Enabled}
	shared, hasShared := result.Fields["shared"]
	if hasShared && string(shared) != "true" && string(shared) != "false" {
		return invalid("shared must be a boolean")
	}
	server.Shared = string(shared) == "true"
	runOn, hasRunOn := result.Fields["runOn"]
	if result.Kind == pimcp.McpServerConfigHTTP {
		if hasRunOn {
			return invalid("runOn is for stdio servers")
		}
		server.HTTP = true
		server.Shared = !hasShared || server.Shared
		return server, ""
	}
	placement, ok := runOnOf(runOn, hasRunOn)
	if !ok {
		return invalid(`runOn must be "location", "host" or { "env": "<name in envs.json>" }`)
	}
	if server.Shared && hasRunOn && placement.Kind == RunOnLocation {
		return invalid(`a shared server cannot run where each conversation works (use runOn "host" or an env)`)
	}
	server.RunOn = placement
	if !hasRunOn {
		server.RunOn = RunOn{Kind: RunOnLocation}
		if server.Shared {
			server.RunOn = RunOn{Kind: RunOnHost}
		}
	}
	return server, ""
}

// runOnOf is `runOn` as configured (set: whether present).
func runOnOf(raw json.RawMessage, set bool) (RunOn, bool) {
	if !set {
		return RunOn{}, true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		switch text {
		case "location":
			return RunOn{Kind: RunOnLocation}, true
		case "host":
			return RunOn{Kind: RunOnHost}, true
		}
		return RunOn{}, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && len(object) == 1 {
		var env string
		if json.Unmarshal(object["env"], &env) == nil && env != "" {
			return RunOn{Kind: RunOnEnv, Env: env}, true
		}
	}
	return RunOn{}, false
}

var hostVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandHostVariables is value with every `${NAME}` replaced by the p2 host's environment variable (the owner's
// configuration only); an unset variable fails, naming it.
func ExpandHostVariables(value string) (string, error) {
	var failure error
	expanded := hostVariable.ReplaceAllStringFunc(value, func(match string) string {
		name := hostVariable.FindStringSubmatch(match)[1]
		variable, ok := os.LookupEnv(name)
		if !ok && failure == nil {
			failure = fmt.Errorf("environment variable %s is not set", name)
		}
		return variable
	})
	return expanded, failure
}

// OwnerConfigFile is `mcp.json`, watched (a missing file is no servers); one process reads it (the server's manager).
type OwnerConfigFile struct {
	observer  *p2runtime.Observer
	mu        sync.Mutex
	state     OwnerConfigState
	listeners map[int]func()
	next      int
}

// NewOwnerConfigFile follows dataDir's mcp.json. last: the servers last read validly before this reader existed (a
// restarted manager's: its new reader may first see a save caught half-way, and keeps them then). Start reads the file the
// first time, Close stops.
func NewOwnerConfigFile(dataDir string, last OwnerConfig) *OwnerConfigFile {
	file := &OwnerConfigFile{state: OwnerConfigState{OwnerConfig: last}, listeners: map[int]func(){}}
	path := filepath.Join(dataDir, "mcp.json")
	files := []p2runtime.ObservedFiles{{
		Env: nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: dataDir}),
		Targets: func(context.Context) ([]durableenv.WatchTarget, error) {
			return []durableenv.WatchTarget{{Path: path}}, nil
		},
	}}
	file.observer = p2runtime.NewObserver(files, func(_ context.Context, retrying bool) bool {
		text := "{}"
		content, err := p2runtime.OptionalText(path)
		if err == nil && content != nil {
			text = *content
		}
		var parsed OwnerConfig
		if err == nil {
			parsed, err = ParseOwnerConfig(text)
		}
		file.mu.Lock()
		if err != nil {
			// A save caught half-way is read once more before it counts; an invalid read keeps the last valid servers.
			if !retrying {
				file.mu.Unlock()
				return true
			}
			message := err.Error()
			file.state = OwnerConfigState{OwnerConfig: file.state.OwnerConfig, Failure: &message}
		} else {
			file.state = OwnerConfigState{OwnerConfig: parsed}
		}
		listeners := make([]func(), 0, len(file.listeners))
		for _, listener := range file.listeners {
			listeners = append(listeners, listener)
		}
		file.mu.Unlock()
		for _, listener := range listeners {
			listener()
		}
		return false
	}, 2*time.Second)
	return file
}

// Start watches the file and reads it the first time.
func (f *OwnerConfigFile) Start(ctx context.Context) error { return f.observer.Start(ctx) }

// Close stops watching.
func (f *OwnerConfigFile) Close() { f.observer.Close() }

// State is the file as last read.
func (f *OwnerConfigFile) State() OwnerConfigState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// Subscribe has listener hear of every read from now on; the result stops it.
func (f *OwnerConfigFile) Subscribe(listener func()) (stop func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.next
	f.next++
	f.listeners[id] = listener
	return func() {
		f.mu.Lock()
		delete(f.listeners, id)
		f.mu.Unlock()
	}
}
