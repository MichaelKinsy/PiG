package oauth

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

func TestParseURLMatchesNodeNewURL(t *testing.T) {
	inputs := []string{
		"com.example.app:/callback",
		"myapp://Callback/Path?x=1#f",
		"urn:ietf:wg:oauth:2.0:oob",
		"ws://Example.COM:80/a/../b",
		"FILE:///tmp/a b",
		"custom://user:pw@HOST:8080/p%41th/./x",
		"custom://host/a b",
		"custom:path with space",
		"mailto:Someone@Example.com",
		"x://[::1]:99/",
		"foo://h/%zz",
		"ftp://example.com:21/x",
		"git+ssh://git@github.com/a/b.git",
		"custom://exa mple/",
		"custom:///triple",
		// net/url cannot spell these authorities and paths, so they serialize from the WHATWG text: an empty authority, port
		// 0 of a non-special scheme, a percent-encoded or non-ASCII opaque host, a backslash in a path, malformed escapes in
		// the path and credentials, and a "?" inside the fragment.
		"x://", "x://h:0/", "custom://h:0/p", "x://H%41ST/", "x://ä.com/", "myapp://ä/cb?x=1", "x://h/\\a\\b", "x://u%zz:p%zz@h/",
		"x://us er@h/", "x://h/%zz?q", "x://h/%", "x://h/a%zz/../b", "x://h/p#a?b", "x://h/p?#a?b", "x://h/a/./b/../c", "x:/a/../b",
		"x:opaque?q#f", "x:opa%zzque", "x://h/p?a b#c d", "x://h/é?é#é", "  x://h/trim  ", "X://H/", "x://h:65536/",
		"wss://e.com:443/", "ftp://e.com:21", "file:///C:/a/../b", "data:text/plain,hi there",
	}
	b, _ := json.Marshal(inputs)
	out, err := exec.Command("node", "-e", `const xs=JSON.parse(process.argv[1]);console.log(JSON.stringify(xs.map(x=>{try{return new URL(x).href}catch(e){return "THROW"}})))`, string(b)).Output()
	if err != nil {
		t.Fatal(err)
	}
	var node []string
	if err := json.Unmarshal(out, &node); err != nil {
		t.Fatal(err)
	}
	for i, in := range inputs {
		got := "THROW"
		if u, err := ParseURL(in); err == nil {
			got = u.String()
		}
		g2 := "THROW"
		if u, err := nodeurl.ParseURL(in); err == nil {
			g2 = u.Href()
		}
		mark := ""
		if got != node[i] {
			mark = "  <-- mcp/oauth.ParseURL differs"
		}
		if g2 != node[i] {
			mark += "  <-- nodeurl differs"
		}
		if got != node[i] {
			t.Errorf("ParseURL(%q) = %q, Node new URL = %q (nodeurl.ParseURL = %q)%s", in, got, node[i], g2, mark)
		}
	}
}

// A caller outside PiG cannot import internal/nodeurl, so ParseURL is the way to build the URL that
// AuthorizationServerMetadataURL takes; Href serializes what it parsed.
func TestParseURLIsHowACallerMakesAURL(t *testing.T) {
	parsed, err := ParseURL("https://auth.example.com/meta#f")
	if err != nil || parsed.Href() != "https://auth.example.com/meta#f" || parsed.Hash != "#f" {
		t.Fatalf("ParseURL = %q (hash %q), %v", parsed.Href(), parsed.Hash, err)
	}
	if _, err := ParseURL("/relative"); err == nil || err.Error() != "Invalid URL" {
		t.Fatalf("ParseURL(relative) error = %v, want Invalid URL", err)
	}
}
