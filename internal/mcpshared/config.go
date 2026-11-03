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

// Ports packages/mcp/src/config.ts.
//
// The `mcpServers` configuration shape that MCP clients share, so
// configurations can be copied between them: stdio servers (`command`, `args`,
// `env`, `cwd`), Streamable HTTP servers (`url`, `headers`, `oauth`), and
// `enabled`, `description` and `timeout` for both.
//
//	{
//	  "mcpServers": {
//	    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."] },
//	    "docs": { "url": "https://example.com/mcp", "headers": { "Authorization": "Bearer ${DOCS_TOKEN}" } }
//	  }
//	}
//
// Applications add fields of their own: [ValidateMcpServerConfig] keeps the
// entry as given, with the fields it does not know, for the caller to check
// afterwards.
//
// Upstream's `McpStdioServerConfig`, `McpHttpServerConfig`, `McpOAuthConfig`
// and `McpServerConfig` types describe the entry for TypeScript callers that
// extend them. Go has no interface extension, and a caller decodes the entry
// into its own struct, so the result carries the entry's JSON instead.

// McpServerConfigKind tags a validated server entry with its transport:
// upstream's `McpServerConfigResult["kind"]`.
type McpServerConfigKind string

// The kinds of [McpServerConfigResult].
const (
	McpServerConfigStdio   McpServerConfigKind = "stdio"
	McpServerConfigHTTP    McpServerConfigKind = "http"
	McpServerConfigInvalid McpServerConfigKind = "invalid"
)

// McpServerConfigResult is a validated server entry, tagged with its
// transport, or why it is invalid. Config is the entry as given: members the
// validator does not know are kept, unchecked, for the caller to check. It is
// empty for an invalid entry, and Error is empty for a valid one.
type McpServerConfigResult struct {
	Kind   McpServerConfigKind
	Config json.RawMessage
	Error  string
}

// McpServerConfigOptions is what the application adds to the validation of
// server entries.
type McpServerConfigOptions struct {
	// CheckMetadataDocumentCallback checks the `oauth.callbackUrl` (empty: none
	// configured) of an entry with `oauth.clientRegistration: "cimd"`: the
	// application's Client ID Metadata Documents must list it. It returns an
	// error message, or the empty string. Without this check, `"cimd"` is refused.
	CheckMetadataDocumentCallback func(callbackURL string) string
}

var serverName = lazyregexp.New(`^[A-Za-z0-9_-]+$`)

// ValidateMcpServerConfig validates one entry of the `mcpServers` shape
// (configuration files are a trust boundary). Server names are letters, digits,
// `_` and `-`, which tool names and namespaces are built from.
func ValidateMcpServerConfig(name string, raw json.RawMessage, options McpServerConfigOptions) McpServerConfigResult {
	invalid := func(message string) McpServerConfigResult {
		return McpServerConfigResult{Kind: McpServerConfigInvalid, Error: message}
	}
	if !serverName.MatchString(name) {
		return invalid(fmt.Sprintf(`invalid server name "%s" (use letters, digits, "_" and "-")`, name))
	}
	entry, ok := objectMembers(raw)
	if !ok {
		return invalid(fmt.Sprintf(`server "%s" must be an object`, name))
	}
	if message := validateBaseFields(entry); message != "" {
		return invalid(fmt.Sprintf(`server "%s": %s`, name, message))
	}
	// `type === undefined || type === "http"`: a `type` that is present but not a string names no transport.
	typ, typeIsString := stringMember(entry, "type")
	_, typePresent := entry["type"]
	typeIs := func(names ...string) bool {
		return !typePresent || (typeIsString && slices.Contains(names, typ))
	}
	if typ == "sse" {
		return invalid(fmt.Sprintf(`server "%s": legacy SSE transport is not supported; use the streamable HTTP URL`, name))
	}
	if url, hasURL := stringMember(entry, "url"); hasURL && typeIs("http", "streamable-http") {
		if message := validateHTTPFields(entry, url, options); message != "" {
			return invalid(fmt.Sprintf(`server "%s": %s`, name, message))
		}
		return McpServerConfigResult{Kind: McpServerConfigHTTP, Config: raw}
	}
	if _, hasCommand := stringMember(entry, "command"); hasCommand && typeIs("stdio") {
		if message := validateStdioFields(entry); message != "" {
			return invalid(fmt.Sprintf(`server "%s": %s`, name, message))
		}
		return McpServerConfigResult{Kind: McpServerConfigStdio, Config: raw}
	}
	return invalid(fmt.Sprintf(`server "%s" needs either "command" (stdio) or "url" (streamable HTTP)`, name))
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

func isString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }

func stringMember(members map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := members[key]
	if !ok || !isString(raw) {
		return "", false
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s, true
}

func validateBaseFields(entry map[string]json.RawMessage) string {
	if enabled, ok := entry["enabled"]; ok && string(enabled) != "true" && string(enabled) != "false" {
		return "enabled must be a boolean"
	}
	if description, ok := entry["description"]; ok && !isString(description) {
		return "description must be a string"
	}
	if raw, ok := entry["timeout"]; ok {
		var timeout float64
		if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &timeout) != nil || !(timeout > 0) {
			return "timeout must be a positive number of seconds"
		}
	}
	return ""
}

func validateHTTPFields(entry map[string]json.RawMessage, url string, options McpServerConfigOptions) string {
	if _, err := nodeurl.ParseHTTPURL(url); err != nil {
		return "url must be an http or https URL"
	}
	if headers, ok := entry["headers"]; ok && !isStringRecord(headers) {
		return "headers must map names to strings"
	}
	return validateOAuth(entry, options)
}

func validateStdioFields(entry map[string]json.RawMessage) string {
	if raw, ok := entry["args"]; ok {
		var args []json.RawMessage
		valid := len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &args) == nil
		for _, arg := range args {
			valid = valid && isString(arg)
		}
		if !valid {
			return "args must be an array of strings"
		}
	}
	if env, ok := entry["env"]; ok && !isStringRecord(env) {
		return "env must map names to strings"
	}
	if cwd, ok := entry["cwd"]; ok && !isString(cwd) {
		return "cwd must be a string"
	}
	return ""
}

func isStringRecord(raw json.RawMessage) bool {
	members, ok := objectMembers(raw)
	if !ok {
		return false
	}
	for _, member := range members {
		if !isString(member) {
			return false
		}
	}
	return true
}

var loopbackHosts = []string{"localhost", "127.0.0.1", "[::1]"}

func validateOAuth(entry map[string]json.RawMessage, options McpServerConfigOptions) string {
	raw, ok := entry["oauth"]
	if !ok {
		return ""
	}
	oauth, isObject := objectMembers(raw)
	if !isObject {
		return "oauth must be an object"
	}
	if clientID, ok := oauth["clientId"]; ok && !isString(clientID) {
		return "oauth.clientId must be a string"
	}
	if clientSecret, ok := oauth["clientSecret"]; ok && !isString(clientSecret) {
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
	callbackURL := ""
	if v, ok := oauth["callbackUrl"]; ok {
		if !isString(v) || json.Unmarshal(v, &callbackURL) != nil || !isLoopbackRedirectURI(callbackURL) {
			return "oauth.callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment"
		}
		u, _ := nodeurl.ParseHTTPURL(callbackURL)
		//portlint:allow numbers u.Port is the serialized port of a parsed URL: at most five decimal digits, or empty (config.ts validateOAuth)
		if urlPort, err := strconv.Atoi(u.Port); err == nil && hasPort && urlPort != port {
			return "oauth.callbackUrl and oauth.callbackPort name different ports"
		}
	}
	if scope, ok := oauth["scope"]; ok && !isString(scope) {
		return "oauth.scope must be a string"
	}
	if v, ok := oauth["clientName"]; ok {
		var clientName string
		if !isString(v) || json.Unmarshal(v, &clientName) != nil || jsstring.Trim(clientName) == "" {
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
		if message := options.CheckMetadataDocumentCallback(callbackURL); message != "" {
			return message
		}
	}
	if v, ok := oauth["authServerMetadataUrl"]; ok {
		var metadataURL string
		valid := isString(v) && json.Unmarshal(v, &metadataURL) == nil
		u, err := nodeurl.ParseHTTPURL(metadataURL)
		secure := u.Protocol == "https:" || (u.Protocol == "http:" && slices.Contains(loopbackHosts, u.Hostname))
		if !valid || err != nil || !secure {
			return "oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]"
		}
	}
	return ""
}

// isLoopbackRedirectURI reports whether a redirect URI can be served by a
// loopback callback server: an `http` URI on `localhost`, `127.0.0.1`, or
// `[::1]` without query or fragment. The URI is parsed as `new URL(value)` does.
func isLoopbackRedirectURI(value string) bool {
	u, err := nodeurl.ParseHTTPURL(value)
	return err == nil && u.Protocol == "http:" && slices.Contains(loopbackHosts, u.Hostname) && u.Search == "" && u.Hash == ""
}

// IsMcpServerOverride reports whether an entry (its members) overrides a
// server defined elsewhere instead of defining one: it has no `command`, `url`
// or `type`, and a member that is present counts whatever its value.
// Applications let a less trusted file (a project's) override a few fields,
// such as `enabled`, of a server a trusted file defines; which fields is
// theirs to decide.
func IsMcpServerOverride(entry map[string]json.RawMessage) bool {
	_, hasCommand := entry["command"]
	_, hasURL := entry["url"]
	_, hasType := entry["type"]
	return !hasCommand && !hasURL && !hasType
}
