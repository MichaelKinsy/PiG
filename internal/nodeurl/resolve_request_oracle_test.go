package nodeurl

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"testing"
)

// requestTargetProbes build the request targets a callback server can receive: origin-form paths with dot segments, encoded dots and slashes, backslashes, repeated slashes and queries of every shape;
// absolute-form targets with other schemes and hosts; "*", empty and authority-looking text.
func requestTargetProbes() [][2]string {
	random := rand.New(rand.NewSource(20260948))
	prefixes := []string{"", "", "", "", ":", "\\", "//", "///", "\\\\", "/\\", "http://", "https://", "HTTP://", "ws://", "ftp://", "file:///", "foo://", "http:", "http:/", "mailto:", "localhost:1455", "*", "//evil.example", "//u:p@evil.example:99999", "//localhost:1455", "http://localhost:1455", "http://[::1]:80", "http://0x7f.1"}
	paths := []string{"/auth/callback", "/auth/callback", "/auth/callback/", "/auth/./callback", "/auth/../auth/callback", "/auth/%2e%2e/auth/callback", "/auth/%2E/callback", "/auth%2Fcallback", "/auth\\callback", "/AUTH/callback", "//auth/callback", "/auth/callback%00", "/ü", "/a b", "/%zz", "/a;b", "/a|b", "", "/", "?code=x", "#frag", "/auth/callback#x?y=1", "/x?y=1#z", "/auth/callback\t", "/au\nth/callback", "/ａ/b"}
	queries := []string{"", "", "?code=abc&state=S", "?state=S&code=a&code=b", "?code=a+b&state=%53", "?code=%E2%9C%93", "?code=%zz&state=", "?&&code&=x&", "?error=access_denied&error_description=No+way", "?error_description=only", "?code=a;b", "?a[]=1&a[]=2", "?%63ode=abc", "?code=a%26b&state=S", "?c=\u00e9&d=%C3%A9", "?code=%ED%A0%80", "?code=a=b=c", "?=x&=y", "?a=1&a=2&b=3&a=4", "?q='\"<>`", "?code=abc#x=1&state=S"}
	bases := []string{"http://localhost", "http://localhost", "http://localhost:1455/auth/callback?x=1#f", "http://u:p@h:8080/a/b/c?q=1&r#frag", "https://h/a/b/", "ws://h/a?x", "ftp://h:21/pub/file?x", "http://h"}
	var probes [][2]string
	for range 5000 {
		probes = append(probes, [2]string{prefixes[random.Intn(len(prefixes))] + paths[random.Intn(len(paths))] + queries[random.Intn(len(queries))], bases[random.Intn(len(bases))]})
	}
	return probes
}

// `new URL(request.url ?? "/", "http://localhost")` runs in Node on the same targets: the origin, the pathname (dot segments, encoded dots, backslashes, authority-looking prefixes) and the search parameters
// the callback server reads must match ResolveURL and SearchParams, including where the constructor throws.
func TestResolveURLRequestTargetsMatchNode(t *testing.T) {
	probes := requestTargetProbes()
	var request []map[string]string
	for _, probe := range probes {
		request = append(request, map[string]string{"input": probe[0], "base": probe[1]})
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/resolve_request.mjs")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var expected []*struct {
		Origin   string
		Pathname string
		Params   [][2]json.RawMessage
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Node answered %d of %d probes", len(expected), len(probes))
	}
	resolved, differing := 0, 0
	for i, probe := range probes {
		got, err := ResolveURL(probe[0], probe[1])
		want := expected[i]
		report := func(format string, args ...any) {
			if differing++; differing <= 12 {
				t.Errorf("%q: "+format, append([]any{probe[0]}, args...)...)
			}
		}
		switch {
		case want == nil && err == nil:
			report("Pig resolves to %q %q, Node throws", got.Origin(), got.Pathname)
		case want != nil && err != nil:
			report("Pig fails with %v, Node gives %s %s", err, want.Origin, want.Pathname)
		case want != nil:
			resolved++
			values := SearchParams(got.Query)
			wantValues := map[string][]string{}
			for _, pair := range want.Params {
				var key string
				var vals []string
				_ = json.Unmarshal(pair[0], &key)
				_ = json.Unmarshal(pair[1], &vals)
				wantValues[key] = vals
			}
			if got.Origin() != want.Origin || got.Pathname != want.Pathname || !reflect.DeepEqual(map[string][]string(values), wantValues) {
				report("Pig %q %q %v, Node %q %q %v", got.Origin(), got.Pathname, values, want.Origin, want.Pathname, wantValues)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Node", differing, len(probes))
	}
	if resolved < 2000 || resolved > len(probes)-500 {
		t.Errorf("probes are one-sided: %d resolve", resolved)
	}
}
