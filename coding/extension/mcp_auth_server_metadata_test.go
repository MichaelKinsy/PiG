package extension

import (
	"encoding/json"
	"testing"
)

// mcp-servers.ts validateOAuth: `oauth.authServerMetadataUrl` must parse as a URL and use https, or http on a loopback
// host; the accepted value reaches the config.
func TestValidateMcpServerConfigChecksTheAuthServerMetadataURL(t *testing.T) {
	const message = "oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]"
	cases := []struct {
		value string
		valid bool
	}{
		{`"https://idp.example/.well-known/openid-configuration"`, true},
		{`"http://localhost:8080/m"`, true},
		{`"http://127.0.0.1/m"`, true},
		{`"http://[::1]:9/m"`, true},
		{`"HTTPS://IDP.example/m"`, true},
		// Node's URL parser strips surrounding whitespace, drops tabs and newlines, and serializes loopback hosts.
		{`" https://idp.example/\tm\n"`, true},
		{`"http://127.1/m"`, true},
		{`"http://0x7f.0.0.1/m"`, true},
		{`"http://LOCALHOST/m"`, true},
		{`"http://[0:0::1]/m"`, true},
		{`"http://localhost./m"`, false},
		{`"https://idp.example:99999/m"`, false},
		{`"http://idp.example/m"`, false},
		{`"http://127.0.0.2/m"`, false},
		{`"ftp://idp.example/m"`, false},
		{`"not a url"`, false},
		{`""`, false},
		{`null`, false},
		{`7`, false},
	}
	for _, c := range cases {
		config, failure := ValidateMcpServerConfig("idp", json.RawMessage(`{"url":"https://mcp.example/mcp","oauth":{"authServerMetadataUrl":`+c.value+`}}`))
		if c.valid {
			var want string
			_ = json.Unmarshal([]byte(c.value), &want)
			if failure != "" || config.OAuth == nil || config.OAuth.AuthServerMetadataURL != want {
				t.Fatalf("%s: failure = %q, config = %#v", c.value, failure, config.OAuth)
			}
			continue
		}
		if failure != `server "idp": `+message {
			t.Fatalf("%s: failure = %q", c.value, failure)
		}
	}
}
