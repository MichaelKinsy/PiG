package ai

// pi: packages/ai/src/auth/oauth/radius.ts
// pi: packages/ai/src/providers/radius-config.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type radiusURLProbe struct {
	Op       string `json:"op"`
	Gateway  string `json:"gateway"`
	Path     string `json:"path"`
	Endpoint string `json:"endpoint"`
	Query    string `json:"query"`
}

// radiusURLProbes compose gateway and authorization-endpoint URLs from spellings that the WHATWG parser normalizes differently from Go's net/url: scheme and host case,
// default and invalid ports, userinfo, dot segments, backslashes, tabs and newlines, IDN and numeric IPv4/IPv6 hosts, trailing slashes, queries and fragments, spaces and non-ASCII text.
func radiusURLProbes() []radiusURLProbe {
	random := rand.New(rand.NewSource(20260941))
	schemes := []string{"https://", "http://", "HTTPS://", "HtTp://", "", "ftp://", "ws://", "file:///", "foo://", "https:/", "https:\\\\", "//"}
	userinfos := []string{"", "", "", "user@", "user:pw@", "us er:p%w@", "ü:ß@", "a@b@", ":@"}
	hosts := []string{"radius.example", "RADIUS.Example", "localhost", "localhost:8788", "example.com:443", "example.com:80", "example.com:99999", "example.com:", "127.0.0.1", "0x7f.1", "[::1]", "[::1]:3000", "[::ffff:1.2.3.4]",
		"bücher.example", "xn--bcher-kva.example", "EXAMPLE.com.", "a b.example", "ex%41mple.com", "exa_mple.com", "1.2.3", "256.1.1.1", "", "a\tb.example", "x\ny.example", "\u00ad.example", "ＥＸＡＭＰＬＥ.com"}
	paths := []string{"", "/", "//", "/a", "/a/", "/a/../b", "/a/./b/", "/%2e%2e/x", "\\a\\b", "/a b", "/ü", "/a%2Fb", "/a?x=1", "/a#frag", "?q", "#f", "/a\tb", "/a|b", "/{x}", "/a^b", "/`"}
	var probes []radiusURLProbe
	build := func() string {
		return schemes[random.Intn(len(schemes))] + userinfos[random.Intn(len(userinfos))] + hosts[random.Intn(len(hosts))] + paths[random.Intn(len(paths))]
	}
	for range 2500 {
		probes = append(probes, radiusURLProbe{Op: "resolve", Gateway: build(), Path: []string{"/v1/config", "/v1/oauth", "/v1/oauth/token", "/v1/oauth/device"}[random.Intn(4)]})
	}
	rawGateways := []string{"mailto:a@b", "data:text/plain,x", "foo:bar", "foo:/p/q", "foo://h/x?y#z", "file:///a/b", "file://host/a", "ftp://u:p@h:21/x", "ws://h:80/", "wss://h:443/x", "javascript:0", "about:blank", "https://a.example/x?y#z", "HTTP://H:8080", "foo://[::1]:9/", "foo://h:99999", "foo://@h", "x", "", "//h", "/p"}
	for _, gateway := range rawGateways {
		for _, path := range []string{"/v1/config", "/v1/oauth"} {
			probes = append(probes, radiusURLProbe{Op: "resolveRaw", Gateway: gateway, Path: path})
		}
	}
	queries := []string{"?a=b", "a='b'&c=\"d\"", "a=<b>#c", "?", "??x", "response_type=code&client_id=pi-gateway&redirect_uri=http%3A%2F%2F127.0.0.1%3A1456%2Foauth%2Fcallback&state=s", "a=b", "", "x=%E2%9C%93&y=a+b&z=*-._~"}
	for range 2500 {
		probes = append(probes, radiusURLProbe{Op: "authorize", Endpoint: build(), Query: queries[random.Intn(len(queries))]})
	}
	// A file base keeps a drive-letter first segment (Node tests only its first two characters); a host-less path starting with "//" serializes after "/."; an opaque
	// path's space before "?" or "#" is "%20"; the search setter removes tabs and newlines after dropping one "?".
	for _, gateway := range []string{"file:///C:/foo/bar", "file:///c|/x", "file://h/C:", "file:///C:ab/c", "file:///C/x", "file:///%43:/x", "file://localhost/C:/x", "foo:/.//p"} {
		probes = append(probes, radiusURLProbe{Op: "resolveRaw", Gateway: gateway, Path: "/v1/config"})
	}
	for _, endpoint := range []string{"foo:/.//p", "foo:/..//p#f", "foo:///.//p", "foo:a  #x", "foo:a  ?q", "foo: ?q", "https://h/p"} {
		for _, query := range []string{"", "a\tb", "\n?a\r", "?\t", "\t", "a=b"} {
			probes = append(probes, radiusURLProbe{Op: "authorize", Endpoint: endpoint, Query: query})
		}
	}
	return probes
}

// The URL expressions of radius.ts and radius-config.ts run in Node against the same probes: new URL(path, normalizeRadiusGatewayUrl(gateway)).toString() for the four gateway
// paths, and new URL(endpoint) with its search set and serialized for the authorization URL. Pig's resolveGatewayPath and radiusAuthorizeURL must give Pi's string, and fail exactly
// when the URL constructor throws.
func TestRadiusURLsMatchPiOnSeededProbes(t *testing.T) {
	probes := radiusURLProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "providers", "radius-config.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/radius_urls.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []*string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	succeeded, differing := 0, 0
	for i, probe := range probes {
		var got *string
		var err error
		var text string
		switch probe.Op {
		case "resolve":
			text, err = resolveGatewayPath(NormalizeRadiusGatewayURL(probe.Gateway), probe.Path)
		case "resolveRaw":
			text, err = resolveGatewayPath(probe.Gateway, probe.Path)
		default:
			text, err = radiusAuthorizeURL(probe.Endpoint, probe.Query)
		}
		if err == nil {
			got = &text
			succeeded++
		} else if err.Error() != "Invalid URL" {
			t.Errorf("%+v: error %q, want the URL constructor's %q", probe, err, "Invalid URL")
		}
		if (got == nil) != (expected[i] == nil) || (got != nil && *got != *expected[i]) {
			if differing++; differing <= 12 {
				want := "<throws>"
				if expected[i] != nil {
					want = *expected[i]
				}
				have := "<error>"
				if got != nil {
					have = *got
				}
				t.Errorf("%+v\n  Pig %q\n  Pi  %q", probe, have, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if succeeded < 1000 || succeeded > len(probes)-500 {
		t.Errorf("probes are one-sided: %d succeed", succeeded)
	}
}
