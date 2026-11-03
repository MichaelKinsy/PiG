package mcp_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// validateEntry is validateMcpServerConfig of an entry written as JSON.
func validateEntry(name, entry string, options ...mcp.McpServerConfigOptions) mcp.McpServerConfigResult {
	var opts mcp.McpServerConfigOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return mcp.ValidateMcpServerConfig(name, json.RawMessage(entry), opts)
}

// errorOf is the error of an invalid entry, or the empty string.
func errorOf(result mcp.McpServerConfigResult) string {
	if result.Kind == mcp.McpServerConfigInvalid {
		return result.Error
	}
	return ""
}

// Pi packages/mcp/test/config.test.ts "accepts stdio and HTTP entries and keeps them as given": packages/mcp/src/config.ts validateMcpServerConfig.
// mutation-checked: a result that re-encodes or drops the entry's members, a missing `streamable-http` or `http` type, and a stdio result for a url entry fail it.
func TestValidateMcpServerConfigAcceptsStdioAndHTTPEntriesAndKeepsThemAsGiven(t *testing.T) {
	stdio := `{"command":"npx","args":["-y","server"],"env":{"A":"1"},"cwd":"sub","enabled":false}`
	if got := validateEntry("files", stdio); got.Kind != mcp.McpServerConfigStdio || string(got.Config) != stdio {
		t.Errorf("stdio entry = %+v", got)
	}
	if got := validateEntry("files", `{"type":"stdio","command":"x"}`); got.Kind != mcp.McpServerConfigStdio {
		t.Errorf("typed stdio entry = %+v", got)
	}

	http := `{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer token"},"oauth":{"clientId":"id","callbackPort":8080,"scope":"read"},"timeout":30,"description":"Docs"}`
	if got := validateEntry("docs", http); got.Kind != mcp.McpServerConfigHTTP || string(got.Config) != http {
		t.Errorf("HTTP entry = %+v", got)
	}
	for _, typ := range []string{"http", "streamable-http"} {
		if got := validateEntry("docs", `{"type":"`+typ+`","url":"http://localhost:3000/mcp"}`); got.Kind != mcp.McpServerConfigHTTP {
			t.Errorf("%s entry = %+v", typ, got)
		}
	}
}

// Pi packages/mcp/test/config.test.ts "picks the transport from type, url and command".
// mutation-checked: a command that wins over url, a type that is ignored, and a missing `sse` refusal fail it.
func TestValidateMcpServerConfigPicksTheTransportFromTypeURLAndCommand(t *testing.T) {
	// With both, url wins unless the type says stdio.
	if got := validateEntry("s", `{"url":"https://a.example","command":"x"}`); got.Kind != mcp.McpServerConfigHTTP {
		t.Errorf("url and command = %+v", got)
	}
	if got := validateEntry("s", `{"type":"stdio","url":"https://a.example","command":"x"}`); got.Kind != mcp.McpServerConfigStdio {
		t.Errorf("stdio type with url and command = %+v", got)
	}
	if got, want := errorOf(validateEntry("s", `{"type":"http","command":"x"}`)), `server "s" needs either "command" (stdio) or "url" (streamable HTTP)`; got != want {
		t.Errorf("http type with command = %q, want %q", got, want)
	}
	if got, want := errorOf(validateEntry("s", `{"type":"sse","url":"https://a.example/sse"}`)), `server "s": legacy SSE transport is not supported; use the streamable HTTP URL`; got != want {
		t.Errorf("sse type = %q, want %q", got, want)
	}
}

// Pi packages/mcp/test/config.test.ts "reports invalid names and fields".
// mutation-checked: each message edited, a missing check of one field, and a `>=` for `>` in the timeout check fail it.
func TestValidateMcpServerConfigReportsInvalidNamesAndFields(t *testing.T) {
	for _, tc := range []struct{ name, entry, want string }{
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
		if got := errorOf(validateEntry(tc.name, tc.entry)); got != tc.want {
			t.Errorf("validate(%q, %s) error = %q, want %q", tc.name, tc.entry, got, tc.want)
		}
	}
}

// Pi packages/mcp/test/config.test.ts "checks oauth settings".
// mutation-checked: each oauth check dropped or its message edited, a loopback list without `[::1]`, and a port range of 0..65535 fail it.
func TestValidateMcpServerConfigChecksOAuthSettings(t *testing.T) {
	oauth := func(settings string) string {
		return errorOf(validateEntry("s", `{"url":"https://a.example","oauth":`+settings+`}`))
	}
	for _, tc := range []struct{ settings, want string }{
		{`"x"`, `server "s": oauth must be an object`},
		{`{"clientId":1}`, `server "s": oauth.clientId must be a string`},
		{`{"clientSecret":1}`, `server "s": oauth.clientSecret must be a string`},
		{`{"callbackPort":70000}`, `server "s": oauth.callbackPort must be a port number`},
		{`{"callbackPort":0}`, `server "s": oauth.callbackPort must be a port number`},
		{`{"callbackPort":1.5}`, `server "s": oauth.callbackPort must be a port number`},
		{`{"callbackUrl":"http://[::1]:8080/cb","callbackPort":8080}`, ``},
		{`{"callbackUrl":"http://localhost:8080/cb","callbackPort":9090}`, `server "s": oauth.callbackUrl and oauth.callbackPort name different ports`},
		{`{"scope":1}`, `server "s": oauth.scope must be a string`},
		{`{"clientName":" "}`, `server "s": oauth.clientName must be a non-empty string`},
		{`{"clientRegistration":"x"}`, `server "s": oauth.clientRegistration must be "auto", "dcr", or "cimd"`},
		{`{"clientRegistration":"dcr","clientName":"app"}`, ``},
		{`{"authServerMetadataUrl":"http://127.0.0.1/.well-known/x"}`, ``},
	} {
		if got := oauth(tc.settings); got != tc.want {
			t.Errorf("oauth %s error = %q, want %q", tc.settings, got, tc.want)
		}
	}
	for _, tc := range []struct{ settings, want string }{
		{`{"callbackUrl":"https://localhost/callback"}`, "oauth.callbackUrl must be an http URI"},
		{`{"callbackUrl":"http://example.com/callback"}`, "oauth.callbackUrl must be an http URI"},
		{`{"callbackUrl":"http://localhost/callback?x=1"}`, "oauth.callbackUrl must be an http URI"},
		{`{"authServerMetadataUrl":"http://auth.example/.well-known/x"}`, "oauth.authServerMetadataUrl must be an https URL"},
	} {
		if got := oauth(tc.settings); !strings.Contains(got, tc.want) {
			t.Errorf("oauth %s error = %q, want it to contain %q", tc.settings, got, tc.want)
		}
	}
}

// Pi packages/mcp/test/config.test.ts "allows cimd only with the application's check of the callback".
// mutation-checked: a cimd entry accepted without the check, a check that is not called with the callback URL, a check error that is dropped, and a cimd entry that may carry a clientId fail it.
func TestValidateMcpServerConfigAllowsCimdOnlyWithTheApplicationsCheckOfTheCallback(t *testing.T) {
	entry := func(oauth string) string { return `{"url":"https://a.example","oauth":` + oauth + `}` }
	if got, want := errorOf(validateEntry("s", entry(`{"clientRegistration":"cimd"}`))), `server "s": oauth.clientRegistration "cimd" is not supported: there are no Client ID Metadata Documents`; got != want {
		t.Errorf("cimd without the check = %q, want %q", got, want)
	}
	// An empty callback URL is the check's undefined: none is configured.
	var checked []string
	options := mcp.McpServerConfigOptions{
		CheckMetadataDocumentCallback: func(callbackURL string) string {
			checked = append(checked, callbackURL)
			if strings.HasSuffix(callbackURL, "/other") {
				return "the documents do not list it"
			}
			return ""
		},
	}
	if got := validateEntry("s", entry(`{"clientRegistration":"cimd"}`), options); got.Kind != mcp.McpServerConfigHTTP {
		t.Errorf("cimd with a passing check = %+v", got)
	}
	if got, want := errorOf(validateEntry("s", entry(`{"clientRegistration":"cimd","callbackUrl":"http://localhost/other"}`), options)), `server "s": the documents do not list it`; got != want {
		t.Errorf("cimd with a failing check = %q, want %q", got, want)
	}
	if want := []string{"", "http://localhost/other"}; !reflect.DeepEqual(checked, want) {
		t.Errorf("callback URLs checked = %q, want %q", checked, want)
	}
	if got, want := errorOf(validateEntry("s", entry(`{"clientRegistration":"cimd","clientId":"id"}`), options)), `server "s": oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName`; got != want {
		t.Errorf("cimd with a clientId = %q, want %q", got, want)
	}
}

// Pi packages/mcp/test/config.test.ts "extending validateMcpServerConfig": an application checks its own fields on the entry the result keeps, like p2's `shared` and `runOn`.
// mutation-checked: a result that drops fields the validator does not know, and shared fields that are checked after the application's own fail it.
func TestValidateMcpServerConfigKeepsUnknownFieldsForTheCallerAfterTheSharedFieldsAreValid(t *testing.T) {
	validateAppConfig := func(name, entry string) string {
		result := validateEntry(name, entry)
		if result.Kind == mcp.McpServerConfigInvalid {
			return result.Error
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(result.Config, &fields); err != nil {
			t.Fatal(err)
		}
		shared, hasShared := fields["shared"]
		if hasShared && string(shared) != "true" && string(shared) != "false" {
			return `server "` + name + `": shared must be a boolean`
		}
		if _, hasRunOn := fields["runOn"]; result.Kind == mcp.McpServerConfigHTTP && hasRunOn {
			return `server "` + name + `": runOn is for stdio`
		}
		return string(result.Kind) + " shared=" + string(shared)
	}
	for _, tc := range []struct{ entry, want string }{
		{`{"command":"x","shared":true,"runOn":"host"}`, "stdio shared=true"},
		{`{"url":"https://a.example","runOn":"host"}`, `server "s": runOn is for stdio`},
		{`{"command":"x","shared":1}`, `server "s": shared must be a boolean`},
		// The shared fields are reported first.
		{`{"command":"x","shared":1,"enabled":"no"}`, `server "s": enabled must be a boolean`},
	} {
		if got := validateAppConfig("s", tc.entry); got != tc.want {
			t.Errorf("app validation of %s = %q, want %q", tc.entry, got, tc.want)
		}
	}
}

// Pi packages/mcp/test/config.test.ts "isMcpServerOverride": packages/mcp/src/config.ts isMcpServerOverride; a member that is present with a null value counts as present, as `!== undefined` does.
// mutation-checked: dropping any of the three members from the check fails it.
func TestIsMcpServerOverride(t *testing.T) {
	for _, tc := range []struct {
		entry string
		want  bool
	}{
		{`{"enabled":false}`, true},
		{`{}`, true},
		{`{"command":"x"}`, false},
		{`{"url":"https://a.example"}`, false},
		{`{"type":"stdio","enabled":false}`, false},
		{`{"command":null}`, false},
	} {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.entry), &entry); err != nil {
			t.Fatal(err)
		}
		if got := mcp.IsMcpServerOverride(entry); got != tc.want {
			t.Errorf("IsMcpServerOverride(%s) = %v, want %v", tc.entry, got, tc.want)
		}
	}
}
