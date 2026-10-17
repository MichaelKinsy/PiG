package ai

// pi: node_modules/@anthropic-ai/sdk/lib/credentials/types.mjs

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// anthropicTokenEndpointProbes cover the loopback exemption and the https rule: schemes in any case, loopback spellings (localhost in mixed case or with a trailing dot, 127.0.0.1 and its numeric and short forms, [::1] and its
// long forms, 0.0.0.0), ports, userinfo, tabs and newlines inside the URL, and text that does not parse.
func anthropicTokenEndpointProbes() []string {
	random := rand.New(rand.NewSource(20260947))
	schemes := []string{"https://", "https://", "http://", "http://", "HTTP://", "HtTpS://", "ws://", "wss://", "ftp://", "file:///", "foo://", "mailto:", "http:/", "http:\\\\", "", "//", "http:"}
	userinfos := []string{"", "", "", "u:p@", "ü@", "a@b@"}
	hosts := []string{"localhost", "LOCALHOST", "Localhost:8080", "localhost.", "127.0.0.1", "127.0.0.1:3000", "127.1", "0x7f.0.0.1", "2130706433", "127.0.0.2", "0.0.0.0", "[::1]", "[::1]:8080", "[0:0:0:0:0:0:0:1]", "[::2]", "[::ffff:127.0.0.1]",
		"api.anthropic.com", "example.com:99999", "example.com:", "a b.example", "ｌｏｃａｌｈｏｓｔ", "lo\tcalhost", "localhost\n", "xn--bcher-kva.example", ""}
	paths := []string{"", "/", "/v1", "?x=1", "#f", "/a b", "\\x"}
	probes := []string{"", "not a url", "localhost:8080", "127.0.0.1:8080/x"}
	for range 4000 {
		probes = append(probes, schemes[random.Intn(len(schemes))]+userinfos[random.Intn(len(userinfos))]+hosts[random.Intn(len(hosts))]+paths[random.Intn(len(paths))])
	}
	return probes
}

// requireSecureTokenEndpoint, cut from the vendored Anthropic SDK 0.129.0, runs in Node on the same base URLs: an empty value is accepted, a URL that does not parse gives 'Invalid token endpoint base URL "x": TypeError: Invalid URL',
// https is accepted, http is accepted for the loopback hosts localhost, 127.0.0.1 and ::1 after the WHATWG host parser has normalized the host, and everything else is refused.
func TestAnthropicTokenEndpointRuleMatchesSDK(t *testing.T) {
	probes := anthropicTokenEndpointProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), "test", "parity", "dependency-sources", "@anthropic-ai", "sdk", "0.129.0", "lib", "credentials", "types.mjs")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/anthropic_token_endpoint.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("SDK oracle: %v\n%s", err, &stderr)
	}
	var expected []*string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("SDK answered %d of %d probes", len(expected), len(probes))
	}
	accepted, loopbackHTTP, differing := 0, 0, 0
	for i, probe := range probes {
		err := requireSecureAnthropicTokenEndpoint(probe)
		if expected[i] == nil {
			accepted++
			if len(probe) > 5 && probe[:5] != "https" && probe[:5] != "HtTpS" {
				loopbackHTTP++
			}
		}
		if (err == nil) != (expected[i] == nil) || (err != nil && err.Error() != *expected[i]) {
			if differing++; differing <= 12 {
				want, have := "<accepted>", "<accepted>"
				if expected[i] != nil {
					want = *expected[i]
				}
				if err != nil {
					have = err.Error()
				}
				t.Errorf("%q\n  Pig %s\n  SDK %s", probe, have, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from the SDK", differing, len(probes))
	}
	if accepted < 800 || loopbackHTTP < 100 || accepted > len(probes)-800 {
		t.Errorf("probes are one-sided: %d accepted, %d loopback http", accepted, loopbackHTTP)
	}
}
