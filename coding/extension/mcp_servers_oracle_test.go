package extension

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type mcpOracleCase struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

type mcpOracleResult struct {
	Error        string            `json:"error"`
	Exposure     *string           `json:"exposure"`
	ToolExposure map[string]string `json:"toolExposure"`
	Exposures    []string          `json:"exposures"`
}

// mcpOracleCorpus is every field of a stdio and an HTTP entry set to a value of each JSON type and to the values the validator branches on.
func mcpOracleCorpus() []mcpOracleCase {
	bases := map[string]string{
		"stdio": `{"command":"x"}`,
		"http":  `{"url":"https://x.example/mcp"}`,
	}
	values := []string{
		`null`, `true`, `false`, `0`, `1`, `-1`, `1.5`, `65535`, `65536`, `1e3`, `""`, `"a"`, ` "  "`, `"\u00a0\u2003"`, `[]`, `["a"]`, `[1]`, `{}`, `{"a":"b"}`, `{"a":1}`,
		`"codemode"`, `"deferred"`, `"direct"`, `"hidden"`, `"codemode-deferred"`, `"bogus"`,
		`"http"`, `"stdio"`, `"streamable-http"`, `"sse"`,
		`"http://localhost:8080/oauth/callback"`, `"http://127.0.0.1/callback"`, `"http://[::1]:9/callback"`, `"http://localhost/callback?x=1"`, `"http://localhost/callback#f"`, `"https://localhost/callback"`,
		`"http://localhost/a/../callback"`, `"http://localhost/other"`, `"https://auth.example/.well-known/x"`, `"http://127.0.0.1:1/x"`, `"http://evil.example/x"`, `"ftp://x.example"`, `"not a url"`,
		`{"codemode-deferred":"codemode-deferred","a_*":"direct"}`, `{"a":"bogus"}`, `{"a*":"hidden","*":"direct"}`,
		`"dcr"`, `"cimd"`, `{"provider":"p"}`, `{"provider":""}`, `{"provider":1}`,
	}
	fields := []string{
		"type", "exposure", "description", "toolExposure", "enabled", "timeout", "args", "env", "cwd", "headers", "url", "command", "oauth", "auth",
	}
	var cases []mcpOracleCase
	add := func(name, config string) {
		cases = append(cases, mcpOracleCase{Name: name, Config: json.RawMessage(config)})
	}
	for kind, base := range bases {
		add("s", base)
		for _, field := range fields {
			for _, value := range values {
				config := strings.TrimSuffix(base, "}") + fmt.Sprintf(`,%q:%s}`, field, value)
				add("s", config)
				if kind == "http" {
					add("s", fmt.Sprintf(`{"url":"https://x.example","type":"http",%q:%s}`, field, value))
				}
			}
		}
	}
	oauthFields := []string{"clientId", "clientSecret", "callbackPort", "callbackUrl", "scope", "clientName", "clientRegistration", "authServerMetadataUrl"}
	for _, field := range oauthFields {
		for _, value := range values {
			add("s", fmt.Sprintf(`{"url":"https://x.example","oauth":{%q:%s}}`, field, value))
			add("s", fmt.Sprintf(`{"url":"https://x.example","oauth":{"clientRegistration":"cimd",%q:%s}}`, field, value))
			add("s", fmt.Sprintf(`{"url":"https://x.example","oauth":{"callbackPort":8080,%q:%s}}`, field, value))
		}
	}
	for _, url := range []string{`http://x.example`, `http://localhost`, `http://[::1]`, `http://127.0.0.1:99`, `https://x.example`, `HTTP://LOCALHOST`, `http://LocalHost:8/`, `http://0x7f.1/`, `http://127.1/`} {
		add("s", fmt.Sprintf(`{"url":%q,"auth":{"provider":"p"}}`, url))
	}
	for _, name := range []string{"", "a b", "a-b_C1", "é", "a.b", "a\n", "x/y"} {
		add(name, `{"command":"x"}`)
	}
	add("s", `[]`)
	add("s", `"x"`)
	add("s", `null`)
	add("s", `1`)
	add("s", `{"command":"x","exposure":"codemode-deferred","toolExposure":{"b":"codemode-deferred","a":"hidden"}}`)
	return cases
}

const mcpOracleBody = `
const mod = await load("pi-coding-agent/core/mcp-servers.js");
const tools = input.tools;
emit(input.cases.map((c) => {
	const r = mod.validateMcpServerConfig(c.name, c.config);
	if (typeof r === "string") return { error: r };
	return {
		exposure: r.exposure ?? null,
		toolExposure: r.toolExposure ?? null,
		exposures: tools.map((tool) => mod.getMcpToolExposure(r, tool)),
	};
}));
`

// TestValidateMcpServerConfigMatchesPi runs the corpus through Pi 1.0.4's validateMcpServerConfig and getMcpToolExposure.
func TestValidateMcpServerConfigMatchesPi(t *testing.T) {
	cases := mcpOracleCorpus()
	tools := []string{"a", "ab", "a_b", "b", "x\ny", "*", "list", ""}
	for i, c := range cases {
		if !json.Valid(c.Config) {
			t.Fatalf("corpus %d is not JSON: %s", i, c.Config)
		}
	}
	var want []mcpOracleResult
	pioracle.Run(t, mcpOracleBody, map[string]any{"cases": cases, "tools": tools}, &want)
	if len(want) != len(cases) {
		t.Fatalf("oracle returned %d results for %d cases", len(want), len(cases))
	}
	accepted := 0
	for i, c := range cases {
		config, message := ValidateMcpServerConfig(c.Name, c.Config)
		w := want[i]
		if message != w.Error {
			t.Errorf("name=%q config=%s: message = %q, Pi %q", c.Name, c.Config, message, w.Error)
			continue
		}
		if w.Error != "" {
			continue
		}
		accepted++
		exposure := ""
		if w.Exposure != nil {
			exposure = *w.Exposure
		}
		if string(config.Exposure) != exposure {
			t.Errorf("config=%s: exposure = %q, Pi %q", c.Config, config.Exposure, exposure)
		}
		keys := config.ToolExposure.Keys()
		if len(keys) != len(w.ToolExposure) {
			t.Errorf("config=%s: toolExposure keys = %v, Pi %v", c.Config, keys, w.ToolExposure)
		}
		for _, key := range keys {
			if got, _ := config.ToolExposure.Get(key); string(got) != w.ToolExposure[key] {
				t.Errorf("config=%s: toolExposure[%q] = %q, Pi %q", c.Config, key, got, w.ToolExposure[key])
			}
		}
		for j, tool := range tools {
			if got := GetMcpToolExposure(config, tool); string(got) != w.Exposures[j] {
				t.Errorf("config=%s: GetMcpToolExposure(%q) = %q, Pi %q", c.Config, tool, got, w.Exposures[j])
			}
		}
	}
	if accepted < 100 {
		t.Fatalf("only %d of %d corpus entries were accepted; the corpus no longer reaches the success path", accepted, len(cases))
	}
}

// TestValidateMcpServerConfigKeepsAnIntegralCallbackPortWrittenAsAnyNumberForm: Pi accepts `8e3` and `8080.0` as integers (`Number.isInteger`), so the validated entry carries the port.
func TestValidateMcpServerConfigKeepsAnIntegralCallbackPortWrittenAsAnyNumberForm(t *testing.T) {
	for text, want := range map[string]int{`8080`: 8080, `8e3`: 8000, `8080.0`: 8080, `1E3`: 1000} {
		config, message := ValidateMcpServerConfig("s", json.RawMessage(`{"url":"https://x.example","oauth":{"callbackPort":`+text+`}}`))
		if message != "" || config.OAuth == nil || config.OAuth.CallbackPort == nil || *config.OAuth.CallbackPort != want {
			t.Errorf("callbackPort %s: config.OAuth = %+v, message %q, want port %d", text, config.OAuth, message, want)
		}
	}
}

// TestMcpHelpersMatchPi compares createToolNameMatcher, isMcpToolName, isLoopbackRedirectUri and mcpNamespace with Pi 1.0.4.
func TestMcpHelpersMatchPi(t *testing.T) {
	entrySets := [][]string{
		{}, {"read"}, {"*"}, {"mcp__*"}, {"a*b"}, {"a.b", "c+d"}, {"[x]*", "(y)?"}, {"^*$"}, {"*\\*"}, {"a*", "*z"}, {"é*"}, {"a**b"},
	}
	names := []string{"", "read", "mcp__s__t", "ab", "a.b", "axb", "a\nb", "a\u2028b", "c+d", "ccd", "[x]1", "x1", "(y)?", "y", "^a$", "\\", "éa", "list_mcp_resources", "read_mcp_resource", "list_mcp_resource_templates", "mcp_", "mcp__"}
	uris := []string{"", "http://localhost:8080/cb", "http://127.0.0.1/cb", "http://[::1]/cb", "https://localhost/cb", "http://localhost/cb?x", "http://localhost/cb#", "http://localhost/cb#x", "http://LOCALHOST/cb", "http://0x7f.1/", "http://127.1/", "nope", "http://localhost:99999/", "http://user@localhost/"}
	servers := []string{"a", "a-b", "a-b-c", "-", "a_b"}
	var want struct {
		Matches    [][]bool `json:"matches"`
		IsMcp      []bool   `json:"isMcp"`
		Loopback   []bool   `json:"loopback"`
		Namespaces []string `json:"namespaces"`
	}
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/mcp-servers.js");
emit({
	matches: input.entrySets.map((entries) => { const m = mod.createToolNameMatcher(entries); return input.names.map((n) => m(n)); }),
	isMcp: input.names.map((n) => mod.isMcpToolName(n)),
	loopback: input.uris.map((u) => mod.isLoopbackRedirectUri(u)),
	namespaces: input.servers.map((s) => mod.mcpNamespace(s)),
});`, map[string]any{"entrySets": entrySets, "names": names, "uris": uris, "servers": servers}, &want)
	for i, entries := range entrySets {
		set := map[string]struct{}{}
		for _, e := range entries {
			set[e] = struct{}{}
		}
		matcher := ToolNameMatcher(set)
		for j, name := range names {
			if got := matcher(name); got != want.Matches[i][j] {
				t.Errorf("matcher%q(%q) = %v, Pi %v", entries, name, got, want.Matches[i][j])
			}
		}
	}
	for j, name := range names {
		if got := IsMcpToolName(name); got != want.IsMcp[j] {
			t.Errorf("IsMcpToolName(%q) = %v, Pi %v", name, got, want.IsMcp[j])
		}
	}
	for j, uri := range uris {
		if got := IsLoopbackRedirectURI(uri); got != want.Loopback[j] {
			t.Errorf("IsLoopbackRedirectURI(%q) = %v, Pi %v", uri, got, want.Loopback[j])
		}
	}
	for j, server := range servers {
		if got := McpNamespace(server); got != want.Namespaces[j] {
			t.Errorf("McpNamespace(%q) = %q, Pi %q", server, got, want.Namespaces[j])
		}
	}
}
