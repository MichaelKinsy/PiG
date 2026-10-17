package nodeurl

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

// ParseURL against Node's own `new URL(value)` for every scheme (non-special, http, https, ws, wss, ftp and file): opaque paths,
// authority-less paths, hosts with credentials and ports, dot segments, percent sequences, fragments and malformed inputs.
func TestParseURLMatchesNode(t *testing.T) {
	inputs := []string{
		"github:user/repo", "github:user/repo#main", "github:user/repo#", "gist:abc123", "gitlab:group/sub/repo.git#a b", "git+ssh://git@github.com/user/repo.git#v1",
		"git+https://token@github.com/user/repo.git", "git+https://u:p@github.com:8443/user/repo", "ssh://git@Example.COM/o/r", "ssh://git@example.com:22/o/r",
		"git://github.com/user/repo", "git:github.com/user/repo", "git:", "git:/abs/path", "git://", "git:///x", "ssh://@host/x", "ssh://:pw@host/x", "ssh://host:/x",
		"ssh://host:99999/x", "ssh://host:ab/x", "ssh://ho st/x", "ssh://host", "ssh://host/", "ssh://host/a/../b", "ssh://host/a/./b/.", "ssh://host/a/%2e%2e/b",
		"ssh://host/a b", "ssh://host/a\\b", "ssh://host/a%2Fb", "ssh://host/a%zz", "ssh://host/é", "ssh://hóst/x", "ssh://host/a?x=1#frag", "ssh://host/a#f g<h>", "x-y.z+w:opaque path",
		"a:b", "1a:b", ":x", "nocolon", "", "  git:user/repo  ", "git:us\ter/re\npo", "https://github.com/user/repo", "https://GitHub.com/User/Repo.git#v1", "https://github.com:443/a",
		"https://user:pass@example.com/a/b?q#h", "https:github.com/x", "https:\\\\github.com\\x\\y", "HTTPS://github.com/x", "https://example.com/a/b%2Fc", "https://example.com/a\\b",
		"https://exa mple.com/", "https://", "https://@/x", "ws://host/x", "wss://host:443/x", "ws://host:80/a", "ftp://user:pw@host:21/x", "ftp://host:2121/a b", "file:///x", "file://host/x?q#f", "file:/x", "file:///C:/a/../b", "file://localhost/x", "file://a@b/x", "x://[::1]/p", "x://[::1]:8080/p", "x://[::g]/", "x://[::1", "ssh://git@[fe80::1]:22/o/r", "javascript:alert(1)", "mailto:a@b.c", "git+ssh://git@github.com:user/repo",
		"git@github.com:user/repo", "ssh://git@github.com/user/repo#a#b", "ssh://host/path#", "ssh://host/path?", "ssh://host?q",
		// The C0-control percent-encode set boundaries (U+001F, U+007F) in an opaque path, a path and a host.
		"x:a\x1fb", "x:a\x7fb", "x:a\x20b", "ssh://host/a\x1fb", "ssh://host/a\x7fb", "ssh://host/a~b", "ssh://ho\x7fst/x", "ssh://ho\x1fst/x",
		// An opaque path's space directly before "?" or "#" is "%20"; other spaces stay, and a trailing space at the end of the input is trimmed.
		"x:a  #f", "x:a  ?q", "x:a b?q", "x: ?q", "x:  #", "x:a \t?q", "x:a%20 #f", "x:a  ", "mailto: a ?b",
		// A host-less path that starts with "//" ("/." in the href) and a path with an empty host.
		"x:/.//p", "x:/..//p?q", "x:///.//p",
		// A blob URL's origin is that of the http or https URL in its path, and opaque otherwise.
		"blob:https://a.B/x", "blob:http://u:p@h:80/x?y#z", "blob:ws://a/x", "blob:file:///x", "blob:foo", "BLOB:https://a:443/x",
		// Port 0 is a real port of a non-special scheme, which has no default to drop; leading zeros are removed.
		"x://h:0/", "x://h:00/p", "x://h:000080/", "ssh://git@host:0/r", "http://h:0/", "ws://h:00/",
	}
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/parse_url.mjs")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var expected []*struct {
		Protocol string `json:"protocol"`
		Username string `json:"username"`
		Password string `json:"password"`
		Hostname string `json:"hostname"`
		Pathname string `json:"pathname"`
		Hash     string `json:"hash"`
		Origin   string `json:"origin"`
		Href     string `json:"href"`
		Searched string `json:"searched"`
		Replaced string `json:"replaced"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, in := range inputs {
		want := expected[i]
		got, err := ParseURL(in)
		switch {
		case want == nil && err == nil:
			t.Errorf("ParseURL(%q) = %+v, Node throws", in, got)
		case want != nil && err != nil:
			t.Errorf("ParseURL(%q) = %v, Node %+v", in, err, *want)
		case want != nil && (got.Protocol != want.Protocol || got.Username != want.Username || got.Password != want.Password || got.Hostname != want.Hostname || got.Pathname != want.Pathname || got.Hash != want.Hash || got.Origin() != want.Origin || got.Href() != want.Href || got.WithSearch("?a b'c\"d<e>#f&g=h") != want.Searched):
			t.Errorf("ParseURL(%q) = %+v, Node %+v", in, got, *want)
		case want != nil:
			// pathname set + toString: an opaque path cannot be replaced, so the href stays as parsed.
			if replaced := got.WithPathname("/new path/x%2Fy?z"); replaced != want.Replaced {
				t.Errorf("ParseURL(%q).WithPathname = %q, Node %q", in, replaced, want.Replaced)
			}
		}
	}
}
