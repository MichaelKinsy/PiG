package ai

// pi: packages/ai/src/api/azure-openai-config.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// azureBaseURLProbes combine schemes, hosts (Azure and other, mixed case, trailing dots, IDN), userinfo, ports, paths that trigger and miss the /openai/v1 rewrite
// (empty, /, /openai, /openai/v1/responses, with trailing slashes and different case), queries and fragments, whitespace Go and JavaScript trim differently, and unparseable text.
func azureBaseURLProbes() []string {
	random := rand.New(rand.NewSource(20260942))
	pad := []string{"", "", " ", "\t", "\ufeff", "\u0085", "\u00a0", "\n"}
	schemes := []string{"https://", "https://", "http://", "HTTPS://", "ftp://", "ws://", "file:///", "foo://", "mailto:", "https:/", "https:\\\\", "", "//", "data:,"}
	userinfos := []string{"", "", "", "u:p@", "ü@", "a@b@"}
	hosts := []string{"res.openai.azure.com", "RES.OpenAI.Azure.com", "res.cognitiveservices.azure.com", "res.ai.azure.com", "res.openai.azure.com.", "bücher.openai.azure.com", "xn--bcher-kva.ai.azure.com",
		"openai.azure.com", ".openai.azure.com", "example.com", "localhost:8080", "res.openai.azure.com:443", "res.openai.azure.com:8443", "res.openai.azure.com:99999", "[::1]", "127.0.0.1", "0x7f.1", "a b.example", "", "res.openai.azure.com\t", "ＲＥＳ.openai.azure.com"}
	paths := []string{"", "/", "//", "///", "/openai", "/openai/", "/OpenAI", "/openai/v1", "/openai/v1/", "/openai/v1/responses", "/openai/v1/responses/", "/openai/v1/responses/x", "/v1", "/a/../openai", "/openai/./v1/responses",
		"/openai?x=1", "?api-version=v1", "/openai/v1/responses?a=b#c", "#frag", "/ü", "/a b", "/openai%2Fv1"}
	var probes []string
	for range 4000 {
		probes = append(probes, pad[random.Intn(len(pad))]+schemes[random.Intn(len(schemes))]+userinfos[random.Intn(len(userinfos))]+hosts[random.Intn(len(hosts))]+paths[random.Intn(len(paths))]+pad[random.Intn(len(pad))])
	}
	// A host-less path that starts with "//" serializes after "/."; an opaque path keeps a space before "?" or "#" as "%20".
	return append(probes, "foo:/.//p", "foo:/..//p?q#f", "foo:///.//p", "foo:a  ?q", "foo: #f", "mailto:a b ?c")
}

// normalizeAzureBaseUrl (cut from Pi's source) runs in Node on the same base URLs: the WHATWG parse (which inputs throw), the Azure host test on the lowercased hostname, the rewrite
// of an empty, /openai or /openai/v1/responses path to /openai/v1 with the query cleared, and the serialization without trailing slashes must match Pig's normalizeAzureBaseURL.
func TestAzureBaseURLNormalizationMatchesPi(t *testing.T) {
	probes := azureBaseURLProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api", "azure-openai-config.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/azure_config.mjs", source)
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
	succeeded, rewritten, differing := 0, 0, 0
	for i, probe := range probes {
		text, err := normalizeAzureBaseURL(probe)
		if expected[i] != nil {
			succeeded++
			if len(*expected[i]) > 10 && (*expected[i])[len(*expected[i])-10:] == "/openai/v1" {
				rewritten++
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
				t.Errorf("%q\n  Pig %q\n  Pi  %q", probe, have, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if succeeded < 1500 || succeeded > len(probes)-500 || rewritten < 100 {
		t.Errorf("probes are one-sided: %d succeed, %d rewritten", succeeded, rewritten)
	}
}
