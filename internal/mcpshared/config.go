package mcpshared

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/mcp/src/config.ts (Pi 76c040a2a, moved from coding-agent's core/mcp-servers.ts).
//
// The `mcpServers` configuration shape that MCP clients share, so configurations can be copied between them: stdio servers (`command`, `args`,
// `env`, `cwd`), Streamable HTTP servers (`url`, `headers`, `oauth`), and `enabled`, `description` and `timeout` for both. Applications add fields
// of their own: [ValidateMcpServerConfig] keeps fields it does not know for the caller to check afterwards.
//
// Go mechanic: Pi's `McpStdioServerConfig`, `McpHttpServerConfig` and `McpOAuthConfig` interfaces type an entry for the caller. An entry here is the
// JSON object as given (and its members by name), which each host decodes into its own config type: coding/extension's McpServerConfig is the
// application's type with the shared and its own fields.

// McpServerConfigKind tags a validated entry with its transport, or marks it invalid.
type McpServerConfigKind string

// Kinds of [McpServerConfigResult].
const (
	McpServerConfigStdio   McpServerConfigKind = "stdio"
	McpServerConfigHTTP    McpServerConfigKind = "http"
	McpServerConfigInvalid McpServerConfigKind = "invalid"
)

// McpServerConfigResult is a validated server entry, tagged with its transport, or why it is invalid. The config is the entry as given: fields the
// validator does not know are kept, unchecked, for the caller to check.
type McpServerConfigResult struct {
	Kind McpServerConfigKind
	// Config is the entry as given, for a stdio or http entry.
	Config json.RawMessage
	// Fields are the members of Config.
	Fields map[string]json.RawMessage
	// Error is why an invalid entry is invalid.
	Error string
	// URL is the URL the http entry was checked with: ExpandURL applied to its `url`.
	URL nodeurl.HTTPURL
}

// McpServerConfigOptions is what the application adds to the validation of server entries.
type McpServerConfigOptions struct {
	// CheckMetadataDocumentCallback checks the `oauth.callbackUrl` (callbackURL, set is false when none is configured) of an entry with
	// `oauth.clientRegistration: "cimd"`: the application's Client ID Metadata Documents must list it. It returns an error message, or "". Without
	// this check, `"cimd"` is refused.
	CheckMetadataDocumentCallback func(callbackURL string, set bool) string
	// ExpandURL, when not nil, is applied to the `url` of an http entry before it is checked. Go mechanic of D156: PiG resolves
	// environment references in a URL before using it, so the URL checked is the expanded one.
	ExpandURL func(string) string
}

var serverName = lazyregexp.New(`^[A-Za-z0-9_-]+$`)

// ValidateMcpServerConfig validates one entry of the `mcpServers` shape (configuration files are a trust boundary). Server names are letters,
// digits, `_` and `-`, which tool names and namespaces are built from.
func ValidateMcpServerConfig(name string, raw json.RawMessage, options McpServerConfigOptions) McpServerConfigResult {
	invalid := func(message string) McpServerConfigResult {
		return McpServerConfigResult{Kind: McpServerConfigInvalid, Error: message}
	}
	if !serverName.MatchString(name) {
		return invalid(fmt.Sprintf(`invalid server name "%s" (use letters, digits, "_" and "-")`, name))
	}
	fields, ok := objectMembers(raw)
	if !ok {
		return invalid(fmt.Sprintf(`server "%s" must be an object`, name))
	}
	if message := validateBaseFields(fields); message != "" {
		return invalid(fmt.Sprintf(`server "%s": %s`, name, message))
	}
	// A `type` that is present but not a string names no transport.
	typ, typeIsString := stringMember(fields, "type")
	_, typePresent := fields["type"]
	typeIs := func(names ...string) bool {
		return !typePresent || (typeIsString && slices.Contains(names, typ))
	}
	if typeIsString && typ == "sse" {
		return invalid(fmt.Sprintf(`server "%s": legacy SSE transport is not supported; use the streamable HTTP URL`, name))
	}
	if rawURL, hasURL := stringMember(fields, "url"); hasURL && typeIs("http", "streamable-http") {
		checked := rawURL
		if options.ExpandURL != nil {
			checked = options.ExpandURL(rawURL)
		}
		parsed, message := validateHTTPFields(fields, checked, options)
		if message != "" {
			return invalid(fmt.Sprintf(`server "%s": %s`, name, message))
		}
		return McpServerConfigResult{Kind: McpServerConfigHTTP, Config: raw, Fields: fields, URL: parsed}
	}
	if _, hasCommand := stringMember(fields, "command"); hasCommand && typeIs("stdio") {
		if message := validateStdioFields(fields); message != "" {
			return invalid(fmt.Sprintf(`server "%s": %s`, name, message))
		}
		return McpServerConfigResult{Kind: McpServerConfigStdio, Config: raw, Fields: fields}
	}
	return invalid(fmt.Sprintf(`server "%s" needs either "command" (stdio) or "url" (streamable HTTP)`, name))
}

func validateBaseFields(fields map[string]json.RawMessage) string {
	if raw, ok := fields["enabled"]; ok && string(raw) != "true" && string(raw) != "false" {
		return "enabled must be a boolean"
	}
	if raw, ok := fields["description"]; ok && !isQuotedString(raw) {
		return "description must be a string"
	}
	if raw, ok := fields["timeout"]; ok {
		var n float64
		if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &n) != nil || !(n > 0) {
			return "timeout must be a positive number of seconds"
		}
	}
	return ""
}

func validateHTTPFields(fields map[string]json.RawMessage, url string, options McpServerConfigOptions) (nodeurl.HTTPURL, string) {
	parsed, err := nodeurl.ParseHTTPURL(url)
	if err != nil {
		return nodeurl.HTTPURL{}, "url must be an http or https URL"
	}
	if raw, ok := fields["headers"]; ok && !isStringRecord(raw) {
		return nodeurl.HTTPURL{}, "headers must map names to strings"
	}
	return parsed, validateOAuth(fields, options)
}

func validateStdioFields(fields map[string]json.RawMessage) string {
	if raw, ok := fields["args"]; ok {
		var args []json.RawMessage
		valid := len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &args) == nil
		for _, arg := range args {
			valid = valid && isQuotedString(arg)
		}
		if !valid {
			return "args must be an array of strings"
		}
	}
	if raw, ok := fields["env"]; ok && !isStringRecord(raw) {
		return "env must map names to strings"
	}
	if raw, ok := fields["cwd"]; ok && !isQuotedString(raw) {
		return "cwd must be a string"
	}
	return ""
}

func objectMembers(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return nil, false
	}
	return members, true
}

func isQuotedString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }

func stringMember(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fields[key]
	if !ok || !isQuotedString(raw) {
		return "", false
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s, true
}

func isStringRecord(raw json.RawMessage) bool {
	members, ok := objectMembers(raw)
	if !ok {
		return false
	}
	for _, value := range members {
		if !isQuotedString(value) {
			return false
		}
	}
	return true
}

var loopbackHosts = []string{"localhost", "127.0.0.1", "[::1]"}

func validateOAuth(fields map[string]json.RawMessage, options McpServerConfigOptions) string {
	raw, ok := fields["oauth"]
	if !ok {
		return ""
	}
	oauth, isObject := objectMembers(raw)
	if !isObject {
		return "oauth must be an object"
	}
	if v, ok := oauth["clientId"]; ok && !isQuotedString(v) {
		return "oauth.clientId must be a string"
	}
	if v, ok := oauth["clientSecret"]; ok && !isQuotedString(v) {
		return "oauth.clientSecret must be a string"
	}
	port, hasPort := 0, false
	if v, ok := oauth["callbackPort"]; ok {
		var n float64
		//portlint:allow numbers a JSON number is finite, and a value outside the int range converts to a different float, so the check still rejects it like Number.isInteger (config.ts validateOAuth)
		if err := json.Unmarshal(v, &n); err != nil || len(v) == 0 || v[0] == '"' || n != float64(int(n)) || n < 1 || n > 65535 {
			return "oauth.callbackPort must be a port number"
		}
		//portlint:allow numbers n is an integer from 1 to 65535 here (config.ts validateOAuth)
		port, hasPort = int(n), true
	}
	callbackURL, hasCallbackURL := "", false
	if _, ok := oauth["callbackUrl"]; ok {
		s, isString := stringMember(oauth, "callbackUrl")
		if !isString || !isLoopbackRedirectURI(s) {
			return "oauth.callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment"
		}
		callbackURL, hasCallbackURL = s, true
		u, _ := nodeurl.ParseHTTPURL(s)
		if u.Port != "" && hasPort {
			//portlint:allow numbers u.Port is the serialized port of a parsed URL: at most five decimal digits, or empty (config.ts validateOAuth)
			if p, _ := strconv.Atoi(u.Port); p != port {
				return "oauth.callbackUrl and oauth.callbackPort name different ports"
			}
		}
	}
	if v, ok := oauth["scope"]; ok && !isQuotedString(v) {
		return "oauth.scope must be a string"
	}
	if _, ok := oauth["clientName"]; ok {
		s, isString := stringMember(oauth, "clientName")
		if !isString || jsstring.Trim(s) == "" {
			return "oauth.clientName must be a non-empty string"
		}
	}
	if v, ok := oauth["clientRegistration"]; ok && string(v) != `"auto"` && string(v) != `"dcr"` {
		if string(v) != `"cimd"` {
			return `oauth.clientRegistration must be "auto", "dcr", or "cimd"`
		}
		_, hasClientID := oauth["clientId"]
		_, hasClientName := oauth["clientName"]
		if hasClientID || hasClientName {
			return `oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName`
		}
		if options.CheckMetadataDocumentCallback == nil {
			return `oauth.clientRegistration "cimd" is not supported: there are no Client ID Metadata Documents`
		}
		if message := options.CheckMetadataDocumentCallback(callbackURL, hasCallbackURL); message != "" {
			return message
		}
	}
	if _, ok := oauth["authServerMetadataUrl"]; ok {
		s, isString := stringMember(oauth, "authServerMetadataUrl")
		u, err := nodeurl.ParseHTTPURL(s)
		secure := u.Protocol == "https:" || (u.Protocol == "http:" && slices.Contains(loopbackHosts, u.Hostname))
		if !isString || err != nil || !secure {
			return "oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]"
		}
	}
	return ""
}

// isLoopbackRedirectURI reports whether a redirect URI can be served by a loopback callback server: an `http` URI on `localhost`, `127.0.0.1`, or
// `[::1]` without query or fragment. The URI is parsed as `new URL(value)` does.
func isLoopbackRedirectURI(value string) bool {
	u, err := nodeurl.ParseHTTPURL(value)
	return err == nil && u.Protocol == "http:" && slices.Contains(loopbackHosts, u.Hostname) && u.Search == "" && u.Hash == ""
}

// McpEntry is an entry of the `mcpServers` shape as an object with named members. Go mechanic: Pi's functions take the parsed JSON object; hosts hold
// entries as an ordered JSON object, a map, or both, and only ask whether a member is present.
type McpEntry interface {
	// Has reports whether the entry has a member named key, whatever its value.
	Has(key string) bool
}

// IsMcpServerOverride reports whether an entry overrides a server defined elsewhere instead of defining one: it has no `command`, `url` or `type`.
// Applications let a less trusted file (a project's) override a few fields, such as `enabled`, of a server a trusted file defines; which fields is
// theirs to decide.
func IsMcpServerOverride(entry McpEntry) bool {
	return !entry.Has("command") && !entry.Has("url") && !entry.Has("type")
}
