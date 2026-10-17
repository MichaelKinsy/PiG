package ai

// pi: packages/ai/src/auth/oauth/openai-chatgpt.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type chatgptResponseProbe struct {
	Flow       string `json:"flow"`
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Raw        []byte `json:"raw"`
}

// chatgptResponseProbes serve the token endpoint every shape of body and status: members absent, empty, blank, null and of each JSON type, scopes with and without the direct-token scope, expiries past the double range, bodies that are not objects, malformed, with a byte order mark or ill-formed UTF-8.
func chatgptResponseProbes() []chatgptResponseProbe {
	random := rand.New(rand.NewSource(20261020))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	member := func(key string, values ...string) string {
		if random.Intn(6) == 0 {
			return ""
		}
		return fmt.Sprintf(`%q:%s`, key, pick(values...))
	}
	join := func(parts ...string) string {
		var kept []string
		for _, part := range parts {
			if part != "" {
				kept = append(kept, part)
			}
		}
		return "{" + strings.Join(kept, ",") + "}"
	}
	var probes []chatgptResponseProbe
	add := func(flow string, status int, body string) {
		probes = append(probes, chatgptResponseProbe{Flow: flow, Status: status, StatusText: http.StatusText(status), Raw: []byte(body)})
	}
	good := `{"access_token":"a","refresh_token":"r","expires_in":3600,"id_token":"i","scope":"openid chatgpt.tokens.use.direct"}`
	fixed := []string{``, `{`, `not json`, `[]`, `[1]`, `5`, `"text"`, `null`, `true`, "\ufeff" + good, " " + good + " \n", good,
		`{"error":"invalid_grant"}`, `{"access_token":"a","access_token":"b","refresh_token":"r","expires_in":1,"id_token":"i","scope":"chatgpt.tokens.use.direct"}`,
		`{"access_token":"a","refresh_token":"r","expires_in":1e999,"id_token":"i","scope":"chatgpt.tokens.use.direct"}`, `{"access_token":"a","refresh_token":"r","expires_in":"60","id_token":"i","scope":"chatgpt.tokens.use.direct"}`,
		`{"access_token":"a","refresh_token":"r","expires_in":0,"id_token":"i","scope":"chatgpt.tokens.use.direct"}`, `{"access_token":"a","refresh_token":"r","expires_in":1e-999,"id_token":"i","scope":"chatgpt.tokens.use.direct"}`,
		`{"access_token":"a","refresh_token":"r","expires_in":1.5,"id_token":"i","scope":"\u00a0chatgpt.tokens.use.direct\u2003x\n\ty "}`, `{"access_token":" ","refresh_token":"r","expires_in":1,"id_token":"i","scope":"chatgpt.tokens.use.direct"}`,
		`{"access_token":"a","refresh_token":"r","expires_in":1,"id_token":"i","scope":"chatgpt.tokens.use.directx"}`, `{"access_token":"a","refresh_token":"r","expires_in":1,"id_token":" ","scope":"chatgpt.tokens.use.direct"}`,
		`{"access_token":"a","refresh_token":"r","expires_in":1,"scope":"chatgpt.tokens.use.direct"}`,
		// Ill-formed UTF-8: fetch decodes one U+FFFD per maximal ill-formed subpart.
		"\xff\xfe", "x\xe2\x82y", "{\"access_token\":\"a\xff\",\"refresh_token\":\"r\xe2\x82\",\"expires_in\":1,\"id_token\":\"i\",\"scope\":\"chatgpt.tokens.use.direct \xf0\x9f\x98\"}"}
	for _, body := range fixed {
		for _, flow := range []string{"exchange", "refresh"} {
			for _, status := range []int{200, 201, 299, 300, 400, 401, 403, 404, 429, 500} {
				add(flow, status, body)
			}
		}
	}
	// The failure detail falls back to response.statusText, the reason phrase the server sent: empty, or not the canonical one.
	for _, statusText := range []string{"", "Custom Reason", "OK"} {
		for _, flow := range []string{"exchange", "refresh"} {
			for _, body := range []string{``, `x`} {
				probes = append(probes, chatgptResponseProbe{Flow: flow, Status: 500, StatusText: statusText, Raw: []byte(body)}, chatgptResponseProbe{Flow: flow, Status: 403, StatusText: statusText, Raw: []byte(body)})
			}
		}
	}
	str := func() string {
		return pick(`"abc"`, `"abc"`, `"abc"`, `""`, `"  "`, `"\t\u00a0"`, `null`, `0`, `false`, `[]`, `"é😀<>&"`)
	}
	scope := func() string {
		return pick(`"chatgpt.tokens.use.direct"`, `"chatgpt.tokens.use.direct"`, `"openid chatgpt.tokens.use.direct"`, `" chatgpt.tokens.use.direct "`, `"openid"`, `""`, `"CHATGPT.TOKENS.USE.DIRECT"`, `"a\u00a0chatgpt.tokens.use.direct"`, `"a\u200bchatgpt.tokens.use.direct"`, `null`, `5`, `["chatgpt.tokens.use.direct"]`)
	}
	number := func() string {
		return pick(`60`, `3600`, `0`, `-1`, `1.5`, `1e999`, `-1e999`, `1e-999`, `1e21`, `"60"`, `null`, `true`)
	}
	// Half the probes are mostly valid, so the later checks (expires_in, scope, id_token) are reached.
	valid := func(value func() string, good string) func() string {
		return func() string {
			if random.Intn(2) == 0 && random.Intn(10) != 0 {
				return good
			}
			return value()
		}
	}
	for range 3000 {
		body := join(member("access_token", valid(str, `"a"`)()), member("refresh_token", valid(str, `"r"`)()), member("expires_in", valid(number, `3600`)()), member("id_token", valid(str, `"i"`)()), member("scope", valid(scope, `"chatgpt.tokens.use.direct"`)()), member("extra", `1`))
		add(pick("exchange", "refresh"), []int{200, 200, 200, 200, 400, 401, 500}[random.Intn(7)], body)
	}
	return probes
}

func TestChatGPTResponsesMatchPi(t *testing.T) {
	probes := chatgptResponseProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/chatgpt_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "openai-chatgpt.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		OK     map[string]any `json:"ok"`
		Err    *string        `json:"err"`
		Syntax bool           `json:"syntax"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	differing, succeeded, failed := 0, 0, 0
	for i, probe := range probes {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: probe.Status, Status: fmt.Sprintf("%d %s", probe.Status, probe.StatusText), Body: io.NopCloser(bytes.NewReader(probe.Raw)), Header: http.Header{}}, nil
		})
		started := float64(nowMillis())
		var credentials OAuthCredentials
		if probe.Flow == "exchange" {
			credentials, err = exchangeChatGPTAuthorizationCode(t.Context(), "code", "verifier", "cid")
		} else {
			credentials, err = RefreshOpenAIChatGPTToken(t.Context(), OAuthCredentials{Refresh: "old", Extra: map[string]json.RawMessage{"clientId": json.RawMessage(`"cid"`)}})
		}
		want := expected[i]
		same := (err == nil) == (want.Err == nil)
		if same && err != nil {
			same = errors.Is(err, errOAuthResponseNotJSON) == want.Syntax && err.Error() == *want.Err
		}
		if same && err == nil {
			var scopes []any
			_ = json.Unmarshal(credentials.Extra["scopes"], &scopes)
			var clientID string
			_ = json.Unmarshal(credentials.Extra["clientId"], &clientID)
			got := map[string]any{"type": "oauth", "access": credentials.Access, "refresh": credentials.Refresh, "ttlMs": math.Round((credentials.ExpiresMillis()-started)/100) * 100, "clientId": clientID, "scopes": scopes}
			expires, _ := want.OK["expires"].(float64)
			wantMap := map[string]any{"type": want.OK["type"], "access": want.OK["access"], "refresh": want.OK["refresh"], "ttlMs": math.Round((expires-1_700_000_000_000)/100) * 100, "clientId": want.OK["clientId"], "scopes": want.OK["scopes"]}
			same = reflect.DeepEqual(got, wantMap)
		}
		if want.Err == nil {
			succeeded++
		} else {
			failed++
		}
		if !same {
			differing++
			if differing <= 8 {
				t.Errorf("%s status %d body %.160q\n  Pi  %v %v\n  Pig %v %v", probe.Flow, probe.Status, probe.Raw, want.OK, ptrText(want.Err), credentials, err)
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes: %d succeed, %d fail", len(probes), succeeded, failed)
	if succeeded < 150 || failed < 1000 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}

func ptrText(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// openai-chatgpt.ts reads an error body with response.text().catch(() => ""), falling back to the status line's reason phrase (statusText), and a success body with response.json(), which rejects with undici's TypeError "terminated" when the connection ends early: a truncated body is never parsed, even when the bytes that arrived are complete JSON.
func TestChatGPTTruncatedBodyReadsAsNoBody(t *testing.T) {
	complete := `{"access_token":"a","refresh_token":"r","expires_in":3600,"id_token":"i","scope":"chatgpt.tokens.use.direct","error":"boom"}`
	for _, tc := range []struct {
		status int
		want   string
	}{{500, "OpenAI OAuth token request failed (500): x"}} {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Status: fmt.Sprintf("%d x", tc.status), Body: &erroringBody{data: complete}, Header: http.Header{}}, nil
		})
		_, err := exchangeChatGPTAuthorizationCode(t.Context(), "code", "verifier", "cid")
		if got := errorText(err); got != tc.want {
			t.Errorf("status %d: %q, want %q", tc.status, got, tc.want)
		}
	}
	withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: &erroringBody{data: complete}, Header: http.Header{}}, nil
	})
	if _, err := exchangeChatGPTAuthorizationCode(t.Context(), "code", "verifier", "cid"); errorText(err) != "terminated" {
		t.Errorf("a truncated success body: %v, want terminated", err)
	}
}
