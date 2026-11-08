package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	envsplugin "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports apps/p2/plugins/mcp/placement.ts: where each MCP instance runs and which leases share it: for every session's lease
// at a location, the owner's servers it enables (one process per session and location, or one for everybody) and its
// project's own servers (always at that location), each with how it connects: a program on the p2 host or on an
// environment, or an HTTP server.

// OAuthServer is an HTTP server that signs in with OAuth: its name, URL and the client settings of its `oauth` entry.
type OAuthServer struct {
	Name   string
	URL    string
	Config *extension.McpOAuthConfig
}

// ConnectKind is how an instance connects.
type ConnectKind int

// How an instance connects: a program on the p2 host or on an environment, an HTTP server, or why it cannot run.
const (
	ConnectHost ConnectKind = iota
	ConnectRemote
	ConnectHTTP
	ConnectUnplaced
)

// Connect is how an instance connects. Which fields count depends on Kind.
type Connect struct {
	Kind ConnectKind

	// Host and Remote: the program.
	Command string
	Args    []string
	Env     map[string]string
	// Cwd: on the host, resolved; on an environment, as configured (`.` when not): relative to Base, `~` the remote home.
	Cwd string
	// HostVariables: `${NAME}` in Env values comes from the p2 host's environment (the owner's servers only).
	HostVariables bool

	// Host: the `file://` URLs offered as the client's roots (an instance on its location's machine only).
	Roots []string

	// Remote: the environment's name in envs.json, and its configuration there.
	Environment string
	Config      *chordjson.Object
	// Revision is the connection's revision: the configuration's, a sandbox's with its host's (`ConnectionRevision`).
	Revision string
	// Base is the location's cwd when the location is on this environment (also the client's root), else nil: the home.
	Base *string

	// HTTP: the server.
	URL     string
	Headers map[string]string
	// OAuth signs in with OAuth (no `Authorization` header configured), as in pi; nil: it does not.
	OAuth *OAuthServer

	// Unplaced: why it cannot run.
	Reason string
}

// InstanceSpec is one instance to run: its identity and where and how it runs. Equal configuration and key give an equal ID.
type InstanceSpec struct {
	ID     string
	Server string
	// Source is "owner" or "project".
	Source string
	// Location is the location it serves; nil: shared by every session, or an HTTP server.
	Location *p2runtime.Location
	RunsOn   RunOn
	Connect  Connect
	// TimeoutMs is the per-request timeout from the configuration, in milliseconds (nil: none configured).
	TimeoutMs *float64
}

// ServerUse is the instance a lease uses for a server.
type ServerUse struct{ Server, ID string }

// PlacedLease is one lease placed: the instance of each server it uses, by server name, and why entries of its project were
// refused.
type PlacedLease struct {
	Lease   Lease
	Servers []ServerUse
	Errors  []string
}

// InstanceOf is the ID of the instance this lease uses for server name.
func (p PlacedLease) InstanceOf(name string) (string, bool) {
	for _, use := range p.Servers {
		if use.Server == name {
			return use.ID, true
		}
	}
	return "", false
}

// Placement is what the leases need: the instances each lease uses, and the shared instances that may stay without one.
type Placement struct {
	Instances map[string]InstanceSpec
	Leases    []PlacedLease
	// Kept are the shared instances of enabled owner servers: they stay running while configured, leased or not.
	Kept map[string]bool
}

// WorkEnvironments are the environments each session's conversations work on, by session, as the server records them (the
// session's own, its subagents' own locations'); "" is the p2 host.
type WorkEnvironments map[string][]string

// envOf is a location's environment as WorkEnvironments names it.
func envOf(location p2runtime.Location) string {
	if location.Env == nil {
		return ""
	}
	return *location.Env
}

// Place is the instances config's servers and the leases' projects need, on the environments of envs. A lease at a location
// on an environment its session does not work on (work) gets no servers: a worker can only lease where its conversations can
// already run commands. A locked session's lease (locks) gets only the servers its owner picked, and its project's servers
// only when picked too.
func Place(config OwnerConfig, leases []Lease, envs *chordjson.Object, work WorkEnvironments, locks map[string]p2runtime.SessionLock) Placement {
	placement := Placement{Instances: map[string]InstanceSpec{}, Kept: map[string]bool{}}
	for _, server := range config.Servers {
		if server.Enabled && server.Shared {
			spec := ownerSpec(server, nil, envs)
			placement.Kept[spec.ID] = true
			placement.Instances[spec.ID] = spec
		}
	}
	for _, lease := range leases {
		where := p2runtime.LocationText(lease.Location)
		placed := PlacedLease{Lease: lease, Servers: []ServerUse{}, Errors: []string{}}
		works := false
		for _, env := range work[lease.Session] {
			works = works || env == envOf(lease.Location)
		}
		if !works {
			placed.Errors = []string{where + ": not where this session works"}
			placement.Leases = append(placement.Leases, placed)
			continue
		}
		project := projectOf(lease, config)
		use := func(spec InstanceSpec) {
			placement.Instances[spec.ID] = spec
			for i, used := range placed.Servers {
				if used.Server == spec.Server {
					placed.Servers[i].ID = spec.ID
					return
				}
			}
			placed.Servers = append(placed.Servers, ServerUse{Server: spec.Server, ID: spec.ID})
		}
		lock, locked := locks[lease.Session]
		for _, server := range config.Servers {
			enabled, set := project.Enabled.Get(server.Name)
			if !set {
				enabled = server.Enabled
			}
			if enabled && (!locked || contains(lock.MCP, server.Name)) {
				use(ownerSpec(server, &lease, envs))
			}
		}
		if !locked || lock.ProjectMCP {
			for _, server := range project.Servers {
				use(projectSpec(server, lease, envs))
			}
		}
		for _, problem := range project.Errors {
			placed.Errors = append(placed.Errors, where+": "+problem)
		}
		placement.Leases = append(placement.Leases, placed)
	}
	return placement
}

func contains(list []string, item string) bool {
	for _, each := range list {
		if each == item {
			return true
		}
	}
	return false
}

// projectOf is the project configuration the lease's worker sent, checked.
func projectOf(lease Lease, config OwnerConfig) ProjectConfig {
	switch lease.Project.Kind {
	case ProjectFile:
		return ParseProjectConfig(lease.Project.Text, config)
	case ProjectUnreadable:
		return ProjectConfig{Errors: []string{".pi/mcp.json: " + lease.Project.Error}}
	}
	return ProjectConfig{}
}

// timeoutOf is a configured timeout, in seconds, as milliseconds.
func timeoutOf(seconds *float64) *float64 {
	if seconds == nil {
		return nil
	}
	return new(*seconds * 1000)
}

// ownerSpec is the instance of owner server server that lease uses (shared: everybody's, lease unused).
func ownerSpec(server OwnerServer, lease *Lease, envs *chordjson.Object) InstanceSpec {
	timeout := timeoutOf(server.Config.Timeout)
	var owned *Lease
	if !server.Shared {
		owned = lease
	}
	if server.HTTP {
		headers := stringsOf(server.Config.Headers)
		// Unshared HTTP servers are one per session, wherever its conversations work.
		hashed := map[string]any{"url": server.Config.URL, "headers": headers, "timeoutMs": timeout}
		if server.Config.OAuth != nil {
			hashed["oauth"] = server.Config.OAuth
		}
		var session any
		if owned != nil {
			session = owned.Session
		}
		return InstanceSpec{
			ID:        idOf([]any{"http", server.Name, session, hashOf(hashed)}),
			Server:    server.Name,
			Source:    "owner",
			RunsOn:    RunOn{Kind: RunOnHost},
			Connect:   Connect{Kind: ConnectHTTP, URL: server.Config.URL, Headers: headers, OAuth: OAuthServerOf(server)},
			TimeoutMs: timeout,
		}
	}
	var at *p2runtime.Location
	if owned != nil {
		at = &owned.Location
	}
	runsOn := server.RunOn
	if runsOn.Kind == RunOnLocation {
		runsOn = RunOn{Kind: RunOnHost}
		if at != nil && at.Env != nil && *at.Env != "" {
			runsOn = RunOn{Kind: RunOnEnv, Env: *at.Env}
		}
	}
	placed := stdioPlaced{server: server.Name, source: "owner", location: at}
	if owned != nil {
		placed.session = &owned.Session
	}
	return stdioSpec(placed, runsOn, server.Config, timeout, envs)
}

// OAuthServerOf is how owner server server signs in with OAuth: as in pi, an HTTP server without an `Authorization` header.
func OAuthServerOf(server OwnerServer) *OAuthServer {
	if !server.HTTP {
		return nil
	}
	if server.Config.Headers != nil {
		for _, header := range server.Config.Headers.Keys() {
			if strings.EqualFold(header, "authorization") {
				return nil
			}
		}
	}
	return &OAuthServer{Name: server.Name, URL: server.Config.URL, Config: server.Config.OAuth}
}

// projectSpec is the instance of project server server at lease's location: always there, for that session alone.
func projectSpec(server ProjectServer, lease Lease, envs *chordjson.Object) InstanceSpec {
	runsOn := RunOn{Kind: RunOnHost}
	if lease.Location.Env != nil {
		runsOn = RunOn{Kind: RunOnEnv, Env: *lease.Location.Env}
	}
	session := lease.Session
	return stdioSpec(stdioPlaced{server: server.Name, source: "project", session: &session, location: &lease.Location}, runsOn,
		server.Config, timeoutOf(server.Config.Timeout), envs)
}

// stdioPlaced is who a stdio instance is for: its server, the configuration it comes from, and its session and location if
// unshared.
type stdioPlaced struct {
	server   string
	source   string
	session  *string
	location *p2runtime.Location
}

// locationJSON is a location as the instance key hashes it.
func locationJSON(location *p2runtime.Location) any {
	if location == nil {
		return nil
	}
	return map[string]any{"cwd": location.Cwd, "env": location.Env}
}

// stdioSpec is a stdio instance on runsOn; on its location's machine its cwd is the base of relative paths and the root.
func stdioSpec(who stdioPlaced, runsOn RunOn, config extension.McpServerConfig, timeout *float64, envs *chordjson.Object) InstanceSpec {
	env := stringsOf(config.Env)
	var cwd any
	if config.Cwd != "" {
		cwd = config.Cwd
	}
	hash := hashOf(map[string]any{"command": config.Command, "args": config.Args, "env": env, "cwd": cwd, "timeoutMs": timeout})
	var owner any
	if who.session != nil {
		owner = []any{*who.session, locationJSON(who.location)}
	}
	machine := ""
	if runsOn.Kind == RunOnEnv {
		machine = runsOn.Env
	}
	var base *string
	if who.location != nil && envOf(*who.location) == machine {
		base = &who.location.Cwd
	}
	hostVariables := who.source == "owner"
	args := config.Args
	if args == nil {
		args = []string{}
	}
	runsOnJSON := any("host")
	if runsOn.Kind == RunOnEnv {
		runsOnJSON = map[string]any{"env": runsOn.Env}
	}
	spec := InstanceSpec{Server: who.server, Source: who.source, Location: who.location, RunsOn: runsOn, TimeoutMs: timeout}
	if runsOn.Kind == RunOnHost {
		spec.ID = idOf([]any{"stdio", who.source, who.server, owner, runsOnJSON, hash})
		home, _ := os.UserHomeDir()
		directory := home
		if base != nil {
			directory = *base
		}
		configured := "."
		if config.Cwd != "" {
			configured = expandHome(config.Cwd)
		}
		spec.Connect = Connect{
			Kind:          ConnectHost,
			Command:       expandHome(config.Command),
			Args:          mapStrings(args, expandHome),
			Env:           env,
			Cwd:           resolvePath(directory, configured),
			HostVariables: hostVariables,
		}
		if base != nil {
			spec.Connect.Roots = []string{nodeurl.PathToFileURL(*base, false)}
		} else {
			spec.Connect.Roots = []string{}
		}
		return spec
	}
	var revision any
	var envConfig *chordjson.Object
	if value, ok := envs.Get(runsOn.Env); ok {
		envConfig, _ = value.(*chordjson.Object)
	}
	revisionText := ""
	if envConfig != nil {
		// An edited environment (or a sandbox's edited host) is another connection: its instances start again on it.
		revisionText = envsplugin.ConnectionRevision(envs, runsOn.Env)
		revision = revisionText
	}
	spec.ID = idOf([]any{"stdio", who.source, who.server, owner, runsOnJSON, revision, hash})
	if envConfig == nil {
		spec.Connect = Connect{Kind: ConnectUnplaced, Reason: `environment "` + runsOn.Env + `" is not in envs.json`}
		return spec
	}
	remoteCwd := "."
	if config.Cwd != "" {
		remoteCwd = config.Cwd
	}
	spec.Connect = Connect{
		Kind: ConnectRemote, Environment: runsOn.Env, Config: envConfig, Revision: revisionText,
		Command: config.Command, Args: args, Env: env, Cwd: remoteCwd, Base: base, HostVariables: hostVariables,
	}
	return spec
}

// stringsOf is an ordered string map as a plain one (nil: none).
func stringsOf(values *extension.OrderedStrings) map[string]string {
	out := map[string]string{}
	if values == nil {
		return out
	}
	for _, key := range values.Keys() {
		out[key], _ = values.Get(key)
	}
	return out
}

func mapStrings(values []string, fn func(string) string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = fn(value)
	}
	return out
}

// expandHome: `~` and `~/…` name the home directory, as in a shell (and in pi).
func expandHome(value string) string {
	home, _ := os.UserHomeDir()
	if value == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(value, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return value
}

// resolvePath is Node's `resolve(base, path)` for a base that is absolute.
func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

// hashOf is the configuration hash: what makes another process or connection (command, args, env, cwd; URL, headers and
// OAuth client settings; the timeout).
func hashOf(value any) string { return digest(value) }

// idOf is an instance's ID from its key.
func idOf(key any) string { return digest(key) }

func digest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:16]
}
