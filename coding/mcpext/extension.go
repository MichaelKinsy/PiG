package mcpext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Ports packages/coding-agent/src/extensions/mcp/index.ts.
//
// Built-in MCP integration.
//
// Connects the servers from `mcp.json` and the servers extensions register with
// `pi.registerMcpServer()` when a session starts, and servers registered later
// right away. A server in `mcp.json` takes precedence over a registered server
// of the same name. Connections run in the background: the first prompt waits
// only for servers with `direct` tools, and codemode scripts, `tool_search`,
// and the resource tools wait for the servers they need when they run. Tools
// are registered as `mcp__<server>__<tool>`. By default
// (`"exposure": "codemode"`) the tools are only callable from codemode scripts,
// which keeps MCP tools out of the model's tool declarations and the codemode
// description: scripts find the tools with `searchTools()` and the server
// instructions with `describeNamespace()`. The codemode tool is activated for
// that unless `autoEnableCodemode` is false. `"deferred"` declares the tools to
// the model once the `tool_search` tool loads them, and activates `tool_search`
// instead of codemode. `"exposure": "direct"`
// declares them to the model right away, and `"hidden"` makes them unreachable.
// `toolExposure` overrides the exposure of single tools. Servers with resources
// are reached through Codex's `list_mcp_resources`,
// `list_mcp_resource_templates`, and `read_mcp_resource` tools (resources.go).
//
// Every call runs through pi's tool pipeline, so `tool_call`/`tool_result`
// hooks and permission extensions apply to MCP tools the same way they do to
// built-in tools.
//
// Problems found at startup (config errors, failed connections, servers that
// need a sign-in) are reported once. Upstream's `/mcp` command opens a manager
// to sign in, reconnect, enable or disable servers, and change their exposure;
// the last two are saved to the `mcp.json` that defines the server, or apply to
// the current session for registered servers. [Extension.RunCommand] is that
// command and [Extension.Manage] its manager, shown in a [McpManagerView].

// Host is the part of the extension API the MCP extension uses. The runner
// implements it; a Piglet that leaves the extension out never needs it.
type Host interface {
	// RegisterTool registers a tool, replacing an earlier registration of the
	// name. Tools with `direct` exposure are activated on registration.
	RegisterTool(definition extension.ToolDefinition)
	// GetAllTools returns every registered tool.
	GetAllTools() []extension.ToolInfo
	GetActiveTools() []string
	SetActiveTools(names []string)
	// GetMcpServers returns the servers extensions registered.
	GetMcpServers() []extension.RegisteredMcpServer
}

// EventContext is the extension context an event handler receives.
type EventContext struct {
	// Cwd is the working directory of the session, for stdio servers.
	Cwd string
	// ProviderToken is the current token of a pi provider of the session's model registry, for servers with
	// `auth.provider`. It returns "" when the provider has none.
	ProviderToken func(ctx context.Context, provider string) string
	// IsProjectTrusted reports whether project configuration may be read.
	IsProjectTrusted func() bool
	// Notify shows a message to the user. Level is "info", "warning", or "error".
	Notify func(message, level string)
}

func (c EventContext) notify(message, level string) {
	if c.Notify == nil {
		return
	}
	// upstream: packages/coding-agent/src/extensions/mcp/index.ts:"MCP failed to load" (try/catch around ctx.ui.notify)
	defer func() { _ = recover() }() // The session may have been disposed meanwhile, which makes ctx stale.
	c.Notify(message, level)
}

// Options configure the extension.
type Options struct {
	// LoadConfig defaults to reading `mcp.json` from the agent directory and the trusted project.
	LoadConfig func(ctx EventContext) LoadedMcpConfig
	// CreateTransport defaults to stdio and streamable HTTP transports built from the server config.
	CreateTransport TransportFactory
	// Credentials defaults to `mcp-auth.json` in the agent directory.
	Credentials *McpOAuthCredentialStore
	// LogPath is the file server log messages are appended to. Defaults to `mcp.log` in the agent directory.
	LogPath string
	// OpenURL opens the OAuth authorization URL. Defaults to doing nothing; the host supplies the platform browser.
	OpenURL func(url string)
	// UpdateConfig saves `/mcp` changes to the server's config file. Defaults to editing its `mcp.json`.
	UpdateConfig func(entry McpServerEntry, patch McpServerConfigPatch) error
	// StartupWait is how long the first prompt waits for servers that are still
	// connecting at startup. Their tools become available when they connect.
	// Default: 10 seconds.
	StartupWait time.Duration
	// AgentDir and ConfigDirName locate the configuration and the credentials.
	AgentDir      string
	ConfigDirName string
	// ClientName and ClientVersion identify this client to servers.
	ClientName    string
	ClientVersion string
	// IsCodemodeTool and IsToolSearchTool tell the built-in tools from another
	// extension's tool of the same name. The defaults match the built-in
	// extensions' source paths.
	IsCodemodeTool   func(extension.ToolInfo) bool
	IsToolSearchTool func(extension.ToolInfo) bool
}

const (
	// upstream: packages/coding-agent/src/extensions/mcp/index.ts:DEFAULT_STARTUP_WAIT_MS
	defaultStartupWait = 10 * time.Second
	// CodemodeToolName and ToolSearchToolName are the tools that reach MCP tools that are not declared.
	CodemodeToolName   = "codemode"
	ToolSearchToolName = "tool_search"
)

// server is a configured server. Disabled servers have no connection.
type server struct {
	entry      McpServerEntry
	connection *Connection
	// registeredConfig is, for servers extensions registered, the config as
	// registered, to detect re-registrations.
	registeredConfig string
	// message is the result of the last `/mcp` action that failed, shown in the manager.
	message string
	// ready is closed when the connection started for the server connected or failed.
	ready chan struct{}
}

func (s *server) enabled() bool { return s.entry.Config.Enabled == nil || *s.entry.Config.Enabled }

func exposureOf(entry McpServerEntry) extension.McpExposure {
	if entry.Config.Exposure != "" {
		return entry.Config.Exposure
	}
	return extension.McpExposureCodemode
}

// configuredExposures are the exposures the server's tools can have, known from its config before it connects.
func configuredExposures(entry McpServerEntry) map[extension.McpExposure]bool {
	exposures := map[extension.McpExposure]bool{exposureOf(entry): true}
	for _, pattern := range entry.Config.ToolExposure.Keys() {
		exposure, _ := entry.Config.ToolExposure.Get(pattern)
		exposures[exposure] = true
	}
	return exposures
}

// hasDirectTools reports whether some of the server's tools are declared to the model, so the first prompt waits for them.
func hasDirectTools(entry McpServerEntry) bool {
	return configuredExposures(entry)[extension.McpExposureDirect]
}

// hasIndirectTools reports whether some of the server's tools are reached through codemode or tool_search.
func hasIndirectTools(entry McpServerEntry) bool {
	exposures := configuredExposures(entry)
	return exposures[extension.McpExposureCodemode] || exposures[extension.McpExposureDeferred]
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// McpServersSection is the name of the system prompt section that lists the servers whose tools are not declared.
//
// Ports packages/coding-agent/src/extensions/mcp/index.ts (MCP_SERVERS_SECTION).
const McpServersSection = "mcp_servers"

// MaxServersSectionChars is the size of the whole `mcp_servers` section in UTF-16 code units. Descriptions shrink to
// fit; when the server lines alone do not fit, the last servers are left out and counted in a closing line.
//
// Ports packages/coding-agent/src/extensions/mcp/index.ts (MAX_SERVERS_SECTION_CHARS).
const MaxServersSectionChars = 4096

// McpServerListing is what the `mcp_servers` section needs of a server.
type McpServerListing struct {
	Entry McpServerEntry
	// Instructions are the server instructions of a connected server.
	Instructions string
}

// maxServerDescriptionChars is the size of a server description in the section, as Codex allows for deferred namespaces.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:MAX_SERVER_DESCRIPTION_CHARS
const maxServerDescriptionChars = 250

// serversSectionIntro opens the `mcp_servers` section.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:SERVERS_SECTION_INTRO
const serversSectionIntro = "MCP servers whose tools are not declared to you. Call the tools of `codemode` servers from codemode scripts: find them with `searchTools(query, { namespace })` and read a server's instructions and tool names with `describeNamespace(name)`. Load the tools of `tool_search` servers with `tool_search`."

// jsLength is JavaScript's string length: UTF-16 code units.
func jsLength(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}

// truncateChars is `truncate` of index.ts: text of at most max UTF-16 code units, else its start cut to max-1 units, without trailing white space, and an ellipsis.
func truncateChars(text string, max int) string {
	if jsLength(text) <= max {
		return text
	}
	if max <= 1 {
		return ""
	}
	units := utf16.Encode([]rune(text))[:max-1]
	return strings.TrimRightFunc(string(utf16.Decode(units)), isJSWhitespace) + "…"
}

// serverSummary is the first line of the configured description, or of the server instructions once connected.
func serverSummary(server McpServerListing) string {
	text := strings.TrimFunc(server.Entry.Config.Description, isJSWhitespace)
	if text == "" {
		text = server.Instructions
	}
	first, _, _ := strings.Cut(text, "\n")
	return strings.TrimFunc(first, isJSWhitespace)
}

// RenderServersSection is the `mcp_servers` section: every enabled server with codemode or deferred tools, with how its
// tools are reached and a one-line summary. The model learns of the servers from it, since neither codemode nor
// tool_search lists them. It reports false when there are no such servers.
//
// Ports packages/coding-agent/src/extensions/mcp/index.ts (renderServersSection).
func RenderServersSection(servers []McpServerListing) (string, bool) {
	var listed []McpServerListing
	for _, server := range servers {
		if server.Entry.Config.Enabled == nil || *server.Entry.Config.Enabled {
			if hasIndirectTools(server.Entry) {
				listed = append(listed, server)
			}
		}
	}
	if len(listed) == 0 {
		return "", false
	}
	slices.SortStableFunc(listed, func(a, b McpServerListing) int { return localeCompare(a.Entry.Name, b.Entry.Name) })
	heads := make([]string, len(listed))
	for i, server := range listed {
		reach := "tool_search"
		if configuredExposures(server.Entry)[extension.McpExposureCodemode] {
			reach = "codemode"
		}
		heads[i] = fmt.Sprintf("- %s (%s)", extension.McpNamespace(server.Entry.Name), reach)
	}
	omitted := func(count int) []string {
		if count <= 0 {
			return nil
		}
		return []string{fmt.Sprintf("- … %d more server%s; find their tools with searchTools()", count, pluralSuffix(count))}
	}
	// Characters of the intro, the first `kept` server lines without descriptions, and the omission line.
	size := func(kept int) int {
		return jsLength(strings.Join(slices.Concat([]string{serversSectionIntro}, heads[:kept], omitted(len(listed)-kept)), "\n"))
	}
	kept := len(listed)
	for kept > 0 && size(kept) > MaxServersSectionChars {
		kept--
	}
	// Each description also takes a ": " separator.
	perServer := 0
	if kept > 0 {
		perServer = min(maxServerDescriptionChars, (MaxServersSectionChars-size(kept))/kept-2)
	}
	lines := make([]string, kept)
	for i, server := range listed[:kept] {
		summary := ""
		if perServer > 0 {
			summary = truncateChars(serverSummary(server), perServer)
		}
		lines[i] = heads[i]
		if summary != "" {
			lines[i] += ": " + summary
		}
	}
	return strings.Join(slices.Concat([]string{serversSectionIntro}, lines, omitted(len(listed)-kept)), "\n"), true
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Extension is the MCP integration of one session. Its event methods are what
// upstream registers with `pi.on(...)`. It is safe for concurrent use.
type Extension struct {
	host    Host
	options Options

	mu                 sync.Mutex
	servers            []*server
	configuredEntries  []McpServerEntry
	configErrors       []string
	overridden         []string
	sessionActive      bool
	autoEnableCodemode bool
	warnedUnreachable  bool
	pending            chan struct{}
	waitedForStartup   bool
	// generation is bumped on every session start and shutdown so work that
	// finishes late is dropped.
	generation int
	sessionCwd string
	// providerToken resolves the tokens of `auth.provider` servers from the session's model registry.
	providerToken func(ctx context.Context, provider string) string
	credentials   *McpOAuthCredentialStore
	serverLog     *McpServerLog
	listeners     map[int]func()
	nextListener  int
	// tokensAtSignIn holds the stored tokens of servers waiting for a sign-in,
	// as they were when the sign-in was needed. `pi mcp login` in another
	// process (for example run by the agent) changes them.
	tokensAtSignIn map[*Connection]string
	background     sync.WaitGroup

	toolMu sync.Mutex
	// toolOwners maps a tool name to the `<server>\0<tool>` it was assigned to, so names stay unique and stable.
	toolOwners map[string]string
	// serverTools are the tool names currently offered by each server.
	serverTools map[string]map[string]bool
	// lanes keep the calls to each server in call order (see extension.CallLane).
	lanes map[string]*extension.CallLane
	// definitions is the last definition registered under each tool name, to re-register withdrawn tools as hidden.
	definitions map[string]extension.ToolDefinition
	// resourceToolsExposure is the exposure the resource tools were last registered with.
	resourceToolsExposure   extension.McpExposure
	resourceToolsRegistered bool
}

// New returns the extension for host.
func New(host Host, options Options) *Extension {
	if options.StartupWait == 0 {
		options.StartupWait = defaultStartupWait
	}
	if options.IsCodemodeTool == nil {
		options.IsCodemodeTool = func(tool extension.ToolInfo) bool {
			return tool.Name == CodemodeToolName && sourcePath(tool) == "builtin:codemode"
		}
	}
	if options.IsToolSearchTool == nil {
		options.IsToolSearchTool = func(tool extension.ToolInfo) bool {
			return tool.Name == ToolSearchToolName && sourcePath(tool) == "builtin:tool-search"
		}
	}
	return &Extension{
		host:               host,
		options:            options,
		autoEnableCodemode: true,
		credentials:        options.Credentials,
		listeners:          map[int]func(){},
		tokensAtSignIn:     map[*Connection]string{},
		toolOwners:         map[string]string{},
		serverTools:        map[string]map[string]bool{},
		lanes:              map[string]*extension.CallLane{},
		definitions:        map[string]extension.ToolDefinition{},
	}
}

// Subscribe registers a function called whenever the servers or their state
// change. The returned function removes it.
func (e *Extension) Subscribe(listener func()) (unsubscribe func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextListener++
	id := e.nextListener
	e.listeners[id] = listener
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.listeners, id)
	}
}

func (e *Extension) emitChange() {
	e.mu.Lock()
	ids := make([]int, 0, len(e.listeners))
	for id := range e.listeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	listeners := make([]func(), len(ids))
	for i, id := range ids {
		listeners[i] = e.listeners[id]
	}
	e.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

func (e *Extension) findServerLocked(name string) *server {
	for _, s := range e.servers {
		if s.entry.Name == name {
			return s
		}
	}
	return nil
}

func (e *Extension) findServer(name string) *server {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.findServerLocked(name)
}

// registeredServers are the servers extensions registered, except names
// `mcp.json` defines, which take precedence.
func (e *Extension) registeredServers() (registered []*server, overriddenNames []string) {
	e.mu.Lock()
	configured := slices.Clone(e.configuredEntries)
	e.mu.Unlock()
	for _, reg := range e.host.GetMcpServers() {
		if i := slices.IndexFunc(configured, func(entry McpServerEntry) bool {
			return extension.McpNamespace(entry.Name) == extension.McpNamespace(reg.Name)
		}); i >= 0 {
			overriddenNames = append(overriddenNames, fmt.Sprintf(`"%s" registered by %s is overridden by "%s" in %s`, reg.Name, reg.ExtensionPath, configured[i].Name, configured[i].Source))
			continue
		}
		config, _ := json.Marshal(reg.Config)
		registered = append(registered, &server{
			entry:            McpServerEntry{Name: reg.Name, Config: reg.Config, Source: reg.ExtensionPath, Scope: "extension"},
			registeredConfig: string(config),
		})
	}
	return registered, overriddenNames
}

func (e *Extension) getCredentials() *McpOAuthCredentialStore {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.credentials == nil {
		e.credentials = NewMcpOAuthCredentialStore(e.options.AgentDir)
	}
	return e.credentials
}

func (e *Extension) getServerLog() *McpServerLog {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.serverLog == nil {
		path := e.options.LogPath
		if path == "" {
			path = filepath.Join(e.options.AgentDir, "mcp.log")
		}
		e.serverLog = NewMcpServerLog(path)
	}
	return e.serverLog
}

// ---------------------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------------------

func (e *Extension) currentEntry(connection *Connection) McpServerEntry {
	if s := e.findServer(connection.Entry.Name); s != nil {
		return s.entry
	}
	return connection.Entry
}

// registerTools registers the tools a connection offers. It is the connection's
// OnTools callback.
func (e *Extension) registerTools(connection *Connection) {
	name := connection.Entry.Name
	entry := e.currentEntry(connection)
	namespace := extension.ToolNamespace{
		Name:         extension.McpNamespace(name),
		Description:  strings.TrimFunc(entry.Config.Description, isJSWhitespace),
		Instructions: connection.Instructions(),
	}

	e.toolMu.Lock()
	previous := e.serverTools[name]
	current := map[string]bool{}
	// Like Codex, all tools whose names sanitize to the same name get the hash suffix, so which one would keep the plain
	// name does not depend on the order of the list.
	var plain []string
	seen := map[string]bool{}
	for _, tool := range connection.Tools() {
		if !seen[tool.Name] {
			seen[tool.Name] = true
			plain = append(plain, CreateMcpToolName(name, tool.Name, nil))
		}
	}
	assignName := func(tool, owner string) string {
		toolName := CreateMcpToolName(name, tool, func(candidate string) bool {
			existing, taken := e.toolOwners[candidate]
			return (taken && existing != owner) || current[candidate] ||
				countOf(plain, candidate) > 1
		})
		e.toolOwners[toolName] = owner
		current[toolName] = true
		return toolName
	}
	var register []extension.ToolDefinition
	for _, tool := range connection.Tools() {
		definition := CreateMcpToolDefinition(McpToolOptions{
			Server:    name,
			Tool:      tool,
			Name:      assignName(tool.Name, name+"\x00"+tool.Name),
			Exposure:  extension.GetMcpToolExposure(entry.Config, tool.Name),
			Namespace: namespace,
			Timeout:   int(connection.Timeout().Milliseconds()),
			GetClient: func(context.Context) (McpToolCaller, error) { return connection, nil },
			Lane:      e.lane(name),
			ReadableResources: func() bool {
				return slices.ContainsFunc(e.resourceServers(), func(s McpResourceServer) bool { return s == McpResourceServer(connection) })
			},
		})
		e.definitions[definition.Name] = definition
		register = append(register, definition)
	}
	e.serverTools[name] = current
	// Tools cannot be unregistered, so tools the server dropped are re-registered as hidden. When
	// the server offers them again they are registered with their configured exposure above.
	for toolName := range previous {
		if definition, ok := e.definitions[toolName]; ok && !current[toolName] {
			hidden := definition
			hidden.Exposure = extension.ToolExposureHidden
			register = append(register, hidden)
		}
	}
	e.toolMu.Unlock()
	for _, definition := range register {
		e.host.RegisterTool(definition)
	}
	e.syncResourceTools()
}

// hideTools makes a disabled server's tools unreachable.
func (e *Extension) hideTools(name string) {
	e.toolMu.Lock()
	var register []extension.ToolDefinition
	for toolName := range e.serverTools[name] {
		if definition, ok := e.definitions[toolName]; ok {
			hidden := definition
			hidden.Exposure = extension.ToolExposureHidden
			register = append(register, hidden)
		}
	}
	e.serverTools[name] = map[string]bool{}
	e.toolMu.Unlock()
	// Registration order follows the tool names, so hiding is deterministic.
	slices.SortFunc(register, func(a, b extension.ToolDefinition) int { return strings.Compare(a.Name, b.Name) })
	for _, definition := range register {
		e.host.RegisterTool(definition)
	}
	e.syncResourceTools()
}

// serversWithResources are the enabled servers with resources whose exposure
// is not `hidden`, which the resource tools reach.
func (e *Extension) serversWithResources() []*server {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*server
	for _, s := range e.servers {
		if s.connection != nil && s.connection.HasResources() && s.enabled() && exposureOf(s.entry) != extension.McpExposureHidden {
			out = append(out, s)
		}
	}
	return out
}

func (e *Extension) resourceServers() []McpResourceServer {
	var out []McpResourceServer
	for _, s := range e.serversWithResources() {
		out = append(out, s.connection)
	}
	return out
}

// syncResourceTools registers the resource tools with the widest exposure of
// the servers they reach: `direct` when one of them is direct, and so on. They
// are hidden when no server has resources.
func (e *Extension) syncResourceTools() {
	exposures := map[extension.McpExposure]bool{}
	for _, s := range e.serversWithResources() {
		exposures[exposureOf(s.entry)] = true
	}
	next := extension.McpExposureHidden
	for _, candidate := range []extension.McpExposure{extension.McpExposureDirect, extension.McpExposureCodemode, extension.McpExposureDeferred} {
		if exposures[candidate] {
			next = candidate
			break
		}
	}
	e.toolMu.Lock()
	if e.resourceToolsRegistered && next == e.resourceToolsExposure {
		e.toolMu.Unlock()
		return
	}
	if !e.resourceToolsRegistered && next == extension.McpExposureHidden {
		e.toolMu.Unlock()
		return
	}
	wasDirect := e.resourceToolsRegistered && e.resourceToolsExposure == extension.McpExposureDirect
	e.resourceToolsExposure, e.resourceToolsRegistered = next, true
	e.toolMu.Unlock()
	definitions := CreateMcpResourceToolDefinitions(ResourceToolsOptions{Exposure: next, Servers: e.resourceServers})
	for _, definition := range definitions {
		e.host.RegisterTool(definition)
	}
	if wasDirect {
		names := map[string]bool{}
		for _, definition := range definitions {
			names[definition.Name] = true
		}
		var active []string
		for _, name := range e.host.GetActiveTools() {
			if !names[name] {
				active = append(active, name)
			}
		}
		e.host.SetActiveTools(active)
	}
}

// ensureDiscoveryActive: tools that are not declared to the model are reached
// through the codemode tool (scripts call them) or the tool_search tool (it
// declares them). Either reaches every such tool. It activates the one the
// tools' exposure asks for: codemode for `codemode` unless
// `autoEnableCodemode` is false, tool_search for `deferred`.
func (e *Extension) ensureDiscoveryActive(ctx EventContext) {
	// From the config, so the tool is active before the servers connect. Resource tools share
	// their server's exposure.
	exposures := map[extension.McpExposure]bool{}
	e.mu.Lock()
	servers := slices.Clone(e.servers)
	autoEnable := e.autoEnableCodemode
	e.mu.Unlock()
	for _, s := range servers {
		if s.enabled() {
			for exposure := range configuredExposures(s.entry) {
				exposures[exposure] = true
			}
		}
	}
	needsCodemode := exposures[extension.McpExposureCodemode]
	needsToolSearch := exposures[extension.McpExposureDeferred]
	if !needsCodemode && !needsToolSearch {
		return
	}
	// Other extensions' tools of the same names cannot reach MCP tools, so never activate them.
	all := e.host.GetAllTools()
	hasCodemode := slices.ContainsFunc(all, e.options.IsCodemodeTool)
	hasToolSearch := slices.ContainsFunc(all, e.options.IsToolSearchTool)
	active := e.host.GetActiveTools()
	var activate []string
	if needsCodemode && hasCodemode && autoEnable && !slices.Contains(active, CodemodeToolName) {
		activate = append(activate, CodemodeToolName)
	}
	if needsToolSearch && hasToolSearch && !slices.Contains(active, ToolSearchToolName) {
		activate = append(activate, ToolSearchToolName)
	}
	if len(activate) > 0 {
		e.host.SetActiveTools(append(slices.Clone(active), activate...))
	}
	reachable := append(slices.Clone(active), activate...)
	if hasCodemode && slices.Contains(reachable, CodemodeToolName) {
		return
	}
	if hasToolSearch && slices.Contains(reachable, ToolSearchToolName) {
		return
	}
	e.mu.Lock()
	warned := e.warnedUnreachable
	e.warnedUnreachable = true
	e.mu.Unlock()
	if warned {
		return
	}
	reason := ""
	if needsCodemode && hasCodemode && !autoEnable {
		reason = " (autoEnableCodemode is false)"
	}
	ctx.notify(fmt.Sprintf("MCP tools are only reachable from the codemode or tool_search tool, but neither is active%s; they cannot be called.", reason), "warning")
}

// ---------------------------------------------------------------------------------------
// Connections
// ---------------------------------------------------------------------------------------

func (e *Extension) storedTokens(connection *Connection) string {
	url := connection.OAuthURL()
	if url == "" {
		return "null"
	}
	e.mu.Lock()
	credentials := e.credentials
	e.mu.Unlock()
	if credentials == nil {
		return "null"
	}
	tokens := credentials.Tokens(url)
	if tokens == nil {
		return "null"
	}
	data, _ := json.Marshal(tokens)
	return string(data)
}

func (e *Extension) onConnectionChange(connection *Connection) {
	state := connection.State()
	e.mu.Lock()
	_, tracked := e.tokensAtSignIn[connection]
	e.mu.Unlock()
	if state != StateNeedsAuth {
		e.mu.Lock()
		delete(e.tokensAtSignIn, connection)
		e.mu.Unlock()
	} else if !tracked {
		tokens := e.storedTokens(connection)
		e.mu.Lock()
		e.tokensAtSignIn[connection] = tokens
		e.mu.Unlock()
	}
	e.emitChange()
}

// reconnectSignedIn reconnects servers that need a sign-in when their
// credentials were stored since.
func (e *Extension) reconnectSignedIn(ctx EventContext) {
	e.mu.Lock()
	var signedIn []*Connection
	for connection, tokens := range e.tokensAtSignIn {
		if e.storedTokensLocked(connection) != tokens {
			signedIn = append(signedIn, connection)
		}
	}
	for _, connection := range signedIn {
		delete(e.tokensAtSignIn, connection)
	}
	e.mu.Unlock()
	if len(signedIn) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, connection := range signedIn {
		wg.Go(func() { _ = connection.Reconnect(context.Background()) })
	}
	wg.Wait()
	e.ensureDiscoveryActive(ctx)
}

// storedTokensLocked is storedTokens with e.mu held.
func (e *Extension) storedTokensLocked(connection *Connection) string {
	url := connection.OAuthURL()
	if url == "" || e.credentials == nil {
		return "null"
	}
	tokens := e.credentials.Tokens(url)
	if tokens == nil {
		return "null"
	}
	data, _ := json.Marshal(tokens)
	return string(data)
}

// createConnection creates the server's connection.
func (e *Extension) createConnection(s *server) (*Connection, error) {
	e.mu.Lock()
	cwd := e.sessionCwd
	e.mu.Unlock()
	createTransport := e.options.CreateTransport
	if createTransport == nil {
		createTransport = CreateDefaultTransport
	}
	connection, err := NewConnection(ConnectionOptions{
		Entry:           s.entry,
		Cwd:             cwd,
		CreateTransport: createTransport,
		Credentials:     e.getCredentials(),
		ProviderToken: func(ctx context.Context, provider string) string {
			e.mu.Lock()
			token := e.providerToken
			e.mu.Unlock()
			if token == nil {
				return ""
			}
			return token(ctx, provider)
		},
		Log:           e.getServerLog(),
		OnTools:       e.registerTools,
		OnChange:      e.onConnectionChange,
		ClientName:    e.options.ClientName,
		ClientVersion: e.options.ClientVersion,
	})
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	s.connection = connection
	e.mu.Unlock()
	e.emitChange()
	return connection, nil
}

// startConnection connects the server in the background on a goroutine the extension owns and drains in
// [Extension.SessionShutdown]. The server's ready channel, set before it returns, is closed when the server connected
// or failed; failures show in its state. isCurrent stops the connection when the session ended meanwhile. The returned
// channel is that ready channel.
//
// Ports packages/coding-agent/src/extensions/mcp/index.ts (startConnection).
func (e *Extension) startConnection(ctx EventContext, s *server, isCurrent func() bool) <-chan struct{} {
	ready := make(chan struct{})
	e.mu.Lock()
	s.ready = ready
	e.background.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.background.Done()
		defer close(ready)
		if !isCurrent() {
			return
		}
		connection, err := e.createConnection(s)
		if err != nil {
			ctx.notify("MCP failed to load: "+err.Error(), "error")
			return
		}
		if !isCurrent() {
			return
		}
		_, _ = connection.GetClient(context.Background())
	}()
	return ready
}

// waitForServers waits for servers still connecting until they settle or ctx ends.
//
// Ports packages/coding-agent/src/extensions/mcp/index.ts (waitForServers).
func waitForServers(ctx context.Context, waiting []*server, readyOf func(*server) chan struct{}) {
	for _, s := range waiting {
		ready := readyOf(s)
		if ready == nil {
			continue
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return
		}
	}
}

// readyOf is the channel that closes when the server settled, or nil when it never started connecting.
func (e *Extension) readyOf(s *server) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.ready
}

// describeState is the short state for lists and the startup report. withError
// appends the first line of a failure.
func describeState(s *server, withError bool) string {
	if !s.enabled() {
		return "disabled"
	}
	connection := s.connection
	if connection == nil {
		return "starting"
	}
	switch state := connection.State(); state {
	case StateNeedsAuth:
		return "needs sign-in"
	case StateFailed:
		if withError {
			message := connection.Error()
			if message == "" {
				message = "unknown error"
			}
			return "failed: " + firstLine(message)
		}
		return "failed"
	case StateConnected:
		resourceCount := ""
		if count := len(connection.Resources()); count > 0 {
			resourceCount = " · " + plural(count, "resource")
		}
		return "connected · " + plural(len(connection.Tools()), "tool") + resourceCount
	case StateConnecting:
		return "connecting…"
	default:
		return string(state)
	}
}

// reportProblems is one message for everything that needs the user after
// startup, or only for `only`, servers that connected later.
func (e *Extension) reportProblems(ctx EventContext, only []*server) {
	var lines []string
	e.mu.Lock()
	servers := slices.Clone(e.servers)
	if only == nil {
		for _, err := range e.configErrors {
			lines = append(lines, "config: "+err)
		}
	} else {
		servers = only
	}
	e.mu.Unlock()
	for _, s := range servers {
		if s.connection == nil {
			continue
		}
		if state := s.connection.State(); state == StateNeedsAuth || state == StateFailed {
			lines = append(lines, s.entry.Name+": "+describeState(s, true))
		}
	}
	if len(lines) == 0 {
		return
	}
	indented := make([]string, len(lines))
	for i, line := range lines {
		indented[i] = "  " + line
	}
	ctx.notify("MCP servers need attention:\n"+strings.Join(indented, "\n")+"\nRun /mcp to fix.", "warning")
}

// sourcePath is the path of the extension that registered a tool. A Session reports the source info of a built-in
// extension as a typed value and an extension host reports a decoded JSON object, so both are read by their `path` member.
func sourcePath(tool extension.ToolInfo) string {
	if info, ok := tool.SourceInfo.(map[string]any); ok {
		path, _ := info["path"].(string)
		return path
	}
	if tool.SourceInfo == nil {
		return ""
	}
	encoded, err := json.Marshal(tool.SourceInfo)
	if err != nil {
		return ""
	}
	var info struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(encoded, &info) != nil {
		return ""
	}
	return info.Path
}

// saveConfig saves a config change; it returns an error message when the file
// could not be updated. Changes to registered servers only apply to the
// current session.
func (e *Extension) saveConfig(s *server, patch McpServerConfigPatch) string {
	e.mu.Lock()
	entry := s.entry
	e.mu.Unlock()
	if entry.Scope != "extension" {
		update := e.options.UpdateConfig
		if update == nil {
			update = func(entry McpServerEntry, patch McpServerConfigPatch) error {
				return UpdateMcpServerConfig(entry.Source, entry.Name, patch)
			}
		}
		if err := update(entry, patch); err != nil {
			return fmt.Sprintf("Could not update %s: %s", entry.Source, err)
		}
	}
	config := entry.Config
	if patch.Enabled != nil {
		config.Enabled = patch.Enabled
	}
	if patch.Exposure != "" {
		config.Exposure = patch.Exposure
	}
	e.mu.Lock()
	s.entry.Config = config
	e.mu.Unlock()
	return ""
}

// ---------------------------------------------------------------------------------------
// Actions for the `/mcp` command, the manager, and `pi mcp`
// ---------------------------------------------------------------------------------------

// ServerView is a snapshot of one configured server.
type ServerView struct {
	Entry McpServerEntry
	// Connection is nil for a disabled server.
	Connection *Connection
	Enabled    bool
	// Message is the result of the last action that failed.
	Message string
}

// Servers returns the configured servers, in order.
func (e *Extension) Servers() []ServerView {
	e.mu.Lock()
	defer e.mu.Unlock()
	views := make([]ServerView, len(e.servers))
	for i, s := range e.servers {
		views[i] = ServerView{Entry: s.entry, Connection: s.connection, Enabled: s.enabled(), Message: s.message}
	}
	return views
}

// Notices are the config errors and overridden registrations, for the manager.
func (e *Extension) Notices() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var notices []string
	for _, err := range e.configErrors {
		notices = append(notices, "config: "+err)
	}
	for _, line := range e.overridden {
		notices = append(notices, "overridden: "+line)
	}
	return notices
}

// DescribeState is the short state of a server, for lists.
func (e *Extension) DescribeState(name string, withError bool) string {
	s := e.findServer(name)
	if s == nil {
		return ""
	}
	return describeState(s, withError)
}

// SignInPrompt is [McpSignInPrompt].
type SignInPrompt = McpSignInPrompt

// SignIn signs in to a server through the browser flow and reconnects. It
// returns a message describing a failure, or "" on success.
func (e *Extension) SignIn(ctx context.Context, name string, prompt McpSignInPrompt) string {
	s := e.findServer(name)
	if s == nil {
		return fmt.Sprintf(`No MCP server named "%s".`, name)
	}
	e.mu.Lock()
	connection := s.connection
	e.mu.Unlock()
	if connection == nil || connection.OAuthURL() == "" {
		return fmt.Sprintf(`MCP server "%s" does not use OAuth.`, name)
	}
	url := connection.OAuthURL()
	store, err := e.getCredentials().ForServer(url)
	if err != nil {
		return "Sign-in failed: " + err.Error()
	}
	settings, err := connection.OAuthSettings()
	if err != nil {
		return "Sign-in failed: " + err.Error()
	}
	if err := SignInMcpServer(ctx, SignInOptions{
		ServerURL: url, Store: store, Settings: settings, Challenge: connection.Challenge(), Prompt: prompt, AppName: e.options.ClientName,
	}); err != nil {
		if _, cancelled := errors.AsType[*McpSignInCancelledError](err); cancelled {
			return "Sign-in cancelled."
		}
		return "Sign-in failed: " + err.Error()
	}
	// The challenge that asked for this sign-in (for example for more scope) is answered.
	connection.ClearChallenge()
	if err := connection.Reconnect(ctx); err != nil {
		return "Signed in, but " + err.Error()
	}
	return ""
}

// SignOut removes a server's stored credentials and disconnects it. It
// returns whether credentials were stored.
func (e *Extension) SignOut(name string) (bool, error) {
	s := e.findServer(name)
	if s == nil {
		return false, nil
	}
	e.mu.Lock()
	connection := s.connection
	e.mu.Unlock()
	if connection == nil || connection.OAuthURL() == "" {
		return false, nil
	}
	// upstream: packages/coding-agent/src/extensions/mcp/index.ts signOut awaits the credential removal before the
	// connection signs out, so a removal that throws leaves the connection signed in.
	removed, err := e.getCredentials().Remove(connection.OAuthURL())
	if err != nil {
		return false, err
	}
	connection.SignOut()
	return removed, nil
}

// Reconnect connects a server again. It returns a message describing a
// failure, or "" on success.
func (e *Extension) Reconnect(ctx context.Context, name string) string {
	s := e.findServer(name)
	if s == nil {
		return fmt.Sprintf(`No MCP server named "%s".`, name)
	}
	e.mu.Lock()
	connection := s.connection
	e.mu.Unlock()
	if connection == nil {
		return fmt.Sprintf(`MCP server "%s" is disabled.`, name)
	}
	if err := connection.Reconnect(ctx); err != nil {
		return err.Error()
	}
	return ""
}

// SetEnabled enables or disables a server and saves the choice. It returns
// an error message when the config could not be saved; connection errors show
// in the state.
func (e *Extension) SetEnabled(ctx EventContext, name string, enabled bool) string {
	s := e.findServer(name)
	if s == nil {
		return fmt.Sprintf(`No MCP server named "%s".`, name)
	}
	if failed := e.saveConfig(s, McpServerConfigPatch{Enabled: &enabled}); failed != "" {
		return failed
	}
	if !enabled {
		e.mu.Lock()
		connection := s.connection
		s.connection = nil
		e.mu.Unlock()
		e.hideTools(name)
		e.emitChange()
		if connection != nil {
			connection.Close()
		}
		return ""
	}
	<-e.startConnection(ctx, s, func() bool { return true })
	return ""
}

// SetExposure changes a server's exposure and saves the choice. It returns an
// error message when the config could not be saved.
func (e *Extension) SetExposure(name string, exposure extension.McpExposure) string {
	s := e.findServer(name)
	if s == nil {
		return fmt.Sprintf(`No MCP server named "%s".`, name)
	}
	if failed := e.saveConfig(s, McpServerConfigPatch{Exposure: exposure}); failed != "" {
		return failed
	}
	e.mu.Lock()
	connection := s.connection
	e.mu.Unlock()
	if connection != nil && connection.State() == StateConnected {
		e.registerTools(connection)
	}
	e.syncResourceTools()
	// Tools no longer exposed directly leave the declared set; direct tools are activated on registration.
	indirect := map[string]bool{}
	for _, tool := range e.host.GetAllTools() {
		if tool.Exposure != "" && tool.Exposure != extension.ToolExposureDirect {
			indirect[tool.Name] = true
		}
	}
	e.toolMu.Lock()
	owned := e.serverTools[name]
	e.toolMu.Unlock()
	var active []string
	for _, toolName := range e.host.GetActiveTools() {
		if !owned[toolName] || !indirect[toolName] {
			active = append(active, toolName)
		}
	}
	e.host.SetActiveTools(active)
	e.emitChange()
	return ""
}

// SetMessage records the result of a failed action for the manager.
func (e *Extension) SetMessage(name, message string) {
	if s := e.findServer(name); s != nil {
		e.mu.Lock()
		s.message = message
		e.mu.Unlock()
	}
}

// EnsureDiscoveryActive activates the tool that reaches the connected servers'
// tools that are not declared, after a `/mcp` action.
func (e *Extension) EnsureDiscoveryActive(ctx EventContext) { e.ensureDiscoveryActive(ctx) }

// Pending waits for the startup connections, or returns at once when there
// are none.
func (e *Extension) Pending() {
	e.mu.Lock()
	pending := e.pending
	e.mu.Unlock()
	if pending != nil {
		<-pending
	}
}

// FormatStatus is the plain status of every server, for `/mcp` without a
// terminal manager.
func (e *Extension) FormatStatus() string {
	e.mu.Lock()
	servers := slices.Clone(e.servers)
	configErrors := slices.Clone(e.configErrors)
	overridden := slices.Clone(e.overridden)
	e.mu.Unlock()
	if len(servers) == 0 && len(configErrors) == 0 && len(overridden) == 0 {
		return fmt.Sprintf("No MCP servers configured. Add them to %s or %s/mcp.json.", filepath.Join(e.options.AgentDir, "mcp.json"), e.options.ConfigDirName)
	}
	var lines []string
	for _, s := range servers {
		name := s.entry.Name
		exposure := exposureOf(s.entry)
		connection := s.connection
		if connection != nil && connection.State() == StateNeedsAuth {
			lines = append(lines, fmt.Sprintf("%s: needs sign-in, run /mcp login %s (%s)", name, name, exposure))
			continue
		}
		tools := ""
		if connection != nil && connection.State() == StateConnected {
			tools = fmt.Sprintf(", %d tools", len(connection.Tools()))
		}
		var state string
		switch {
		case !s.enabled():
			state = "disabled"
		case connection != nil && connection.State() == StateDisconnected:
			state = "disconnected, reconnects on next call"
		case connection != nil:
			state = string(connection.State())
		default:
			state = "starting"
		}
		errText := ""
		if connection != nil && connection.Error() != "" && connection.State() != StateConnected {
			errText = "\n    " + strings.ReplaceAll(connection.Error(), "\n", "\n    ")
		}
		lines = append(lines, fmt.Sprintf("%s: %s%s (%s)%s", name, state, tools, exposure, errText))
	}
	for _, err := range configErrors {
		lines = append(lines, "config error: "+err)
	}
	for _, line := range overridden {
		lines = append(lines, "overridden: "+line)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------------------

func (e *Extension) loadConfig(ctx EventContext) LoadedMcpConfig {
	if e.options.LoadConfig != nil {
		return e.options.LoadConfig(ctx)
	}
	trusted := false
	if ctx.IsProjectTrusted != nil {
		trusted = ctx.IsProjectTrusted()
	}
	return LoadMcpConfig(LoadOptions{AgentDir: e.options.AgentDir, Cwd: ctx.Cwd, ProjectTrusted: trusted, ConfigDirName: e.options.ConfigDirName})
}

// SessionStart is the `session_start` handler. It loads the configuration and
// connects the enabled servers on a goroutine the extension owns and drains in
// [Extension.SessionShutdown]; the first prompt waits for them in
// [Extension.BeforeAgentStart].
func (e *Extension) SessionStart(ctx EventContext) {
	loaded := e.loadConfig(ctx)
	e.mu.Lock()
	e.configErrors = loaded.Errors
	e.autoEnableCodemode = loaded.AutoEnableCodemode == nil || *loaded.AutoEnableCodemode
	e.warnedUnreachable = false
	e.waitedForStartup = false
	e.sessionCwd = ctx.Cwd
	e.providerToken = ctx.ProviderToken
	e.generation++
	current := e.generation
	e.sessionActive = true
	e.configuredEntries = loaded.Servers
	e.mu.Unlock()
	registered, overridden := e.registeredServers()
	e.mu.Lock()
	e.overridden = overridden
	e.servers = nil
	for _, entry := range loaded.Servers {
		e.servers = append(e.servers, &server{entry: entry})
	}
	e.servers = append(e.servers, registered...)
	var enabled []*server
	for _, s := range e.servers {
		if s.enabled() {
			enabled = append(enabled, s)
		}
	}
	e.mu.Unlock()
	e.emitChange()
	// Codemode or tool_search is activated from the config: the first prompt does not wait for servers whose tools
	// are not declared to the model, and scripts or searches wait for them.
	e.ensureDiscoveryActive(ctx)
	if len(enabled) == 0 {
		e.reportProblems(ctx, nil)
		return
	}
	isCurrent := func() bool { return e.currentGeneration(current) }
	readies := make([]<-chan struct{}, len(enabled))
	for i, s := range enabled {
		readies[i] = e.startConnection(ctx, s, isCurrent)
	}
	pending := make(chan struct{})
	e.mu.Lock()
	e.pending = pending
	e.background.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.background.Done()
		defer close(pending)
		for _, ready := range readies {
			<-ready
		}
		if isCurrent() {
			e.reportProblems(ctx, nil)
		}
	}()
}

func (e *Extension) currentGeneration(generation int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return generation == e.generation
}

// waitForDirectServers makes the first prompt wait for servers whose tools are declared to the model, so they are
// declared in its first request, but not indefinitely: a slow or hanging server must not hold up the prompt. Other
// servers are waited for when a script or search needs them ([Extension.ToolCall]).
func (e *Extension) waitForDirectServers(ctx EventContext) {
	e.mu.Lock()
	if e.waitedForStartup {
		e.mu.Unlock()
		return
	}
	e.waitedForStartup = true
	wait := e.options.StartupWait
	var ready []chan struct{}
	for _, s := range e.servers {
		if s.enabled() && hasDirectTools(s.entry) && s.ready != nil {
			ready = append(ready, s.ready)
		}
	}
	e.mu.Unlock()
	if len(ready) == 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for _, ch := range ready {
		select {
		case <-ch:
		case <-timer.C:
			ctx.notify("MCP servers are still connecting; their tools become available once connected.", "info")
			return
		}
	}
}

// BeforeAgentStart is the `before_agent_start` handler. The first prompt waits for the servers with direct tools
// ([Extension.waitForDirectServers]). Every prompt lists the servers in the `mcp_servers` section as they are when it
// starts; the session appends the section to the conversation when it changed, for example after a server connected.
func (e *Extension) BeforeAgentStart(ctx EventContext, options *extension.BuildSystemPromptOptions) {
	e.waitForDirectServers(ctx)
	if options == nil {
		return
	}
	e.mu.Lock()
	listing := make([]McpServerListing, len(e.servers))
	for i, s := range e.servers {
		listing[i] = McpServerListing{Entry: s.entry}
		if s.connection != nil {
			listing[i].Instructions = s.connection.Instructions()
		}
	}
	e.mu.Unlock()
	section, ok := RenderServersSection(listing)
	setPromptSection(options, McpServersSection, section, ok)
}

// setPromptSection sets the section of a per-run collection (`sections[name] = value`: a new name goes last, an
// existing one keeps its place) or, without a value, deletes it (`delete sections[name]`).
func setPromptSection(options *extension.BuildSystemPromptOptions, name, value string, present bool) {
	if options.Sections == nil {
		if !present {
			return
		}
		options.Sections = &ai.OrderedSections{}
	}
	sections := *options.Sections
	index := slices.IndexFunc(sections, func(section ai.PromptSection) bool { return section.Name == name })
	switch {
	case present && index >= 0:
		sections[index].Value = &value
	case present:
		sections = append(sections, ai.PromptSection{Name: name, Value: &value})
	case index >= 0:
		sections = slices.Delete(sections, index, index+1)
	}
	*options.Sections = sections
}

// scriptNeedsServerPattern matches the codemode helpers that search, enumerate, or describe tools or namespaces, which
// may name a server in other forms than its namespace.
var scriptNeedsServerPattern = lazyregexp.New(`\b(searchTools|describeNamespace|describeTool|ALL_TOOLS)\b`)

// scriptNeedsServer reports whether a codemode script needs the server: it names the server's namespace, or searches,
// enumerates, or describes tools or namespaces.
func scriptNeedsServer(code, server string) bool {
	return scriptNeedsServerPattern.MatchString(code) || strings.Contains(code, extension.McpNamespace(server))
}

// ToolCall is the `tool_call` handler: a codemode script waits for the servers it names, or for every server when it
// searches or enumerates tools, so their tools are registered before the script runs. tool_search and the resource
// tools reach every server, so they wait for all of them. It returns when the servers settled or ctx ends.
//
// Ports packages/coding-agent/src/extensions/mcp/index.ts (pi.on("tool_call")).
func (e *Extension) ToolCall(ctx context.Context, toolName string, input map[string]any) {
	var tool *extension.ToolInfo
	for _, candidate := range e.host.GetAllTools() {
		if candidate.Name == toolName {
			tool = &candidate
			break
		}
	}
	if tool == nil {
		return
	}
	e.mu.Lock()
	var pendingServers []*server
	for _, s := range e.servers {
		if s.enabled() && s.ready != nil && (s.connection == nil || s.connection.State() != StateConnected) {
			pendingServers = append(pendingServers, s)
		}
	}
	e.mu.Unlock()
	if len(pendingServers) == 0 {
		return
	}
	var waiting []*server
	switch {
	case e.options.IsCodemodeTool(*tool):
		source, _ := input["code"].(string)
		for _, s := range pendingServers {
			if scriptNeedsServer(source, s.entry.Name) {
				waiting = append(waiting, s)
			}
		}
	case e.options.IsToolSearchTool(*tool) || resourceToolNames[tool.Name]:
		waiting = pendingServers
	}
	waitForServers(ctx, waiting, e.readyOf)
}

// resourceToolNames are the tools that reach the resources of every server.
var resourceToolNames = map[string]bool{ListMcpResourcesTool: true, ListMcpResourceTemplatesTool: true, ReadMcpResourceTool: true}

// TurnStart is the `turn_start` handler. It picks up sign-ins done outside the
// session, such as `pi mcp login` run by the agent.
func (e *Extension) TurnStart(ctx EventContext) {
	e.mu.Lock()
	waiting := len(e.tokensAtSignIn) > 0
	e.mu.Unlock()
	if waiting {
		e.reconnectSignedIn(ctx)
	}
}

// McpServersChange is the `mcp_servers_change` handler. Servers registered or
// unregistered during the session connect or disconnect right away.
func (e *Extension) McpServersChange(ctx EventContext) {
	e.mu.Lock()
	active := e.sessionActive
	current := e.generation
	e.mu.Unlock()
	if !active {
		return
	}
	registered, overridden := e.registeredServers()
	next := map[string]*server{}
	for _, s := range registered {
		next[s.entry.Name] = s
	}
	e.mu.Lock()
	e.overridden = overridden
	// Unregistered servers and re-registered ones with a new config are dropped; the latter come back below.
	var removed []*server
	for _, s := range e.servers {
		if s.entry.Scope != "extension" {
			continue
		}
		if n, ok := next[s.entry.Name]; !ok || n.registeredConfig != s.registeredConfig {
			removed = append(removed, s)
		}
	}
	e.servers = slices.DeleteFunc(e.servers, func(s *server) bool { return slices.Contains(removed, s) })
	e.mu.Unlock()
	for _, s := range removed {
		e.hideTools(s.entry.Name)
	}
	var added []*server
	e.mu.Lock()
	for _, s := range registered {
		if e.findServerLocked(s.entry.Name) == nil {
			added = append(added, s)
		}
	}
	e.servers = append(e.servers, added...)
	e.mu.Unlock()
	e.emitChange()
	e.ensureDiscoveryActive(ctx)
	var closing sync.WaitGroup
	for _, s := range removed {
		e.mu.Lock()
		connection := s.connection
		e.mu.Unlock()
		if connection != nil {
			closing.Go(connection.Close)
		}
	}
	closing.Wait()
	var connecting []*server
	for _, s := range added {
		if s.enabled() {
			connecting = append(connecting, s)
		}
	}
	if !e.currentGeneration(current) || len(connecting) == 0 {
		return
	}
	isCurrent := func() bool { return e.currentGeneration(current) }
	readies := make([]<-chan struct{}, len(connecting))
	for i, s := range connecting {
		readies[i] = e.startConnection(ctx, s, isCurrent)
	}
	for _, ready := range readies {
		<-ready
	}
	if !isCurrent() {
		// The session ended meanwhile: close what connected.
		var wg sync.WaitGroup
		for _, s := range connecting {
			e.mu.Lock()
			connection := s.connection
			e.mu.Unlock()
			if connection != nil {
				wg.Go(connection.Close)
			}
		}
		wg.Wait()
		return
	}
	e.reportProblems(ctx, connecting)
}

// SessionShutdown is the `session_shutdown` handler: it closes every
// connection and waits for the extension's background work.
func (e *Extension) SessionShutdown() {
	e.mu.Lock()
	e.sessionActive = false
	e.generation++
	var closing []*Connection
	for _, s := range e.servers {
		if s.connection != nil {
			closing = append(closing, s.connection)
		}
	}
	e.servers = nil
	e.mu.Unlock()
	e.emitChange()
	var wg sync.WaitGroup
	for _, connection := range closing {
		wg.Go(connection.Close)
	}
	wg.Wait()
	e.background.Wait()
}

// countOf is the number of times value occurs in list.
func countOf(list []string, value string) int {
	n := 0
	for _, item := range list {
		if item == value {
			n++
		}
	}
	return n
}

// lane returns the call lane of a server. The caller holds toolMu.
func (e *Extension) lane(server string) *extension.CallLane {
	lane := e.lanes[server]
	if lane == nil {
		lane = &extension.CallLane{}
		e.lanes[server] = lane
	}
	return lane
}
