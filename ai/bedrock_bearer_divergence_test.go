//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The two places Bedrock's bearer token and PiG differ from Pi, D195 and D196. Each case runs Pi's bedrock-converse-stream (pinned Pi, through the Node oracle) and
// PiG's provider against a loopback server and records what reached it: the Authorization header, or the error the stream ended with when no request was sent.
// The divergence cases pin both outcomes, so the record is wrong the day either side changes; the controls (an unset token, a token without skip-auth) must agree.
//
// pi: packages/ai/src/api/bedrock-converse-stream.ts:185-191 (getProviderEnvValue treats an empty variable as unset, so skipAuth stays on and config.token is not set);
// the AWS JavaScript SDK then selects bearer auth for any AWS_BEARER_TOKEN_BEDROCK that is present: @aws-sdk/core NODE_AUTH_SCHEME_PREFERENCE_OPTIONS
// (`bearerTokenKey in env`) and @aws-sdk/token-providers fromEnvSigningName (`bearerTokenKey in process.env`).
func TestBedrockBearerTokenDivergencesFromPi(t *testing.T) {
	type outcome struct {
		// Authorization is "Bearer <token>" or "SigV4 <access key id>" of the request the server received; empty when none arrived.
		Authorization string `json:"authorization"`
		// Error is the message the stream ended with ("Unknown: stop" for the server's 400 reply).
		Error string `json:"error"`
	}
	cases := []struct {
		name string
		env  map[string]string
		// Pi and PiG are what the case must produce; ErrorContains matches a request that was never sent.
		pi, pig          outcome
		piErrorContains  string
		pigErrorContains string
		same             bool
	}{
		{name: "D195 empty token with skip-auth", env: map[string]string{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_BEARER_TOKEN_BEDROCK": ""},
			pi: outcome{}, piErrorContains: "could not be signed with `token`", pig: outcome{Authorization: "SigV4 dummy-access-key", Error: "Unknown: stop"}},
		{name: "D195 empty token with static keys", env: map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret", "AWS_BEARER_TOKEN_BEDROCK": ""},
			pi: outcome{}, piErrorContains: "could not be signed with `token`", pig: outcome{Authorization: "SigV4 AKIAEXAMPLE", Error: "Unknown: stop"}},
		{name: "D196 skip-auth with a token over http", env: map[string]string{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_BEARER_TOKEN_BEDROCK": "tok"},
			pi: outcome{Authorization: "Bearer tok", Error: "Unknown: stop"}, pig: outcome{}, pigErrorContains: "requires HTTPS"},
		{name: "control unset token with static keys", env: map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"}, same: true,
			pi: outcome{Authorization: "SigV4 AKIAEXAMPLE", Error: "Unknown: stop"}},
		{name: "control token without skip-auth over http", env: map[string]string{"AWS_BEARER_TOKEN_BEDROCK": "tok"}, same: true,
			pi: outcome{Authorization: "Bearer tok", Error: "Unknown: stop"}},
	}
	const script = `(async () => {
  const http = require('node:http');
  const [root, modelId] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/bedrock-converse-stream.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  process.env.AWS_BEDROCK_FORCE_HTTP1 = '1';
  let authorization = '';
  const server = http.createServer((req, res) => { req.resume(); req.on('end', () => { authorization = String(req.headers.authorization); res.statusCode = 400; res.setHeader('content-type', 'application/json'); res.end('{"message":"stop"}'); }); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const model = { id: modelId, name: 'Claude', provider: 'amazon-bedrock', api: 'bedrock-converse-stream', baseUrl: 'http://127.0.0.1:' + server.address().port, reasoning: false,
    input: ['text'], maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
  const messages = [{ role: 'user', content: 'hi', timestamp: 1 }];
  const result = await lazy.bedrockConverseStreamApi().streamSimple(model, transcript.normalizeContext({ messages }), { maxTokens: 100 }).result();
  server.close();
  console.log(JSON.stringify({ authorization, error: result.errorMessage ?? '' }));
})()`
	const modelID = "anthropic.claude-sonnet-4-5-20250929-v1:0"
	sigV4 := regexp.MustCompile(`^AWS4-HMAC-SHA256 Credential=([^/]+)/`)
	kind := func(header string) string { // Bearer headers pass through; a SigV4 one reduces to its access key id, as the signature and date vary.
		if m := sigV4.FindStringSubmatch(header); m != nil {
			return "SigV4 " + m[1]
		}
		return header
	}
	scrub := t.TempDir()
	hermetic := map[string]string{
		"AWS_REGION": "us-east-1", "AWS_EC2_METADATA_DISABLED": "true",
		"AWS_CONFIG_FILE": filepath.Join(scrub, "config"), "AWS_SHARED_CREDENTIALS_FILE": filepath.Join(scrub, "credentials"),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range hermetic {
				env[k] = v
			}
			for k, v := range tc.env {
				env[k] = v
			}

			cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), modelID)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "AWS_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			cmd.Stderr = new(bytes.Buffer)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, cmd.Stderr)
			}
			var pi outcome
			if err := json.Unmarshal(out, &pi); err != nil {
				t.Fatalf("oracle output %q: %v", out, err)
			}
			if pi.Authorization == "undefined" {
				pi.Authorization = ""
			}
			pi.Authorization = kind(pi.Authorization)
			if tc.piErrorContains != "" {
				if pi.Authorization != "" || !strings.Contains(pi.Error, tc.piErrorContains) {
					t.Fatalf("Pi: %+v, want no request and an error containing %q", pi, tc.piErrorContains)
				}
				pi.Error = ""
			} else if pi != tc.pi {
				t.Fatalf("Pi: %+v, want %+v", pi, tc.pi)
			}

			for _, name := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "AWS_BEDROCK_SKIP_AUTH"} {
				t.Setenv(name, "") // registers the restore; the variable must then be unset, not empty
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			}
			for k, v := range env {
				t.Setenv(k, v)
			}
			var mu sync.Mutex
			var authorization string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				authorization = kind(r.Header.Get("Authorization"))
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"stop"}`))
			}))
			defer server.Close()
			model := &Model{ID: modelID, DisplayName: "Claude", Input: []string{"text"}, Capabilities: ModelCapabilities{MaxOutputTokens: 1000, ContextWindow: 10000},
				ProviderMeta: ProviderMetadata{ProviderID: "amazon-bedrock", API: APIBedrockConverseStream, BaseURL: server.URL}}
			stream, err := BedrockConverseStreamAPI().StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}), StreamOptions{MaxTokens: 100})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(t.Context()) {
			}
			mu.Lock()
			got := outcome{Authorization: authorization, Error: stream.Result().ErrorMessage}
			mu.Unlock()
			if tc.pigErrorContains != "" {
				if got.Authorization != "" || !strings.Contains(got.Error, tc.pigErrorContains) {
					t.Fatalf("PiG: %+v, want no request and an error containing %q", got, tc.pigErrorContains)
				}
				got.Error = ""
			} else {
				want := tc.pig
				if tc.same {
					want = tc.pi
				}
				if got != want {
					t.Fatalf("PiG: %+v, want %+v", got, want)
				}
			}
			if tc.same && (got.Authorization != pi.Authorization) {
				t.Fatalf("PiG sent %q, Pi sent %q: not a divergence", got.Authorization, pi.Authorization)
			}
			if !tc.same && got.Authorization == pi.Authorization {
				t.Fatalf("PiG and Pi both produced %q: the divergence is gone, remove its record", got.Authorization)
			}
		})
	}
}
