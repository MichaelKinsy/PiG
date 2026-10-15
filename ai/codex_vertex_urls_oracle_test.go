//go:build !pig_strip_google_vertex

package ai

// pi: packages/ai/src/api/openai-codex-responses.ts
// pi: packages/ai/src/api/google-vertex.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type codexVertexProbe struct {
	Op      string `json:"op"`
	BaseURL string `json:"baseUrl"`
}

// codexVertexProbes build base URLs from schemes, hosts, ports, userinfo and paths: Codex proxies with and without /codex or /codex/responses and trailing slashes, whitespace that Go and JavaScript trim differently,
// and Vertex paths with API-version segments (v1, v1beta1, v1beta, v2alpha, V1, %-encoded digits, dot segments, query and fragment look-alikes).
func codexVertexProbes() []codexVertexProbe {
	random := rand.New(rand.NewSource(20260945))
	pad := []string{"", "", "", " ", "\t", "\ufeff", "\u0085", "\u00a0", "\n"}
	schemes := []string{"https://", "https://", "http://", "HTTPS://", "HTTP://", "wss://", "ws://", "ftp://", "file:///", "foo://", "https:/", "https:\\\\", "mailto:", "", "//"}
	userinfos := []string{"", "", "", "u:p@", "ü@"}
	hosts := []string{"chatgpt.com", "CHATGPT.com", "localhost:8080", "bücher.example", "[::1]:9000", "0x7f.1", "a b.example", "host:443", "host:80", "host:99999", "", "ＡＰＩ.example", "aiplatform.googleapis.com", "us-central1-aiplatform.googleapis.com"}
	paths := []string{"", "/", "//", "/backend-api", "/backend-api/", "/backend-api/codex", "/backend-api/codex/", "/backend-api/codex/responses", "/backend-api/codex/responses/", "/codex/responses/x", "/v1", "/v1/", "/v1beta1", "/v1beta", "/v2alpha", "/V1", "/v", "/v%31", "/a/v1/b", "/a/../v1", "/a/./b", "/%76%31", "/x/v1beta12/y", "/v1x", "/vv1", "/ü", "/a b", "/a?v1", "/a#v1", "?x=1", "#v1", "/v1?x=/v2"}
	var probes []codexVertexProbe
	for range 4500 {
		base := pad[random.Intn(len(pad))] + schemes[random.Intn(len(schemes))] + userinfos[random.Intn(len(userinfos))] + hosts[random.Intn(len(hosts))] + paths[random.Intn(len(paths))] + pad[random.Intn(len(pad))]
		op := "codexWs"
		if random.Intn(2) == 0 {
			op = "vertexVersion"
		}
		probes = append(probes, codexVertexProbe{Op: op, BaseURL: base})
	}
	for _, blank := range []string{"", " \t", "\ufeff", "\u0085", "\u00a0", " \ufeff ", "\u2028", "\u200b", "\u180e"} {
		probes = append(probes, codexVertexProbe{Op: "codexWs", BaseURL: blank}, codexVertexProbe{Op: "vertexVersion", BaseURL: blank})
	}
	return probes
}

// resolveCodexWebSocketUrl (with resolveCodexUrl) and baseUrlIncludesApiVersion, cut from Pi's sources, run in Node on the same base URLs: the Codex endpoint suffix rules on JS-trimmed text, the WHATWG parse,
// the https: to wss: and http: to ws: protocol setter, and the Vertex segment test on the parsed pathname must match Pig's resolveCodexURL with codexWebSocketHref and vertexBaseURLIncludesAPIVersion.
func TestCodexWebSocketAndVertexVersionURLsMatchPi(t *testing.T) {
	probes := codexVertexProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/codex_vertex_urls.mjs", filepath.Join(src, "openai-codex-responses.ts"), filepath.Join(src, "google-vertex.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	codexOK, versioned, differing := 0, 0, 0
	for i, probe := range probes {
		var got json.RawMessage
		if probe.Op == "vertexVersion" {
			got, _ = json.Marshal(vertexBaseURLIncludesAPIVersion(probe.BaseURL))
			if string(got) == "true" {
				versioned++
			}
		} else if href, err := codexWebSocketHref(resolveCodexURL(probe.BaseURL)); err == nil {
			got, _ = json.Marshal(href)
			codexOK++
		} else {
			got = json.RawMessage("null")
			if err.Error() != "Invalid URL" {
				t.Errorf("%+v: error %q, want the URL constructor's %q", probe, err, "Invalid URL")
			}
		}
		if string(got) != string(expected[i]) {
			if differing++; differing <= 12 {
				t.Errorf("%+v\n  Pig %s\n  Pi  %s", probe, got, expected[i])
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if codexOK < 800 || codexOK > len(probes)/2-300 || versioned < 300 {
		t.Errorf("probes are one-sided: %d Codex URLs resolve, %d Vertex paths carry a version", codexOK, versioned)
	}
}
