package ai

// pi: packages/ai/src/api/pi-messages.ts
// pi: packages/ai/src/api/cloudflare-workers-ai-system-one.ts
// pi: packages/ai/src/api/typesafe-system-one.ts
// pi: packages/ai/src/api/openai-decisions.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"testing"
)

type serviceURLProbe struct {
	Op      string `json:"op"`
	BaseURL string `json:"baseUrl"`
	Path    string `json:"path"`
	Debug   bool   `json:"debug"`
}

// serviceURLProbes are provider base URLs with trailing slashes, existing queries (with repeated, encoded, plus-signed and empty pairs, and a debug pair), fragments, userinfo, ports,
// mixed-case and IDN hosts, dot segments and characters the path and query encode sets differ on.
func serviceURLProbes() []serviceURLProbe {
	random := rand.New(rand.NewSource(20260944))
	schemes := []string{"https://", "https://", "http://", "HTTPS://", "ftp://", "ws://", "file:///", "foo://", "mailto:", "https:/", "https:\\\\", "", "//"}
	userinfos := []string{"", "", "", "u:p@", "ü@"}
	hosts := []string{"api.example.com", "API.Example.COM", "localhost:8080", "bücher.example", "127.0.0.1", "[::1]:9000", "0x7f.1", "a b.example", "api.example.com:99999", "api.example.com:443", "", "ＡＰＩ.example"}
	paths := []string{"", "/", "//", "/v1", "/v1/", "/v1///", "/a/b", "/a/../b", "/a/./b/", "/a b", "/ü", "/a%2Fb", "/api/v1.2", "\\v1", "/%", "/v1;x"}
	suffixes := []string{"", "", "", "?x=1", "?x=1&x=2", "?a=b c&d=%E2%9C%93", "?debug=0", "?debug=0&debug=2&z=1", "?a+b=c+d", "?&&a&=b&", "?a=%zz&b=%", "?q='x'\"<>`", "?x=1#frag", "#frag", "#f/", "?", "?a=b/", "?ü=ß", "?debug=1&a=b"}
	var probes []serviceURLProbe
	for range 4500 {
		base := schemes[random.Intn(len(schemes))] + userinfos[random.Intn(len(userinfos))] + hosts[random.Intn(len(hosts))] + paths[random.Intn(len(paths))] + suffixes[random.Intn(len(suffixes))]
		if random.Intn(3) == 0 {
			probes = append(probes, serviceURLProbe{Op: "service", BaseURL: base, Path: []string{"run", "systemone", "decisions"}[random.Intn(3)]})
		} else {
			probes = append(probes, serviceURLProbe{Op: "messages", BaseURL: base, Debug: random.Intn(2) == 0})
		}
	}
	// A host-less base whose path starts with "//" keeps "/." in the href, so the resolved path cannot read back as an authority; an opaque base's space before "?" is "%20".
	for _, base := range []string{"foo:/.//p", "foo:/.//p/q?x=1", "foo:/..//p#f", "foo:///.//p", "foo:a ?x=1", "foo:a  #f"} {
		probes = append(probes, serviceURLProbe{Op: "service", BaseURL: base, Path: "run"}, serviceURLProbe{Op: "messages", BaseURL: base}, serviceURLProbe{Op: "messages", BaseURL: base, Debug: true})
	}
	return probes
}

// The URL expressions of Pi's four providers run in Node on the same base URLs: new URL(path, `${base}/`) for System One and decisions, and new URL(`${base}/messages`) with
// searchParams.set("debug", "1") for pi-messages. Pig's resolveServiceURL and piMessagesEndpoint must give Pi's string and fail exactly when the constructor throws.
func TestProviderServiceURLsMatchPiOnSeededProbes(t *testing.T) {
	probes := serviceURLProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/service_urls.mjs")
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
	succeeded, withQuery, differing := 0, 0, 0
	for i, probe := range probes {
		var text string
		var err error
		if probe.Op == "service" {
			text, err = resolveServiceURL(probe.BaseURL, probe.Path)
		} else {
			text, err = piMessagesEndpoint(probe.BaseURL, probe.Debug)
		}
		if err != nil && err.Error() != "Invalid URL" {
			t.Errorf("%+v: error %q, want the URL constructor's %q", probe, err, "Invalid URL")
		}
		if expected[i] != nil {
			succeeded++
			if bytes.Contains([]byte(*expected[i]), []byte("?")) {
				withQuery++
			}
		}
		if (err == nil) != (expected[i] != nil) || (err == nil && text != *expected[i]) {
			if differing++; differing <= 12 {
				want := "<throws>"
				if expected[i] != nil {
					want = *expected[i]
				}
				have := text
				if err != nil {
					have = "<error>"
				}
				t.Errorf("%+v\n  Pig %q\n  Pi  %q", probe, have, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if succeeded < 1500 || succeeded > len(probes)-500 || withQuery < 500 {
		t.Errorf("probes are one-sided: %d succeed, %d with a query", succeeded, withQuery)
	}
}
