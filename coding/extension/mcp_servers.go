package extension

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/src/core/mcp-servers.ts.
//
// MCP server configuration and the servers extensions register with
// `pi.registerMcpServer()`. The core only validates and stores registrations.
// The MCP extension (built in, or another extension that handles
// `mcp_servers_change`) connects them next to the servers from `mcp.json`.

// McpExposure selects how the model reaches the tools of an MCP server.
//
//   - codemode: tools are callable from codemode scripts, but not declared to
//     the model and not listed in the codemode description, which lists only
//     the server's namespace. Scripts find them with searchTools().
//     "codemode-deferred" is accepted as an alias.
//   - deferred: not declared to the model until the tool_search tool loads
//     them; the model then calls them directly. Does not need codemode.
//   - direct: tools are declared to the model like any other tool (and callable
//     from codemode).
//   - hidden: tools are registered but unreachable.
type McpExposure string

// The exposures of upstream's McpExposure union.
const (
	McpExposureCodemode McpExposure = "codemode"
	McpExposureDeferred McpExposure = "deferred"
	McpExposureDirect   McpExposure = "direct"
	McpExposureHidden   McpExposure = "hidden"
)

var mcpExposures = []McpExposure{
	McpExposureCodemode, McpExposureDeferred, McpExposureDirect, McpExposureHidden,
}

// mcpExposureAliases maps the exposures that were renamed to their current names (MCP_EXPOSURE_ALIASES).
var mcpExposureAliases = map[string]McpExposure{"codemode-deferred": McpExposureCodemode}

// McpOAuthConfig is OAuth client settings for servers that do not support
// dynamic client registration.
type McpOAuthConfig struct {
	// ClientID is a pre-registered client id. Without it, a client is registered
	// with the authorization server.
	ClientID string `json:"clientId,omitempty"`
	// ClientSecret may reference environment variables (`${NAME}`) or commands (`!cmd`).
	ClientSecret string `json:"clientSecret,omitempty"`
	// CallbackPort is the port of the loopback callback server, for clients
	// registered with a fixed redirect URI. Without CallbackURL, the redirect URI
	// is `http://127.0.0.1:<port>/callback`.
	CallbackPort *int `json:"callbackPort,omitempty"`
	// CallbackURL is the redirect URI registered for ClientID, for example
	// `http://localhost:8080/oauth/callback`. It must be an `http` URI on
	// `localhost`, `127.0.0.1`, or `[::1]`. Without a port, the callback server
	// listens on CallbackPort or a free port, which is added to the URI (RFC 8252).
	CallbackURL string `json:"callbackUrl,omitempty"`
	// Scope lists the scopes to request, separated by spaces. Default: the
	// scopes the server advertises.
	Scope string `json:"scope,omitempty"`
	// ClientName is the `client_name` sent with dynamic client registration,
	// for servers that only accept known clients. Default: the app name.
	ClientName string `json:"clientName,omitempty"`
	// ClientRegistration is how pi identifies itself without ClientID. `dcr`
	// (default): dynamic client registration. `cimd`: pi's Client ID Metadata
	// Document on pi.dev, for authorization servers that allow pi by that URL.
	// The server must support it for public clients, and the callback must use
	// the default path `/callback`.
	ClientRegistration string `json:"clientRegistration,omitempty"`
	// AuthServerMetadataURL is an authorization server metadata document
	// (RFC 8414 or OpenID Connect discovery) to use instead of discovery
	// through the server, for servers that advertise a wrong authorization
	// server or none. The document is trusted as configured. It must use
	// https, except on loopback hosts.
	AuthServerMetadataURL string `json:"authServerMetadataUrl,omitempty"`
}

// UnmarshalJSON reads `callbackPort` as a JavaScript number, so `8080.0` and `8e3` are the integers validation accepts.
func (c *McpOAuthConfig) UnmarshalJSON(data []byte) error {
	type plain McpOAuthConfig
	aux := struct {
		*plain
		CallbackPort *float64 `json:"callbackPort"`
	}{plain: (*plain)(c)}
	err := json.Unmarshal(data, &aux)
	c.CallbackPort = nil
	if aux.CallbackPort != nil && *aux.CallbackPort == float64(int(*aux.CallbackPort)) {
		port := int(*aux.CallbackPort)
		c.CallbackPort = &port
	}
	return err
}

// McpAuthConfig sends the token of a pi provider (`/login <provider>`) as the
// bearer token instead of using OAuth.
type McpAuthConfig struct {
	Provider string `json:"provider"`
}

// McpServerConfig is one server entry of the `mcpServers` shape. It is either
// a stdio server (Command set) or a streamable HTTP server (URL set): upstream's
// McpStdioServerConfig | McpHttpServerConfig union.
type McpServerConfig struct {
	// Type is "stdio" or "http"; "streamable-http" is accepted for HTTP servers.
	Type string `json:"type,omitempty"`
	// Exposure defaults to codemode.
	Exposure McpExposure `json:"exposure,omitempty"`
	// Description is what the server offers, in a sentence. The `mcp_servers`
	// system prompt section lists the server with it, tool search ranks the
	// server's tools by it, and codemode's `describeNamespace()` returns it.
	Description string `json:"description,omitempty"`
	// ToolExposure overrides Exposure for single tools. Keys are tool names as
	// the server offers them, or patterns where `*` matches any characters. An
	// exact name wins over patterns; among patterns the first match in the
	// object wins. hidden removes tools, so `"exposure": "hidden"` with overrides
	// for a few tools exposes only those.
	ToolExposure *OrderedExposures `json:"toolExposure,omitempty"`
	// Enabled set to false keeps the entry without connecting. Default: true.
	Enabled *bool `json:"enabled,omitempty"`
	// Timeout is the per-request timeout in seconds. Progress notifications from
	// the server reset it. Default: 60.
	Timeout *float64 `json:"timeout,omitempty"`

	// Stdio servers.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Env values may reference environment variables (`${NAME}`) or commands (`!cmd`).
	Env *OrderedStrings `json:"env,omitempty"`
	// Cwd is relative to the session working directory.
	Cwd string `json:"cwd,omitempty"`

	// HTTP servers.
	URL string `json:"url,omitempty"`
	// Headers values may reference environment variables (`${NAME}`) or commands (`!cmd`).
	Headers *OrderedStrings `json:"headers,omitempty"`
	OAuth   *McpOAuthConfig `json:"oauth,omitempty"`
	// Auth sends the token of a pi provider instead of using OAuth. Not allowed
	// in project `mcp.json` files, and requires https except on loopback hosts.
	Auth *McpAuthConfig `json:"auth,omitempty"`
}

// IsHTTP reports whether the entry is a streamable HTTP server: upstream's
// `"url" in config` check.
func (c McpServerConfig) IsHTTP() bool { return c.URL != "" }

// OrderedStrings is a string map that keeps its JSON key order, as a
// JavaScript object does.
type OrderedStrings struct{ o *orderedjson.Object }

// NewOrderedStrings builds the map from alternating keys and values.
func NewOrderedStrings(pairs ...string) *OrderedStrings {
	s := &OrderedStrings{o: orderedjson.New()}
	for i := 0; i+1 < len(pairs); i += 2 {
		_ = s.o.SetValue(pairs[i], pairs[i+1])
	}
	return s
}

// Keys returns the keys in order.
func (s *OrderedStrings) Keys() []string {
	if s == nil {
		return nil
	}
	return s.o.Keys()
}

// Get returns the value of key.
func (s *OrderedStrings) Get(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	raw, ok := s.o.Get(key)
	if !ok {
		return "", false
	}
	var v string
	_ = json.Unmarshal(raw, &v)
	return v, true
}

// MarshalJSON writes the keys in order.
func (s OrderedStrings) MarshalJSON() ([]byte, error) { return s.o.MarshalJSON() }

// UnmarshalJSON reads an object of strings.
func (s *OrderedStrings) UnmarshalJSON(data []byte) error {
	o, err := orderedjson.Parse(data)
	if err != nil {
		return err
	}
	for _, key := range o.Keys() {
		raw, _ := o.Get(key)
		var v string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &v) != nil {
			return fmt.Errorf("value of %q is not a string", key)
		}
	}
	s.o = o
	return nil
}

// OrderedExposures is a map of tool names or patterns to exposures that keeps
// its JSON key order: among patterns the first match wins.
type OrderedExposures struct{ o *orderedjson.Object }

// NewOrderedExposures builds the map from alternating patterns and exposures.
func NewOrderedExposures(pairs ...string) *OrderedExposures {
	e := &OrderedExposures{o: orderedjson.New()}
	for i := 0; i+1 < len(pairs); i += 2 {
		_ = e.o.SetValue(pairs[i], pairs[i+1])
	}
	return e
}

// Keys returns the patterns in order.
func (e *OrderedExposures) Keys() []string {
	if e == nil {
		return nil
	}
	return e.o.Keys()
}

// Get returns the exposure of a key.
func (e *OrderedExposures) Get(key string) (McpExposure, bool) {
	if e == nil {
		return "", false
	}
	raw, ok := e.o.Get(key)
	if !ok {
		return "", false
	}
	var v string
	_ = json.Unmarshal(raw, &v)
	return McpExposure(v), true
}

// MarshalJSON writes the keys in order.
func (e OrderedExposures) MarshalJSON() ([]byte, error) { return e.o.MarshalJSON() }

// UnmarshalJSON reads an object; validation of the values is
// [ValidateMcpServerConfig]'s.
func (e *OrderedExposures) UnmarshalJSON(data []byte) error {
	o, err := orderedjson.Parse(data)
	if err != nil {
		return err
	}
	e.o = o
	return nil
}

var loopbackHosts = []string{"localhost", "127.0.0.1", "[::1]"}

// IsLoopbackRedirectURI reports whether a redirect URI can be served by the
// loopback callback server: an `http` URI on `localhost`, `127.0.0.1`, or
// `[::1]` without query or fragment. The URI is parsed as `new URL(value)` does.
func IsLoopbackRedirectURI(value string) bool {
	u, err := nodeurl.ParseHTTPURL(value)
	return err == nil && u.Protocol == "http:" && slices.Contains(loopbackHosts, u.Hostname) && u.Search == "" && u.Hash == ""
}

// McpNamespace is the namespace of a server's tools: `mcp__<server>` with `-` replaced by `_`, like the tool names.
//
// Ports packages/coding-agent/src/core/mcp-servers.ts (mcpNamespace).
func McpNamespace(server string) string {
	return "mcp__" + strings.ReplaceAll(server, "-", "_")
}

var mcpServerName = lazyregexp.New(`^[A-Za-z0-9_-]+$`)

// resolveExposureAliases returns the entry with the exposure aliases of `exposure` and `toolExposure` replaced by their current names, every member in its original place (resolveExposureAliases: `{ ...value }` and an in-place assignment keep the member order).
//
// upstream: mcp-servers.ts:151-166 (resolveExposureAliases)
func resolveExposureAliases(value json.RawMessage, fields map[string]json.RawMessage) (json.RawMessage, map[string]json.RawMessage) {
	resolved, err := orderedjson.Parse(value)
	if err != nil {
		return value, fields
	}
	changed := false
	if s, ok := stringField(fields, "exposure"); ok {
		if alias, isAlias := mcpExposureAliases[s]; isAlias {
			_ = resolved.SetValue("exposure", string(alias))
			changed = true
		}
	}
	if raw, ok := fields["toolExposure"]; ok {
		if _, isObject := objectMap(raw); isObject {
			parsed, _ := orderedjson.Parse(raw)
			rebuilt := orderedjson.New()
			for _, tool := range parsed.Keys() {
				entry, _ := parsed.Get(tool)
				var name string
				if isString(entry) && json.Unmarshal(entry, &name) == nil {
					if alias, isAlias := mcpExposureAliases[name]; isAlias {
						entry, _ = json.Marshal(string(alias))
						changed = true
					}
				}
				rebuilt.Set(tool, entry)
			}
			if changed {
				_ = resolved.SetValue("toolExposure", rebuilt)
			}
		}
	}
	if !changed {
		return value, fields
	}
	out, _ := resolved.MarshalJSON()
	resolvedFields, _ := objectMap(out)
	return out, resolvedFields
}

// resolveMcpExposureAliases returns the `mcpServers` entry validateMcpServerConfig returns for a valid config: a copy with the exposure aliases of `exposure` and `toolExposure` replaced by their current names. A value that is not an object is returned unchanged.
//
// upstream: mcp-servers.ts:192-196 (validateMcpServerConfig returns the resolved copy)
func resolveMcpExposureAliases(value json.RawMessage) json.RawMessage {
	fields, ok := objectMap(value)
	if !ok {
		return value
	}
	resolved, _ := resolveExposureAliases(value, fields)
	return resolved
}

func isExposure(value string) bool {
	return slices.Contains(mcpExposures, McpExposure(value))
}

// toolPatternRegExp is upstream's `new RegExp("^" + parts.join(".*") + "$")`.
// A JavaScript `.` matches anything but a line terminator.
func toolPatternRegExp(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	return regexp.MustCompile("^" + strings.Join(parts, `[^\n\r\x{2028}\x{2029}]*`) + "$")
}

// GetMcpToolExposure is the exposure of one tool of a server: its
// `toolExposure` entry, else the server's `exposure`.
func GetMcpToolExposure(config McpServerConfig, toolName string) McpExposure {
	if exact, ok := config.ToolExposure.Get(toolName); ok {
		return exact
	}
	for _, pattern := range config.ToolExposure.Keys() {
		if strings.Contains(pattern, "*") && toolPatternRegExp(pattern).MatchString(toolName) {
			exposure, _ := config.ToolExposure.Get(pattern)
			return exposure
		}
	}
	if config.Exposure != "" {
		return config.Exposure
	}
	return McpExposureCodemode
}

func validateOAuth(fields map[string]json.RawMessage, name string) string {
	raw, ok := fields["oauth"]
	if !ok {
		return ""
	}
	oauth, isObject := objectMap(raw)
	if !isObject {
		return "oauth must be an object"
	}
	if v, ok := oauth["clientId"]; ok && !isString(v) {
		return "oauth.clientId must be a string"
	}
	if v, ok := oauth["clientSecret"]; ok && !isString(v) {
		return "oauth.clientSecret must be a string"
	}
	port, hasPort := 0, false
	if v, ok := oauth["callbackPort"]; ok {
		var n float64
		if err := json.Unmarshal(v, &n); err != nil || len(v) == 0 || v[0] == '"' || n != float64(int(n)) || n < 1 || n > 65535 {
			return "oauth.callbackPort must be a port number"
		}
		port, hasPort = int(n), true
	}
	if v, ok := oauth["callbackUrl"]; ok {
		var s string
		if !isString(v) || json.Unmarshal(v, &s) != nil || !IsLoopbackRedirectURI(s) {
			return "oauth.callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment"
		}
		u, _ := nodeurl.ParseHTTPURL(s)
		if urlPort := u.Port; urlPort != "" && hasPort {
			var p int
			_, _ = fmt.Sscanf(urlPort, "%d", &p)
			if p != port {
				return "oauth.callbackUrl and oauth.callbackPort name different ports"
			}
		}
	}
	if v, ok := oauth["scope"]; ok && !isString(v) {
		return "oauth.scope must be a string"
	}
	if v, ok := oauth["clientName"]; ok {
		var s string
		// upstream: mcp-servers.ts validateOAuth (`!value.clientName.trim()`, String.prototype.trim's whitespace set).
		if !isString(v) || json.Unmarshal(v, &s) != nil || widthx.JSTrim(s) == "" {
			return "oauth.clientName must be a non-empty string"
		}
	}
	// upstream: mcp-servers.ts validateOAuth (clientRegistration, 1.0.1 #10302).
	if v, ok := oauth["clientRegistration"]; ok && string(v) != `"dcr"` {
		if string(v) != `"cimd"` {
			return `oauth.clientRegistration must be "dcr" or "cimd"`
		}
		_, hasClientID := oauth["clientId"]
		_, hasClientName := oauth["clientName"]
		if hasClientID || hasClientName {
			return `oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName`
		}
		if raw, ok := oauth["callbackUrl"]; ok {
			var s string
			if isString(raw) && json.Unmarshal(raw, &s) == nil {
				if u, err := nodeurl.ParseHTTPURL(s); err == nil && (u.Hostname == "[::1]" || callbackPathname(s) != "/callback") {
					return `oauth.clientRegistration "cimd" requires oauth.callbackUrl on localhost or 127.0.0.1 with path /callback`
				}
			}
		}
	}
	if v, ok := oauth["authServerMetadataUrl"]; ok {
		var s string
		valid := isString(v) && json.Unmarshal(v, &s) == nil
		u, err := nodeurl.ParseHTTPURL(s)
		secure := u.Protocol == "https:" || (u.Protocol == "http:" && slices.Contains(loopbackHosts, u.Hostname))
		if !valid || err != nil || !secure {
			return "oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]"
		}
	}
	return ""
}

// callbackPathname is `new URL(raw).pathname` for a validated loopback
// callback URL: dot segments removed and an empty path reported as "/".
func callbackPathname(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	// Resolving the path against itself removes "." and ".." segments as the WHATWG parser does.
	path := u.ResolveReference(&url.URL{Path: u.Path, RawPath: u.RawPath}).EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

func objectMap(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	return m, true
}

func isString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }

func isStringRecord(raw json.RawMessage) bool {
	m, ok := objectMap(raw)
	if !ok {
		return false
	}
	for _, v := range m {
		if !isString(v) {
			return false
		}
	}
	return true
}

func stringField(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fields[key]
	if !ok || !isString(raw) {
		return "", false
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s, true
}

// ValidateMcpServerConfig validates one entry of the `mcpServers` shape. It
// returns the config, or the message upstream's validation returns as a string.
func ValidateMcpServerConfig(name string, value json.RawMessage) (McpServerConfig, string) {
	if !mcpServerName.MatchString(name) {
		return McpServerConfig{}, fmt.Sprintf(`invalid server name "%s" (use letters, digits, "_" and "-")`, name)
	}
	fields, ok := objectMap(value)
	if !ok {
		return McpServerConfig{}, fmt.Sprintf(`server "%s" must be an object`, name)
	}
	value, fields = resolveExposureAliases(value, fields)
	quoted := make([]string, len(mcpExposures))
	for i, e := range mcpExposures {
		quoted[i] = `"` + string(e) + `"`
	}
	exposures := strings.Join(quoted, ", ")
	if raw, ok := fields["exposure"]; ok {
		if s, isStr := stringField(fields, "exposure"); !isStr || !isExposure(s) {
			_ = raw
			return McpServerConfig{}, fmt.Sprintf(`server "%s": exposure must be one of %s`, name, exposures)
		}
	}
	if raw, ok := fields["toolExposure"]; ok {
		overrides, isObject := objectMap(raw)
		if !isObject {
			return McpServerConfig{}, fmt.Sprintf(`server "%s": toolExposure must map tool names to exposures`, name)
		}
		parsed, _ := orderedjson.Parse(raw)
		for _, tool := range parsed.Keys() {
			v := overrides[tool]
			var s string
			if !isString(v) || json.Unmarshal(v, &s) != nil || !isExposure(s) {
				return McpServerConfig{}, fmt.Sprintf(`server "%s": toolExposure "%s" must be one of %s`, name, tool, exposures)
			}
		}
	}
	if raw, ok := fields["enabled"]; ok && string(raw) != "true" && string(raw) != "false" {
		return McpServerConfig{}, fmt.Sprintf(`server "%s": enabled must be a boolean`, name)
	}
	if raw, ok := fields["description"]; ok && !isString(raw) {
		return McpServerConfig{}, fmt.Sprintf(`server "%s": description must be a string`, name)
	}
	if raw, ok := fields["timeout"]; ok {
		var n float64
		if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &n) != nil || !(n > 0) {
			return McpServerConfig{}, fmt.Sprintf(`server "%s": timeout must be a positive number of seconds`, name)
		}
	}
	// upstream: mcp-servers.ts validateMcpServerConfig (`type === undefined || type === "http"`): a `type` that is present but not a string names no transport.
	typ, typeIsString := stringField(fields, "type")
	_, typePresent := fields["type"]
	typeIs := func(names ...string) bool {
		return !typePresent || (typeIsString && slices.Contains(names, typ))
	}
	if typ == "sse" {
		return McpServerConfig{}, fmt.Sprintf(`server "%s": legacy SSE transport is not supported; use the streamable HTTP URL`, name)
	}

	rawURL, hasURL := stringField(fields, "url")
	if hasURL && typeIs("http", "streamable-http") {
		// upstream: mcp-servers.ts validateMcpServerConfig (`URL.canParse(value.url) && /^https?:$/.test(new URL(value.url).protocol)`).
		parsedURL, err := nodeurl.ParseHTTPURL(rawURL)
		if err != nil {
			return McpServerConfig{}, fmt.Sprintf(`server "%s": url must be an http or https URL`, name)
		}
		if raw, ok := fields["headers"]; ok && !isStringRecord(raw) {
			return McpServerConfig{}, fmt.Sprintf(`server "%s": headers must map names to strings`, name)
		}
		if message := validateOAuth(fields, name); message != "" {
			return McpServerConfig{}, fmt.Sprintf(`server "%s": %s`, name, message)
		}
		if raw, ok := fields["auth"]; ok {
			auth, isObject := objectMap(raw)
			provider, hasProvider := stringField(auth, "provider")
			if !isObject || !hasProvider || provider == "" {
				return McpServerConfig{}, fmt.Sprintf(`server "%s": auth.provider must be a provider name`, name)
			}
			if parsedURL.Protocol != "https:" && !slices.Contains(loopbackHosts, parsedURL.Hostname) {
				return McpServerConfig{}, fmt.Sprintf(`server "%s": auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]`, name)
			}
		}
		return decodeMcpServerConfig(value), ""
	}
	if _, hasCommand := stringField(fields, "command"); hasCommand && typeIs("stdio") {
		if raw, ok := fields["args"]; ok {
			var args []json.RawMessage
			valid := len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &args) == nil
			for _, a := range args {
				valid = valid && isString(a)
			}
			if !valid {
				return McpServerConfig{}, fmt.Sprintf(`server "%s": args must be an array of strings`, name)
			}
		}
		if raw, ok := fields["env"]; ok && !isStringRecord(raw) {
			return McpServerConfig{}, fmt.Sprintf(`server "%s": env must map names to strings`, name)
		}
		if raw, ok := fields["cwd"]; ok && !isString(raw) {
			return McpServerConfig{}, fmt.Sprintf(`server "%s": cwd must be a string`, name)
		}
		return decodeMcpServerConfig(value), ""
	}
	return McpServerConfig{}, fmt.Sprintf(`server "%s" needs either "command" (stdio) or "url" (streamable HTTP)`, name)
}

// decodeMcpServerConfig reads the members the host uses from a validated entry. Validation checks only the members of the entry's transport, so a member of the other transport (or an unused one) with a value of another type is ignored, as it is in Pi's untyped object.
//
// upstream: mcp-servers.ts validateMcpServerConfig (returns the entry as is after checking the transport's members)
func decodeMcpServerConfig(value json.RawMessage) McpServerConfig {
	var config McpServerConfig
	// The value is valid JSON; an error names the first member that does not fit its Go type, and the decoder keeps every member that does.
	_ = json.Unmarshal(value, &config)
	return config
}

// RegisteredMcpServer is a server an extension registered with
// `pi.registerMcpServer()`.
type RegisteredMcpServer struct {
	Name   string          `json:"name"`
	Config McpServerConfig `json:"config"`
	// Declared is the config as the extension wrote it with its exposure aliases resolved, JSON.stringify(JSON.parse(text)): every member, known or not, in the order written, the way a JavaScript object holds them. Extensions read it back through getMcpServers, so the wire carries it in place of Config, which holds the members the host uses. Empty for a server registered with a typed Config alone.
	// upstream: mcp-servers.ts:151-166,192-222 (validateMcpServerConfig returns the alias-resolved copy of the object it was given), 283 (list copies it with structuredClone)
	Declared json.RawMessage `json:"-"`
	// ExtensionPath is the path of the extension that registered the server.
	ExtensionPath string `json:"extensionPath"`
}

// MarshalJSON writes the server as getMcpServers returns it: name, config, extensionPath, with the config as declared.
func (s RegisteredMcpServer) MarshalJSON() ([]byte, error) {
	var config any = s.Config
	if len(s.Declared) > 0 {
		config = s.Declared
	}
	return json.Marshal(struct {
		Name          string `json:"name"`
		Config        any    `json:"config"`
		ExtensionPath string `json:"extensionPath"`
	}{s.Name, config, s.ExtensionPath})
}

// McpServerRegistry holds the servers registered by the extensions of one
// runtime. It is safe for concurrent use; the change listener runs after the
// registry's lock is released.
type McpServerRegistry struct {
	mu             sync.Mutex
	order          []string
	servers        map[string]RegisteredMcpServer
	changeListener func()
}

// NewMcpServerRegistry returns an empty registry.
func NewMcpServerRegistry() *McpServerRegistry {
	return &McpServerRegistry{servers: map[string]RegisteredMcpServer{}}
}

// Register adds or replaces a server. The caller checks ownership. A
// replaced server keeps its position, as a JavaScript Map does.
func (r *McpServerRegistry) Register(server RegisteredMcpServer) {
	r.mu.Lock()
	if _, exists := r.servers[server.Name]; !exists {
		r.order = append(r.order, server.Name)
	}
	r.servers[server.Name] = server
	listener := r.changeListener
	r.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// Unregister removes a server registered by extensionPath. Servers of other
// extensions are left alone.
func (r *McpServerRegistry) Unregister(name, extensionPath string) {
	r.mu.Lock()
	server, ok := r.servers[name]
	if !ok || server.ExtensionPath != extensionPath {
		r.mu.Unlock()
		return
	}
	delete(r.servers, name)
	r.order = slices.DeleteFunc(r.order, func(n string) bool { return n == name })
	listener := r.changeListener
	r.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// Get returns a registered server.
func (r *McpServerRegistry) Get(name string) (RegisteredMcpServer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	server, ok := r.servers[name]
	return server, ok
}

// List returns copies of the registered servers, in registration order.
func (r *McpServerRegistry) List() []RegisteredMcpServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RegisteredMcpServer, 0, len(r.order))
	for _, name := range r.order {
		server := r.servers[name]
		data, err := json.Marshal(server.Config)
		if err == nil {
			var clone McpServerConfig
			if json.Unmarshal(data, &clone) == nil {
				server.Config = clone
			}
		}
		server.Declared = slices.Clone(server.Declared)
		out = append(out, server)
	}
	return out
}

// SetChangeListener sets the function called after every change. The runner
// sets it when it binds, to emit `mcp_servers_change`.
func (r *McpServerRegistry) SetChangeListener(listener func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changeListener = listener
}
