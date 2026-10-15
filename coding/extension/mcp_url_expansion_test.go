package extension

import (
	"encoding/json"
	"testing"
)

// Pi #10654: mcp-servers.ts validateMcpServerConfig checks the raw `url`, so `{"url":"${MY_VAR}"}` is rejected even
// when MY_VAR holds an http(s) URL, while headers and env values of the same entry are expanded like API keys. The
// URL is a config value: validation, the transport and the OAuth URL see its expansion.
func TestValidateMcpServerConfigChecksTheExpandedURL(t *testing.T) {
	t.Setenv("PIG_MCP_URL_TEST", "https://x.example/mcp")
	t.Setenv("PIG_MCP_URL_HOST", "x.example")
	t.Setenv("PIG_MCP_URL_BAD", "ftp://x.example/mcp")
	t.Setenv("PIG_MCP_URL_PLAIN", "http://x.example/mcp")
	t.Setenv("PIG_MCP_URL_LOOPBACK", "http://localhost:8788/mcp")
	for _, tc := range []struct{ name, config, want string }{
		{"whole url", `{"url":"${PIG_MCP_URL_TEST}"}`, ""},
		{"bare variable", `{"url":"$PIG_MCP_URL_TEST"}`, ""},
		{"host only", `{"url":"https://${PIG_MCP_URL_HOST}/mcp"}`, ""},
		{"expands to another scheme", `{"url":"${PIG_MCP_URL_BAD}"}`, `server "x": url must be an http or https URL`},
		{"auth needs https after expansion", `{"url":"${PIG_MCP_URL_PLAIN}","auth":{"provider":"p"}}`, `server "x": auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]`},
		{"auth on loopback after expansion", `{"url":"${PIG_MCP_URL_LOOPBACK}","auth":{"provider":"p"}}`, ""},
		{"unset variable keeps the raw check", `{"url":"${PIG_MCP_URL_UNSET_VARIABLE}"}`, `server "x": url must be an http or https URL`},
		{"command is not run", `{"url":"!echo https://x.example"}`, `server "x": url must be an http or https URL`},
		{"literal url without references", `{"url":"https://x.example/a?b=1"}`, ""},
	} {
		config, message := ValidateMcpServerConfig("x", json.RawMessage(tc.config))
		if message != tc.want {
			t.Errorf("%s: message = %q, want %q", tc.name, message, tc.want)
		}
		if message == "" && config.URL == "" {
			t.Errorf("%s: the validated config lost its url", tc.name)
		}
	}
}

func TestExpandMcpURL(t *testing.T) {
	t.Setenv("PIG_MCP_URL_TEST", "https://x.example/mcp")
	for raw, want := range map[string]string{
		"${PIG_MCP_URL_TEST}":             "https://x.example/mcp",
		"https://h/$$a":                   "https://h/$a",
		"https://h/a$PIG_MCP_URL_UNSET_Z": "https://h/a$PIG_MCP_URL_UNSET_Z", // an unset variable leaves a literal URL as written
		"!echo https://x.example":         "!echo https://x.example",
		"https://x.example/plain":         "https://x.example/plain",
	} {
		if got := ExpandMcpURL(raw); got != want {
			t.Errorf("ExpandMcpURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
