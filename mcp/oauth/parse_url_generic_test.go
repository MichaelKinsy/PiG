package oauth

import "testing"

// ParseURL is `new URL(text)` (the schema validators read every URL field that way). For a non-http scheme the result must serialise as the WHATWG
// href and keep Node's `host`, `username` and `password`. The expectations were produced by Node's `new URL(text)`: href, host, and
// username/password (Node returns them percent-encoded). They cover user only, user and password, password only, an empty password, explicit and
// default ports, percent-encoding and an opaque path.
// Pi: packages/mcp/src/oauth/types.ts (URL fields), Node `new URL(...)`
func TestParseURLGenericSchemesKeepNodesHostAndUserinfo(t *testing.T) {
	for _, tc := range []struct {
		in, href, host, user, password string
	}{
		{"ftp://user:pw@host:2121/p", "ftp://user:pw@host:2121/p", "host:2121", "user", "pw"},
		{"ftp://user@host/p", "ftp://user@host/p", "host", "user", ""},
		{"ftp://:pw@host/p", "ftp://:pw@host/p", "host", "", "pw"},
		{"ftp://host:21/", "ftp://host/", "host", "", ""},
		{"ws://h:80", "ws://h/", "h", "", ""},
		{"custom://u:p@h:1/a?q=1#f", "custom://u:p@h:1/a?q=1#f", "h:1", "u", "p"},
		{"custom://us%20er:p%40w@h/a b", "custom://us%20er:p%40w@h/a%20b", "h", "us%20er", "p%40w"},
		{"ftp://user:@host/", "ftp://user@host/", "host", "user", ""},
		{"ftp://host/p", "ftp://host/p", "host", "", ""},
	} {
		got, err := ParseURL(tc.in)
		if err != nil {
			t.Errorf("ParseURL(%q): %v", tc.in, err)
			continue
		}
		host := got.Hostname
		if got.Port != "" {
			host += ":" + got.Port
		}
		if got.String() != tc.href || host != tc.host || got.Username != tc.user || got.Password != tc.password {
			t.Errorf("ParseURL(%q) = {href %q, host %q, user %q, password %q}, want {%q, %q, %q, %q}", tc.in, got.String(), host, got.Username, got.Password,
				tc.href, tc.host, tc.user, tc.password)
		}
	}
}
