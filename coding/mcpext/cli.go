package mcpext

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Ports packages/coding-agent/src/extensions/mcp/cli.ts.
//
// `pi mcp`: add, remove, and check MCP servers and sign in to them outside a
// session. Agents run it through bash to configure servers, verify an
// `mcp.json` they wrote, and start an OAuth sign-in; the user only approves
// access in the browser. Running sessions pick up new credentials on their next
// turn.

// McpCommandOptions configure [RunMcpCommand].
type McpCommandOptions struct {
	Cwd      string
	AgentDir string
	// AppName and ConfigDirName are the CLI name and the per-project configuration directory, `pi` and `.pi` upstream. Defaults: "pig" and ".pig".
	AppName       string
	ConfigDirName string
	// Credentials defaults to `mcp-auth.json` in AgentDir.
	Credentials *McpOAuthCredentialStore
	// OpenURL opens the sign-in URL in the platform browser. Nil opens nothing.
	OpenURL func(url string)
	// IsProjectTrusted reports whether the project at cwd is trusted; an error ends the command with exit code 1. Nil means untrusted.
	IsProjectTrusted func(cwd string) (bool, error)
	// Interactive lets `login` read a pasted redirect URL from Stdin. Upstream's condition is a terminal on stdin and no injected URL opener, which the caller decides.
	Interactive bool
	// Stdin is where a pasted redirect URL is read from; PromptOutput receives the question. Defaults: standard input and standard error.
	Stdin        io.Reader
	PromptOutput io.Writer
	// Color styles the help text and hints as a terminal renders chalk.bold and chalk.dim.
	Color bool
	// Log and Error receive the output lines. Defaults: standard output and standard error.
	Log   func(line string)
	Error func(line string)
}

// upstream: packages/coding-agent/src/extensions/mcp/cli.ts:DEFAULT_LOGIN_TIMEOUT_SECONDS
const defaultLoginTimeoutSeconds = 300

// maxTimerDelay is the largest delay of a Node timer: a longer one fires after 1 ms instead (Node's TimeoutOverflowWarning).
const maxTimerDelay = 2147483647

type mcpCLI struct {
	options McpCommandOptions
	app     string
	dir     string
	log     func(string)
	error   func(string)
}

func (c *mcpCLI) bold(text string) string {
	if c.options.Color {
		return "\x1b[1m" + text + "\x1b[22m"
	}
	return text
}

func (c *mcpCLI) dim(text string) string {
	if c.options.Color {
		return "\x1b[2m" + text + "\x1b[22m"
	}
	return text
}

func (c *mcpCLI) helpHint() string {
	return c.dim(fmt.Sprintf(`Use "%s mcp --help" for usage.`, c.app))
}

func (c *mcpCLI) help() string {
	text := `%[1]s
  %[2]s mcp add <server> [options] -- <command> [args...]
  %[2]s mcp add <server> [options] --url <url>
  %[2]s mcp remove <server> [-l]
  %[2]s mcp list [--json]
  %[2]s mcp login <server> [--timeout <seconds>]
  %[2]s mcp logout <server>

Configure and check MCP servers and sign in to OAuth servers without starting a session.
Reads ~/%[3]s/agent/mcp.json and, in trusted projects, %[3]s/mcp.json.

Commands:
  add <server>            Add or replace a server in mcp.json
  remove <server>         Remove a server from mcp.json
  list                    Show state, tools, and errors (exits 1 on failure)
  login <server>          Sign in through the browser
  logout <server>         Delete the stored OAuth credentials

Options for add and remove:
  -l, --local             Use %[3]s/mcp.json in the current project instead of the global file

Options for add:
  --url <url>             Streamable HTTP server URL (instead of a command)
  --env <KEY=VALUE>       Environment variable for a stdio server (repeatable)
  --cwd <dir>             Working directory for a stdio server
  --header <KEY=VALUE>    HTTP header (repeatable)
  --bearer-token-env-var <NAME>
                          Send "Authorization: Bearer ${NAME}"
  --oauth-client-id <id>  Pre-registered OAuth client id
  --oauth-client-secret <secret>
                          OAuth client secret (may be ${NAME} or !command)
  --oauth-callback-port <port>
                          Fixed OAuth callback port
  --oauth-client-name <name>
                          Client name sent when registering with the OAuth server
  --exposure <mode>       codemode (default), deferred, direct, or hidden
  --description <text>    What the server offers, shown in the system prompt

Other options:
  --json                  Print the list as JSON
  --timeout <seconds>     How long login waits for the browser (default: 300)`
	return fmt.Sprintf(text, c.bold("Usage:"), c.app, c.dir)
}

// serverReport is the state of one server as `list` prints it.
type serverReport struct {
	name   string
	scope  string
	source string
	// override is the project `mcp.json` that overrides `enabled`, `exposure`, or `toolExposure` of this global server.
	override  string
	enabled   bool
	exposure  string
	transport string
	state     string
	tools     []string
	// toolExposure lists the tools whose exposure differs from the server's, in tool order.
	toolExposure      [][2]string
	resources         *int
	resourceTemplates *int
	err               string
}

func (r serverReport) toolExposureOf(tool string) (string, bool) {
	for _, pair := range r.toolExposure {
		if pair[0] == tool {
			return pair[1], true
		}
	}
	return "", false
}

func (c *mcpCLI) newConnection(entry McpServerEntry, credentials *McpOAuthCredentialStore) (*Connection, error) {
	return NewConnection(ConnectionOptions{
		Entry:           entry,
		Cwd:             c.options.Cwd,
		CreateTransport: CreateDefaultTransport,
		Credentials:     credentials,
		Log:             NewMcpServerLog(filepath.Join(c.options.AgentDir, "mcp.log")),
		OnTools:         func(*Connection) {},
	})
}

type optionKind int

const (
	optionFlag optionKind = iota + 1
	optionValue
	optionList
)

// optionAliases are the short spellings of options. `-l`/`--local` match `pi install`.
var optionAliases = map[string]string{"-l": "--local"}

type parsedOptions struct {
	positional []string
	values     map[string]string
	flags      map[string]bool
	// lists holds the values of `list` options, in order.
	lists map[string][]string
}

func (p parsedOptions) has(name string) bool {
	_, isValue := p.values[name]
	return isValue || p.flags[name]
}

// parseOptions parses `--name value` options; it returns false after reporting an unknown one. `--` ends the options, as does reaching maxPositionals positional arguments: the remaining arguments are positional, so a command's own options (`add <server> <command> --flag`) are passed through.
func (c *mcpCLI) parseOptions(args []string, known map[string]optionKind, maxPositionals int) (parsedOptions, bool) {
	parsed := parsedOptions{values: map[string]string{}, flags: map[string]bool{}, lists: map[string][]string{}}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if alias, ok := optionAliases[arg]; ok {
			arg = alias
		}
		if arg == "--" || len(parsed.positional) >= maxPositionals {
			rest := args[index:]
			if arg == "--" {
				rest = args[index+1:]
			}
			parsed.positional = append(parsed.positional, rest...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			parsed.positional = append(parsed.positional, arg)
			continue
		}
		name := arg[2:]
		kind, ok := known[name]
		if !ok {
			c.error(fmt.Sprintf("Unknown option %s.\n%s", arg, c.helpHint()))
			return parsedOptions{}, false
		}
		if kind == optionFlag {
			parsed.flags[name] = true
			continue
		}
		index++
		if index >= len(args) {
			c.error(arg + " needs a value.")
			return parsedOptions{}, false
		}
		if kind == optionList {
			parsed.lists[name] = append(parsed.lists[name], args[index])
		} else {
			parsed.values[name] = args[index]
		}
	}
	return parsed, true
}

// RunMcpCommand runs `mcp <args>` and returns the exit code.
func RunMcpCommand(ctx context.Context, args []string, options McpCommandOptions) int {
	c := &mcpCLI{options: options, app: options.AppName, dir: options.ConfigDirName, log: options.Log, error: options.Error}
	if c.app == "" {
		c.app = "pig"
	}
	if c.dir == "" {
		c.dir = ".pig"
	}
	if c.log == nil {
		c.log = func(line string) { _, _ = fmt.Fprintln(os.Stdout, line) }
	}
	if c.error == nil {
		c.error = func(line string) { _, _ = fmt.Fprintln(os.Stderr, line) }
	}
	if len(args) == 0 || args[0] == "help" || slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		c.log(c.help())
		return 0
	}
	command, rest := args[0], args[1:]
	projectConfig := filepath.Join(options.Cwd, c.dir, "mcp.json")
	switch command {
	case "add":
		return c.add(rest, projectConfig)
	case "remove":
		return c.remove(rest, projectConfig)
	}
	projectTrusted, err := c.trusted()
	if err != nil {
		c.error(err.Error())
		return 1
	}
	loaded := LoadMcpConfig(LoadOptions{AgentDir: options.AgentDir, Cwd: options.Cwd, ProjectTrusted: projectTrusted, ConfigDirName: c.dir})
	untrustedNote := ""
	if !projectTrusted && fileExists(projectConfig) {
		untrustedNote = fmt.Sprintf("%s is ignored because the project is not trusted. Start %s in the project to trust it.", projectConfig, c.app)
	}
	credentials := options.Credentials
	if credentials == nil {
		credentials = NewMcpOAuthCredentialStore(options.AgentDir)
	}

	switch command {
	case "list":
		parsed, ok := c.parseOptions(rest, map[string]optionKind{"json": optionFlag}, math.MaxInt)
		if !ok {
			return 1
		}
		if len(parsed.positional) > 0 {
			c.error(fmt.Sprintf("Usage: %s mcp list [--json]\n%s", c.app, c.helpHint()))
			return 1
		}
		return c.list(ctx, loaded, parsed.flags["json"], untrustedNote, credentials)
	case "login", "logout":
		known := map[string]optionKind{}
		if command == "login" {
			known["timeout"] = optionValue
		}
		parsed, ok := c.parseOptions(rest, known, math.MaxInt)
		if !ok {
			return 1
		}
		if len(parsed.positional) == 0 || parsed.positional[0] == "" || len(parsed.positional) > 1 {
			c.error(fmt.Sprintf("Usage: %s mcp %s <server>\n%s", c.app, command, c.helpHint()))
			return 1
		}
		name := parsed.positional[0]
		var entry *McpServerEntry
		names := make([]string, len(loaded.Servers))
		for i := range loaded.Servers {
			names[i] = loaded.Servers[i].Name
			if entry == nil && loaded.Servers[i].Name == name {
				entry = &loaded.Servers[i]
			}
		}
		if entry == nil {
			note := ""
			if untrustedNote != "" {
				note = " " + untrustedNote
			}
			configured := strings.Join(names, ", ")
			if configured == "" {
				configured = "none"
			}
			c.error(fmt.Sprintf(`No MCP server named "%s".%s Configured: %s.`, name, note, configured))
			return 1
		}
		connection, err := c.newConnection(*entry, credentials)
		if err != nil {
			c.error(err.Error())
			return 1
		}
		defer connection.Close()
		serverURL := connection.OAuthURL()
		if serverURL == "" {
			c.error(fmt.Sprintf(`MCP server "%s" does not use OAuth. Only HTTP servers without an Authorization header do.`, name))
			return 1
		}
		if command == "logout" {
			removed, err := credentials.Remove(name, serverURL)
			if err != nil {
				c.error(err.Error())
				return 1
			}
			if removed {
				c.log(fmt.Sprintf(`Signed out of MCP server "%s".`, name))
			} else {
				c.log(fmt.Sprintf(`No stored credentials for MCP server "%s".`, name))
			}
			return 0
		}
		timeoutSeconds := float64(defaultLoginTimeoutSeconds)
		if value, ok := parsed.values["timeout"]; ok {
			timeoutSeconds = jsnumber.Parse(value)
		}
		if math.IsNaN(timeoutSeconds) || math.IsInf(timeoutSeconds, 0) || timeoutSeconds <= 0 {
			c.error("--timeout must be a positive number of seconds.")
			return 1
		}
		return c.login(ctx, *entry, connection, serverURL, timeoutSeconds*1000, credentials)
	}
	c.error(fmt.Sprintf("Unknown mcp command \"%s\".\n%s", command, c.helpHint()))
	return 1
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (c *mcpCLI) trusted() (bool, error) {
	if c.options.IsProjectTrusted == nil {
		return false, nil
	}
	return c.options.IsProjectTrusted(c.options.Cwd)
}

// parsePairs parses the `KEY=VALUE` pairs of a repeatable option into an object in insertion order. It returns false after reporting a malformed pair.
func (c *mcpCLI) parsePairs(option string, pairs []string) (*orderedjson.Object, bool) {
	record := orderedjson.New()
	for _, pair := range pairs {
		separator := strings.Index(pair, "=")
		if separator <= 0 {
			c.error(fmt.Sprintf(`--%s expects KEY=VALUE, got "%s".`, option, pair))
			return nil, false
		}
		_ = record.SetValue(pair[:separator], pair[separator+1:])
	}
	return record, true
}

func (c *mcpCLI) add(args []string, projectConfig string) int {
	usage := fmt.Sprintf("Usage: %s mcp add <server> [options] (--url <url> | -- <command> [args...])\n%s", c.app, c.helpHint())
	parsed, ok := c.parseOptions(args, map[string]optionKind{
		"local":                optionFlag,
		"url":                  optionValue,
		"env":                  optionList,
		"cwd":                  optionValue,
		"header":               optionList,
		"bearer-token-env-var": optionValue,
		"oauth-client-id":      optionValue,
		"oauth-client-secret":  optionValue,
		"oauth-callback-port":  optionValue,
		"oauth-client-name":    optionValue,
		"exposure":             optionValue,
		"description":          optionValue,
	}, 2)
	if !ok {
		return 1
	}
	var name string
	var command []string
	if len(parsed.positional) > 0 {
		name, command = parsed.positional[0], parsed.positional[1:]
	}
	serverURL, hasURL := parsed.values["url"]
	if name == "" || !hasURL == (len(command) == 0) {
		c.error(usage)
		return 1
	}
	httpOnly := []string{"header", "bearer-token-env-var", "oauth-client-id", "oauth-client-secret", "oauth-callback-port", "oauth-client-name"}
	stdioOnly := []string{"env", "cwd"}
	misplacedOptions := httpOnly
	if hasURL {
		misplacedOptions = stdioOnly
	}
	for _, option := range misplacedOptions {
		if _, isList := parsed.lists[option]; parsed.has(option) || isList {
			applies := "stdio servers"
			if !hasURL {
				applies = "HTTP servers (--url)"
			}
			c.error(fmt.Sprintf("--%s only applies to %s.", option, applies))
			return 1
		}
	}

	config := orderedjson.New()
	if hasURL {
		headers, ok := c.parsePairs("header", parsed.lists["header"])
		if !ok {
			return 1
		}
		if bearer, ok := parsed.values["bearer-token-env-var"]; ok {
			_ = headers.SetValue("Authorization", "Bearer ${"+bearer+"}")
		}
		oauth := orderedjson.New()
		if clientID, ok := parsed.values["oauth-client-id"]; ok {
			_ = oauth.SetValue("clientId", clientID)
		}
		if secret, ok := parsed.values["oauth-client-secret"]; ok {
			_ = oauth.SetValue("clientSecret", secret)
		}
		if port, ok := parsed.values["oauth-callback-port"]; ok {
			oauth.Set("callbackPort", jsnumber.JSON(jsnumber.Parse(port)))
		}
		if clientName, ok := parsed.values["oauth-client-name"]; ok {
			_ = oauth.SetValue("clientName", clientName)
		}
		_ = config.SetValue("url", serverURL)
		if headers.Len() > 0 {
			config.Set("headers", mustMarshal(headers))
		}
		if oauth.Len() > 0 {
			config.Set("oauth", mustMarshal(oauth))
		}
	} else {
		env, ok := c.parsePairs("env", parsed.lists["env"])
		if !ok {
			return 1
		}
		_ = config.SetValue("command", command[0])
		if len(command) > 1 {
			_ = config.SetValue("args", command[1:])
		}
		if env.Len() > 0 {
			config.Set("env", mustMarshal(env))
		}
		if cwd, ok := parsed.values["cwd"]; ok {
			_ = config.SetValue("cwd", cwd)
		}
	}
	if exposure, ok := parsed.values["exposure"]; ok {
		_ = config.SetValue("exposure", exposure)
	}
	if description, ok := parsed.values["description"]; ok {
		_ = config.SetValue("description", description)
	}
	raw := mustMarshal(config)
	validated, message := extension.ValidateMcpServerConfig(name, raw)
	if message != "" {
		c.error(message)
		return 1
	}
	// The saved entry is the validated copy, whose exposure aliases are resolved in place (cli.ts addMcpServerConfig(path, name, validated)).
	if _, ok := parsed.values["exposure"]; ok {
		_ = config.SetValue("exposure", string(validated.Exposure))
		raw = mustMarshal(config)
	}

	project := parsed.flags["local"]
	path := filepath.Join(c.options.AgentDir, "mcp.json")
	scope := "global"
	if project {
		path, scope = projectConfig, "project"
	}
	replaced, err := AddMcpServerConfigJSON(path, name, raw)
	if err != nil {
		c.error(fmt.Sprintf("Could not update %s: %s", path, err))
		return 1
	}
	verb := "Added"
	if replaced {
		verb = "Replaced"
	}
	c.log(fmt.Sprintf(`%s %s MCP server "%s" in %s.`, verb, scope, name, path))
	if project {
		trusted, err := c.trusted()
		if err != nil {
			c.error(err.Error())
			return 1
		}
		if !trusted {
			c.log(fmt.Sprintf("The project is not trusted, so %s is ignored until you start %s in the project and trust it.", path, c.app))
		}
	}
	// HTTP servers without an Authorization header may use OAuth.
	mayNeedSignIn := validated.IsHTTP()
	if validated.Headers != nil {
		for _, header := range validated.Headers.Keys() {
			if strings.EqualFold(header, "authorization") {
				mayNeedSignIn = false
			}
		}
	}
	signIn := ""
	if mayNeedSignIn {
		signIn = fmt.Sprintf(". If it requires sign-in: %s mcp login %s", c.app, name)
	}
	c.log(fmt.Sprintf("Check it with: %s mcp list%s", c.app, signIn))
	return 0
}

// mustMarshal is the JSON text of an object whose members are all valid JSON, as every object this file builds is.
func mustMarshal(object *orderedjson.Object) json.RawMessage {
	raw, _ := object.MarshalJSON()
	return raw
}

func (c *mcpCLI) remove(args []string, projectConfig string) int {
	parsed, ok := c.parseOptions(args, map[string]optionKind{"local": optionFlag}, math.MaxInt)
	if !ok {
		return 1
	}
	if len(parsed.positional) == 0 || parsed.positional[0] == "" || len(parsed.positional) > 1 {
		c.error(fmt.Sprintf("Usage: %s mcp remove <server> [-l]\n%s", c.app, c.helpHint()))
		return 1
	}
	name := parsed.positional[0]
	project := parsed.flags["local"]
	path := filepath.Join(c.options.AgentDir, "mcp.json")
	scope := "global"
	if project {
		path, scope = projectConfig, "project"
	}
	removed, err := RemoveMcpServerConfig(path, name)
	if err != nil {
		c.error(fmt.Sprintf("Could not update %s: %s", path, err))
		return 1
	}
	if removed {
		c.log(fmt.Sprintf(`Removed %s MCP server "%s" from %s.`, scope, name, path))
		return 0
	}
	other := ""
	for _, server := range LoadMcpConfig(LoadOptions{AgentDir: c.options.AgentDir, Cwd: c.options.Cwd, ProjectTrusted: true, ConfigDirName: c.dir}).Servers {
		if server.Name == name && server.Scope != scope {
			hint := "omit --local"
			if server.Scope == "project" {
				hint = "use --local"
			}
			other = fmt.Sprintf(" It is defined in %s; %s.", server.Source, hint)
			break
		}
	}
	c.error(fmt.Sprintf(`No %s MCP server named "%s" in %s.%s`, scope, name, path, other))
	return 1
}

// report connects to one enabled server and records what `list` prints.
func (c *mcpCLI) report(ctx context.Context, entry McpServerEntry, credentials *McpOAuthCredentialStore) serverReport {
	config := entry.Config
	scope := entry.Scope
	if scope == "" {
		scope = "global"
	}
	report := serverReport{
		name: entry.Name, scope: scope, source: entry.Source, override: entry.Override,
		enabled:   config.Enabled == nil || *config.Enabled,
		exposure:  string(exposureOf(entry)),
		transport: describeTransport(entry),
		state:     "disabled",
		tools:     []string{},
	}
	if !report.enabled {
		return report
	}
	connection, err := c.newConnection(entry, credentials)
	if err != nil {
		report.state, report.err = string(StateFailed), err.Error()
		return report
	}
	// The connection records the state and error.
	_, _ = connection.GetClient(ctx)
	report.state = string(connection.State())
	for _, tool := range connection.Tools() {
		report.tools = append(report.tools, tool.Name)
		if exposure := string(extension.GetMcpToolExposure(config, tool.Name)); exposure != report.exposure {
			report.toolExposure = append(report.toolExposure, [2]string{tool.Name, exposure})
		}
	}
	if connection.HasResources() {
		resources, templates := len(connection.Resources()), len(connection.ResourceTemplates())
		report.resources, report.resourceTemplates = &resources, &templates
	}
	if report.state != string(StateConnected) {
		report.err = connection.Error()
	}
	connection.Close()
	return report
}

func (r serverReport) jsonObject() *orderedjson.Object {
	object := orderedjson.New()
	_ = object.SetValue("name", r.name)
	_ = object.SetValue("scope", r.scope)
	_ = object.SetValue("source", r.source)
	if r.override != "" {
		_ = object.SetValue("override", r.override)
	}
	_ = object.SetValue("enabled", r.enabled)
	_ = object.SetValue("exposure", r.exposure)
	_ = object.SetValue("transport", r.transport)
	_ = object.SetValue("state", r.state)
	_ = object.SetValue("tools", r.tools)
	if len(r.toolExposure) > 0 {
		overrides := orderedjson.New()
		for _, pair := range r.toolExposure {
			_ = overrides.SetValue(pair[0], pair[1])
		}
		object.Set("toolExposure", mustMarshal(overrides))
	}
	if r.resources != nil {
		_ = object.SetValue("resources", *r.resources)
		_ = object.SetValue("resourceTemplates", *r.resourceTemplates)
	}
	if r.err != "" {
		_ = object.SetValue("error", r.err)
	}
	return object
}

func (c *mcpCLI) list(ctx context.Context, loaded LoadedMcpConfig, asJSON bool, untrustedNote string, credentials *McpOAuthCredentialStore) int {
	reports := make([]serverReport, len(loaded.Servers))
	var wg sync.WaitGroup
	for i, entry := range loaded.Servers {
		wg.Go(func() { reports[i] = c.report(ctx, entry, credentials) })
	}
	wg.Wait()
	failed := len(loaded.Errors) > 0
	for _, report := range reports {
		if report.enabled && report.state != string(StateConnected) {
			failed = true
		}
	}

	if asJSON {
		servers := make([]json.RawMessage, len(reports))
		for i, report := range reports {
			servers[i] = mustMarshal(report.jsonObject())
		}
		document := orderedjson.New()
		_ = document.SetValue("servers", servers)
		errorsList := loaded.Errors
		if errorsList == nil {
			errorsList = []string{}
		}
		_ = document.SetValue("errors", errorsList)
		if untrustedNote != "" {
			_ = document.SetValue("note", untrustedNote)
		}
		// JSON.stringify(value, null, 2)
		canonical, err := jsonstringify.Canonicalize(mustMarshal(document))
		var indented bytes.Buffer
		if err == nil {
			err = json.Indent(&indented, canonical, "", "  ")
		}
		if err != nil {
			c.error(err.Error())
			return 1
		}
		c.log(indented.String())
		return exitCode(failed)
	}
	if len(reports) == 0 && len(loaded.Errors) == 0 {
		c.log(fmt.Sprintf("No MCP servers configured. Add them to %s or %s/mcp.json.", filepath.Join(c.options.AgentDir, "mcp.json"), c.dir))
	}
	for _, report := range reports {
		var state string
		switch report.state {
		case string(StateConnected):
			plural := "s"
			if len(report.tools) == 1 {
				plural = ""
			}
			state = fmt.Sprintf("connected, %d tool%s", len(report.tools), plural)
		case string(StateNeedsAuth):
			state = "needs sign-in"
		default:
			state = report.state
		}
		c.log(fmt.Sprintf("%s: %s (%s, %s)", report.name, state, report.exposure, report.scope))
		c.log("  " + report.transport)
		if report.override != "" {
			c.log("  project override: " + report.override)
		}
		if report.state == string(StateNeedsAuth) {
			c.log(fmt.Sprintf("  sign in with: %s mcp login %s", c.app, report.name))
		}
		if len(report.tools) > 0 {
			tools := make([]string, len(report.tools))
			for i, tool := range report.tools {
				tools[i] = tool
				if exposure, ok := report.toolExposureOf(tool); ok && exposure != "" {
					tools[i] = fmt.Sprintf("%s [%s]", tool, exposure)
				}
			}
			c.log("  tools: " + strings.Join(tools, ", "))
		}
		if report.resources != nil {
			c.log(fmt.Sprintf("  resources: %d, URI templates: %d", *report.resources, *report.resourceTemplates))
		}
		if report.err != "" {
			c.log("  " + strings.Join(strings.Split(report.err, "\n"), "\n  "))
		}
	}
	for _, configError := range loaded.Errors {
		c.log("config error: " + configError)
	}
	if untrustedNote != "" {
		c.log(untrustedNote)
	}
	return exitCode(failed)
}

func exitCode(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

func (c *mcpCLI) login(ctx context.Context, entry McpServerEntry, connection *Connection, serverURL string, timeoutMs float64, credentials *McpOAuthCredentialStore) int {
	name := entry.Name
	// Connecting first answers whether a sign-in is needed and records the server's challenge.
	if _, err := connection.GetClient(ctx); err == nil {
		c.log(fmt.Sprintf(`Already signed in to MCP server "%s" (%d tools).`, name, len(connection.Tools())))
		return 0
	}
	if connection.State() != StateNeedsAuth {
		reason := connection.Error()
		if reason == "" {
			reason = "unknown error"
		}
		c.error(fmt.Sprintf(`MCP server "%s" failed to connect: %s`, name, reason))
		return 1
	}

	settings, err := connection.OAuthSettings()
	if err != nil {
		c.error(fmt.Sprintf(`Sign-in to MCP server "%s" failed: %s`, name, err))
		return 1
	}
	store, err := credentials.ForServer(name, serverURL)
	if err != nil {
		c.error(fmt.Sprintf(`Sign-in to MCP server "%s" failed: %s`, name, err))
		return 1
	}
	delay := time.Duration(timeoutMs * float64(time.Millisecond))
	if timeoutMs > maxTimerDelay {
		delay = time.Millisecond
	}
	// --timeout limits the whole sign-in, including requests to the authorization server (cli.ts, #10565).
	signInCtx, cancelSignIn := context.WithTimeout(ctx, delay)
	defer cancelSignIn()
	err = SignInMcpServer(signInCtx, SignInOptions{
		ServerURL: serverURL,
		Store:     store,
		Settings:  settings,
		Challenge: connection.Challenge(),
		Prompt:    &redirectPrompt{cli: c, name: name},
		AppName:   "pig",
	})
	cancelSignIn()
	if err != nil {
		if _, ok := errors.AsType[*McpSignInCancelledError](err); ok {
			c.error(fmt.Sprintf(`Sign-in to MCP server "%s" was cancelled or not completed within %d seconds.`, name, int64(math.Floor(timeoutMs/1000+0.5))))
		} else {
			c.error(fmt.Sprintf(`Sign-in to MCP server "%s" failed: %s`, name, err))
		}
		return 1
	}
	connection.ClearChallenge()
	if err := connection.Reconnect(ctx); err != nil {
		c.error(fmt.Sprintf("Signed in, but %s", err))
		return 1
	}
	c.log(fmt.Sprintf(`Signed in to MCP server "%s" (%d tools).`, name, len(connection.Tools())))
	return 0
}

// redirectPrompt shows the authorization URL and, in a terminal, accepts the pasted redirect URL; otherwise only the browser callback can finish the sign-in.
type redirectPrompt struct {
	cli  *mcpCLI
	name string
}

func (p *redirectPrompt) ShowAuthorizationURL(authorizationURL *url.URL) {
	p.cli.log(fmt.Sprintf("Sign in to MCP server \"%s\" in your browser:\n%s", p.name, authorizationURL.String()))
	if p.cli.options.OpenURL != nil {
		p.cli.options.OpenURL(authorizationURL.String())
	}
}

// PromptForRedirectURL returns an empty string when ctx ends: the callback arrived, or the sign-in timed out.
func (p *redirectPrompt) PromptForRedirectURL(ctx context.Context) (string, error) {
	if !p.cli.options.Interactive {
		<-ctx.Done()
		return "", nil
	}
	input, output := p.cli.options.Stdin, p.cli.options.PromptOutput
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stderr
	}
	_, _ = io.WriteString(output, "If the browser cannot reach this machine, paste the URL it was redirected to: ")
	answer := make(chan string, 1)
	go func() {
		// The read cannot be interrupted; it ends when the terminal answers or the process exits, as upstream's readline leaves stdin open.
		line, err := bufio.NewReader(input).ReadString('\n')
		if err == nil || line != "" {
			answer <- strings.TrimRight(line, "\r\n")
		}
	}()
	select {
	case line := <-answer:
		return line, nil
	case <-ctx.Done():
		return "", nil
	}
}
