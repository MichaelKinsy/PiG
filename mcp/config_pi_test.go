package mcp_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/config.test.ts (Pi 76c040a2a). Entries are JSON text, as a configuration file holds them.

func errorOfEntry(name, raw string, options mcp.McpServerConfigOptions) string {
	result := mcp.ValidateMcpServerConfig(name, json.RawMessage(raw), options)
	return result.Error
}

func TestValidateMcpServerConfigAcceptsStdioAndHTTPEntriesAndKeepsThemAsGiven(t *testing.T) {
	stdio := `{"command":"npx","args":["-y","server"],"env":{"A":"1"},"cwd":"sub","enabled":false}`
	result := mcp.ValidateMcpServerConfig("files", json.RawMessage(stdio), mcp.McpServerConfigOptions{})
	if result.Kind != mcp.McpServerConfigStdio || string(result.Config) != stdio {
		t.Fatalf("result = %+v", result)
	}
	if kind := mcp.ValidateMcpServerConfig("files", json.RawMessage(`{"type":"stdio","command":"x"}`), mcp.McpServerConfigOptions{}).Kind; kind != mcp.McpServerConfigStdio {
		t.Fatalf("kind = %s", kind)
	}
	http := `{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer token"},"oauth":{"clientId":"id","callbackPort":8080,"scope":"read"},"timeout":30,"description":"Docs"}`
	result = mcp.ValidateMcpServerConfig("docs", json.RawMessage(http), mcp.McpServerConfigOptions{})
	if result.Kind != mcp.McpServerConfigHTTP || string(result.Config) != http {
		t.Fatalf("result = %+v", result)
	}
	for _, typ := range []string{"http", "streamable-http"} {
		raw := `{"type":"` + typ + `","url":"http://localhost:3000/mcp"}`
		if kind := mcp.ValidateMcpServerConfig("docs", json.RawMessage(raw), mcp.McpServerConfigOptions{}).Kind; kind != mcp.McpServerConfigHTTP {
			t.Fatalf("%s: kind = %s", typ, kind)
		}
	}
}

func TestValidateMcpServerConfigPicksTheTransportFromTypeURLAndCommand(t *testing.T) {
	kind := func(raw string) mcp.McpServerConfigKind {
		return mcp.ValidateMcpServerConfig("s", json.RawMessage(raw), mcp.McpServerConfigOptions{}).Kind
	}
	// With both, url wins unless the type says stdio.
	if kind(`{"url":"https://a.example","command":"x"}`) != mcp.McpServerConfigHTTP || kind(`{"type":"stdio","url":"https://a.example","command":"x"}`) != mcp.McpServerConfigStdio {
		t.Fatal("transport mismatch")
	}
	if got := errorOfEntry("s", `{"type":"http","command":"x"}`, mcp.McpServerConfigOptions{}); got != `server "s" needs either "command" (stdio) or "url" (streamable HTTP)` {
		t.Fatalf("error = %q", got)
	}
	if got := errorOfEntry("s", `{"type":"sse","url":"https://a.example/sse"}`, mcp.McpServerConfigOptions{}); got != `server "s": legacy SSE transport is not supported; use the streamable HTTP URL` {
		t.Fatalf("error = %q", got)
	}
}

func TestValidateMcpServerConfigReportsInvalidNamesAndFields(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"bad name", `{"command":"x"}`, `invalid server name "bad name" (use letters, digits, "_" and "-")`},
		{"s", `"npx"`, `server "s" must be an object`},
		{"s", `[]`, `server "s" must be an object`},
		{"s", `{"command":"x","enabled":"yes"}`, `server "s": enabled must be a boolean`},
		{"s", `{"command":"x","description":1}`, `server "s": description must be a string`},
		{"s", `{"command":"x","timeout":0}`, `server "s": timeout must be a positive number of seconds`},
		{"s", `{"command":"x","args":"a b"}`, `server "s": args must be an array of strings`},
		{"s", `{"command":"x","env":{"A":1}}`, `server "s": env must map names to strings`},
		{"s", `{"command":"x","cwd":1}`, `server "s": cwd must be a string`},
		{"s", `{"url":"file:///x"}`, `server "s": url must be an http or https URL`},
		{"s", `{"url":"not a url"}`, `server "s": url must be an http or https URL`},
		{"s", `{"url":"https://a.example","headers":{"A":1}}`, `server "s": headers must map names to strings`},
		{"s", `{}`, `server "s" needs either "command" (stdio) or "url" (streamable HTTP)`},
	} {
		if got := errorOfEntry(tc.name, tc.raw, mcp.McpServerConfigOptions{}); got != tc.want {
			t.Errorf("%s %s: error = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

func TestValidateMcpServerConfigChecksOAuthSettings(t *testing.T) {
	oauth := func(settings string) string {
		return errorOfEntry("s", `{"url":"https://a.example","oauth":`+settings+`}`, mcp.McpServerConfigOptions{})
	}
	exact := func(settings, want string) {
		t.Helper()
		if got := oauth(settings); got != want {
			t.Errorf("oauth %s: error = %q, want %q", settings, got, want)
		}
	}
	contains := func(settings, want string) {
		t.Helper()
		if got := oauth(settings); !strings.Contains(got, want) {
			t.Errorf("oauth %s: error = %q, want one containing %q", settings, got, want)
		}
	}
	exact(`"x"`, `server "s": oauth must be an object`)
	exact(`{"clientId":1}`, `server "s": oauth.clientId must be a string`)
	exact(`{"clientSecret":1}`, `server "s": oauth.clientSecret must be a string`)
	exact(`{"callbackPort":70000}`, `server "s": oauth.callbackPort must be a port number`)
	contains(`{"callbackUrl":"https://localhost/callback"}`, "oauth.callbackUrl must be an http URI")
	contains(`{"callbackUrl":"http://example.com/callback"}`, "oauth.callbackUrl must be an http URI")
	contains(`{"callbackUrl":"http://localhost/callback?x=1"}`, "oauth.callbackUrl must be an http URI")
	exact(`{"callbackUrl":"http://[::1]:8080/cb","callbackPort":8080}`, "")
	exact(`{"callbackUrl":"http://localhost:8080/cb","callbackPort":9090}`, `server "s": oauth.callbackUrl and oauth.callbackPort name different ports`)
	exact(`{"scope":1}`, `server "s": oauth.scope must be a string`)
	exact(`{"clientName":" "}`, `server "s": oauth.clientName must be a non-empty string`)
	exact(`{"clientRegistration":"x"}`, `server "s": oauth.clientRegistration must be "auto", "dcr", or "cimd"`)
	exact(`{"clientRegistration":"dcr","clientName":"app"}`, "")
	contains(`{"authServerMetadataUrl":"http://auth.example/.well-known/x"}`, "oauth.authServerMetadataUrl must be an https URL")
	exact(`{"authServerMetadataUrl":"http://127.0.0.1/.well-known/x"}`, "")
}

func TestValidateMcpServerConfigAllowsCimdOnlyWithTheApplicationsCheckOfTheCallback(t *testing.T) {
	entry := func(oauth string) string { return `{"url":"https://a.example","oauth":` + oauth + `}` }
	if got := errorOfEntry("s", entry(`{"clientRegistration":"cimd"}`), mcp.McpServerConfigOptions{}); got != `server "s": oauth.clientRegistration "cimd" is not supported: there are no Client ID Metadata Documents` {
		t.Fatalf("error = %q", got)
	}
	var checked []string
	options := mcp.McpServerConfigOptions{CheckMetadataDocumentCallback: func(callbackURL *string) string {
		if callbackURL == nil {
			checked = append(checked, "undefined")
		} else {
			checked = append(checked, *callbackURL)
		}
		if callbackURL != nil && strings.HasSuffix(*callbackURL, "/other") {
			return "the documents do not list it"
		}
		return ""
	}}
	if kind := mcp.ValidateMcpServerConfig("s", json.RawMessage(entry(`{"clientRegistration":"cimd"}`)), options).Kind; kind != mcp.McpServerConfigHTTP {
		t.Fatalf("kind = %s", kind)
	}
	if got := errorOfEntry("s", entry(`{"clientRegistration":"cimd","callbackUrl":"http://localhost/other"}`), options); got != `server "s": the documents do not list it` {
		t.Fatalf("error = %q", got)
	}
	if !reflect.DeepEqual(checked, []string{"undefined", "http://localhost/other"}) {
		t.Fatalf("checked = %+v", checked)
	}
	if got := errorOfEntry("s", entry(`{"clientRegistration":"cimd","clientId":"id"}`), options); got != `server "s": oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName` {
		t.Fatalf("error = %q", got)
	}
}

// An application's own fields, like p2's `shared` and `runOn`, checked on the result.
func validateAppConfig(name, raw string) string {
	result := mcp.ValidateMcpServerConfig(name, json.RawMessage(raw), mcp.McpServerConfigOptions{})
	if result.Kind == mcp.McpServerConfigInvalid {
		return result.Error
	}
	shared, hasShared := result.Fields["shared"]
	if hasShared && string(shared) != "true" && string(shared) != "false" {
		return `server "` + name + `": shared must be a boolean`
	}
	if _, hasRunOn := result.Fields["runOn"]; result.Kind == mcp.McpServerConfigHTTP && hasRunOn {
		return `server "` + name + `": runOn is for stdio`
	}
	if !hasShared {
		return string(result.Kind) + " shared=undefined"
	}
	return string(result.Kind) + " shared=" + string(shared)
}

func TestValidateMcpServerConfigKeepsUnknownFieldsForTheCallerAfterTheSharedFieldsAreValid(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"command":"x","shared":true,"runOn":"host"}`, "stdio shared=true"},
		{`{"url":"https://a.example","runOn":"host"}`, `server "s": runOn is for stdio`},
		{`{"command":"x","shared":1}`, `server "s": shared must be a boolean`},
		// The shared fields are reported first.
		{`{"command":"x","shared":1,"enabled":"no"}`, `server "s": enabled must be a boolean`},
	} {
		if got := validateAppConfig("s", tc.raw); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.raw, got, tc.want)
		}
	}
}

type entryFields map[string]bool

func (e entryFields) Has(key string) bool { return e[key] }

func TestIsMcpServerOverrideIsTrueForEntriesWithoutCommandURLOrType(t *testing.T) {
	for _, tc := range []struct {
		entry entryFields
		want  bool
	}{
		{entryFields{"enabled": true}, true}, {entryFields{}, true}, {entryFields{"command": true}, false}, {entryFields{"url": true}, false},
		{entryFields{"type": true, "enabled": true}, false},
	} {
		if got := mcp.IsMcpServerOverride(tc.entry); got != tc.want {
			t.Errorf("%v: %v", tc.entry, got)
		}
	}
}
