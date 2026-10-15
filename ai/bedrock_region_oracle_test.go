//go:build !pig_strip_bedrock_converse_stream

package ai

// pi: packages/ai/src/api/bedrock-converse-stream.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// bedrockRegionProbes build base URLs around the standard Bedrock runtime host: regions and partitions, fips, mixed case, trailing dots, userinfo, ports, IDN and full-width letters
// that the WHATWG host parser folds, schemes, paths, and text that is not a URL.
func bedrockRegionProbes() []string {
	random := rand.New(rand.NewSource(20260943))
	schemes := []string{"https://", "https://", "http://", "HTTPS://", "ftp://", "foo://", "file:///", "https:/", "https:\\\\", "", "//", "mailto:"}
	userinfos := []string{"", "", "", "u:p@", "ü@", "a@b@"}
	hosts := []string{"bedrock-runtime.us-east-1.amazonaws.com", "bedrock-runtime-fips.us-gov-west-1.amazonaws.com", "bedrock-runtime.cn-north-1.amazonaws.com.cn", "BEDROCK-RUNTIME.EU-WEST-1.AMAZONAWS.COM",
		"bedrock-runtime.us-east-1.amazonaws.com.", "ｂｅｄｒｏｃｋ-runtime.us-east-1.amazonaws.com", "bedrock-runtime.US_east.amazonaws.com", "bedrock-runtime..amazonaws.com", "bedrock-runtime.a.b.amazonaws.com",
		"bedrock-runtime.us-east-1.amazonaws.com.cn", "bedrock-runtime.us-east-1.amazonaws.org", "bedrock.us-east-1.amazonaws.com", "xbedrock-runtime.us-east-1.amazonaws.com", "bedrock-runtime-fips-x.us-east-1.amazonaws.com",
		"bedrock-runtime.us-east-1.amazonaws.com:443", "bedrock-runtime.us-east-1.amazonaws.com:99999", "bedrock-runtime.us-east-1.amazonaws.com\t", "bedrock-runtime.ü.amazonaws.com", "bedrock-runtime.us east.amazonaws.com",
		"localhost", "127.0.0.1", "[::1]", "0x7f.1", "proxy.example", "bedrock-runtime.%75s-east-1.amazonaws.com", "bedrock-runtime.us-east-1.amazonaws.com%2e", "bedrock-runtime.us-east-1.amazonaws.com\n", " bedrock-runtime.us-east-1.amazonaws.com"}
	paths := []string{"", "/", "/v1", "?x=1", "#f", "/a b", "\\x", ":", "/%"}
	probes := []string{"", " ", "https://", "not a url", "bedrock-runtime.us-east-1.amazonaws.com", "bedrock-runtime.us-east-1.amazonaws.com:443"}
	for range 3000 {
		probes = append(probes, schemes[random.Intn(len(schemes))]+userinfos[random.Intn(len(userinfos))]+hosts[random.Intn(len(hosts))]+paths[random.Intn(len(paths))])
	}
	return probes
}

// getStandardBedrockEndpointRegion (cut from Pi's source) runs in Node on the same base URLs: the WHATWG hostname (lowercased, IDN-folded, port and userinfo removed) must match Pig's
// bedrockEndpointRegion, which gives "" where Pi gives undefined.
func TestBedrockEndpointRegionMatchesPi(t *testing.T) {
	probes := bedrockRegionProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api", "bedrock-converse-stream.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/bedrock_region.mjs", source)
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
	matched, differing := 0, 0
	for i, probe := range probes {
		want := ""
		if expected[i] != nil {
			want = *expected[i]
			matched++
		}
		if got := bedrockEndpointRegion(probe); got != want {
			if differing++; differing <= 12 {
				t.Errorf("%q\n  Pig %q\n  Pi  %q", probe, got, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if matched < 300 || matched > len(probes)-500 {
		t.Errorf("probes are one-sided: %d match", matched)
	}
}
