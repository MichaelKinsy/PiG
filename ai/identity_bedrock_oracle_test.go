//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// bedrockApplicationAgent is the application part of an AWS SDK user agent: the tokens after the SDK's api/ pair, without its m/ metadata token.
var (
	bedrockApplicationAgent = regexp.MustCompile(`api/\S+ (.*)$`)
	bedrockSDKMetadata      = regexp.MustCompile(`(?:^| )m/\S+`)
)

// Pi's bedrockUserAgent hands the client identity to the AWS SDK as customUserAgent pairs. The JavaScript SDK writes them after its api/ pair;
// the Go SDK does the same, so the pair after api/ is compared. PiG's product name replaces Pi's (D65).
func TestBedrockUserAgentMatchesPi(t *testing.T) {
	identities := []map[string]any{
		nil,
		{"version": "9.9.9"},
		{"userAgent": "short", "version": "9.9.9"},
		{"userAgent": map[string]any{"custom": "acme"}},
		{"userAgent": map[string]any{"custom": "acme/1.0"}},
		{"userAgent": map[string]any{"custom": "acme (x; y)"}},
		{"userAgent": map[string]any{"custom": "a/b/c é😀"}},
		{"userAgent": map[string]any{"custom": ""}},
	}
	const script = `(async () => {
  const http = require('node:http');
  const [root, identitiesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/bedrock-converse-stream.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  process.env.AWS_BEDROCK_SKIP_AUTH = '1';
  process.env.AWS_REGION = 'us-east-1';
  process.env.AWS_BEDROCK_FORCE_HTTP1 = '1';
  let agent;
  const server = http.createServer((req, res) => { agent = req.headers['user-agent']; req.resume(); req.on('end', () => { res.statusCode = 400; res.setHeader('content-type', 'application/json'); res.setHeader('x-amzn-errortype', 'ValidationException'); res.end('{"message":"stop"}'); }); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const out = [];
  for (const identity of JSON.parse(identitiesJSON)) {
    const model = { id: 'us.anthropic.claude-opus-4-8', name: 'Claude', provider: 'amazon-bedrock', api: 'bedrock-converse-stream', baseUrl: 'http://127.0.0.1:' + server.address().port, reasoning: false,
      input: ['text'], maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    agent = undefined;
    await lazy.bedrockConverseStreamApi().streamSimple(model, transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 1 }] }), { maxTokens: 10, identity: identity ?? undefined }).result();
    out.push(agent ?? '');
  }
  server.close();
  console.log(JSON.stringify(out));
})()`
	encoded, err := json.Marshal(identities)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(encoded))
	cmd.Stderr = new(bytes.Buffer)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, cmd.Stderr)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for i, identity := range identities {
		var piIdentity *ClientIdentity
		if identity != nil {
			raw, _ := json.Marshal(identity)
			piIdentity = new(ClientIdentity)
			if err := json.Unmarshal(raw, piIdentity); err != nil {
				t.Fatal(err)
			}
		}
		server, requests := rejectingProviderServer(t)
		stream, err := NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL).Stream(t.Context(), providerWireTranscript(), StreamOptions{Identity: piIdentity, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
		if err != nil {
			t.Fatal(err)
		}
		stream.Result()
		got := bedrockApplicationAgent.FindStringSubmatch((<-requests).header.Get("User-Agent"))
		piAgent := bedrockApplicationAgent.FindStringSubmatch(want[i])
		if got == nil || piAgent == nil {
			t.Fatalf("identity %v: no application agent in PiG %q or Pi %q", identity, got, want[i])
		}
		got[1], piAgent[1] = strings.TrimSpace(bedrockSDKMetadata.ReplaceAllString(got[1], "")), strings.TrimSpace(bedrockSDKMetadata.ReplaceAllString(piAgent[1], ""))
		expected := piAgent[1]
		if _, custom := identity["userAgent"].(map[string]any); !custom {
			expected = strings.Replace(expected, "pi", pigidentity.UserAgentProduct, 1)
			if identity == nil {
				expected = pigidentity.UserAgentProduct + "/" + ProductVersion
			}
		}
		if got[1] != expected {
			t.Errorf("identity %v: PiG %q, Pi %q (full Pi agent %q)", identity, got[1], piAgent[1], want[i])
		}
	}
}
