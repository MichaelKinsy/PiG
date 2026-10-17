package ai

// pi: packages/ai/src/auth/oauth/openrouter.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type openRouterProbe struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	// Raw is Body's exact bytes for the oracle, which builds its Response from them: a JSON string cannot carry ill-formed UTF-8.
	Raw []byte `json:"raw"`
}

type openRouterAnswer struct {
	OK  json.RawMessage `json:"ok"`
	Err *string         `json:"err"`
}

// openRouterProbes serve the key-exchange endpoint every shape of body and status: keys and error members absent, empty, null and of each JSON type, nested and non-object errors, bodies that are not objects, empty, malformed or begin with a byte order mark.
func openRouterProbes() []openRouterProbe {
	random := rand.New(rand.NewSource(20261014))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	member := func(key string, values ...string) string {
		if random.Intn(4) == 0 {
			return ""
		}
		return fmt.Sprintf(`%q:%s`, key, pick(values...))
	}
	var probes []openRouterProbe
	for _, body := range []string{``, `{`, `not json`, `[]`, `[{"key":"k"}]`, `5`, `"text"`, `null`, `true`, "\ufeff" + `{"key":"k"}`, "\ufeff" + `{"error":"bom"}`, "\ufeff" + `[`, " \n" + `{"key":"k"}` + "\n ", `{"key":"k"} {}`, `{"key":"k"}x`, `{"key":"a","key":"b"}`, `{"key":"é😀<>&\u2028"}`, `{"key":""}`, `{"key":null}`, `{"key":5}`, `{"error":{"message":"nested"}}`, `{"error":{"message":5}}`, `{"error":[]}`, `{"error":["x"]}`, `{"error":null,"message":"m"}`, `{"error_description":"","message":"m"}`, `{"error_description":"d","message":"m","error":"e"}`} {
		for _, status := range []int{200, 201, 400, 401, 403, 429, 500} {
			probes = append(probes, openRouterProbe{Status: status, Body: body})
		}
	}
	// Ill-formed UTF-8: fetch decodes one U+FFFD per maximal ill-formed subpart.
	for _, body := range []string{"\xff\xfe", "a\xe2\x82b", "{\"error\":\"x\xff\xfe \xe2\x82 \xf0\x9f\x98 \xed\xa0\x80 \xc0\x80\"}", "{\"key\":\"k\xe2\x82\"}", "{\"key\":\"\",\"message\":\"m\xf0\x9f\x98\"}"} {
		for _, status := range []int{200, 400, 500} {
			probes = append(probes, openRouterProbe{Status: status, Body: body})
		}
	}
	str := func() string {
		return pick(`"v"`, `""`, `null`, `5`, `true`, `[]`, `{}`, `{"message":"deep"}`, `{"message":""}`, `{"message":null}`, `["x"]`, `"é😀"`)
	}
	for range 600 {
		var parts []string
		for _, part := range []string{member("key", str()), member("error_description", str()), member("message", str()), member("error", str()), member("extra", str())} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		probes = append(probes, openRouterProbe{Status: []int{200, 200, 400, 401, 500}[random.Intn(5)], Body: "{" + strings.Join(parts, ",") + "}"})
	}
	for i := range probes {
		probes[i].Raw = []byte(probes[i].Body)
	}
	return probes
}

func TestOpenRouterExchangeMatchesPi(t *testing.T) {
	probes := openRouterProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/openrouter_exchange.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "openrouter.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []openRouterAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	differing, succeeded := 0, 0
	for i, probe := range probes {
		withOpenRouterToken(t, func(*http.Request) (*http.Response, error) { return cannedResp(probe.Status, probe.Body), nil })
		creds, err := exchangeOpenRouterCode(t.Context(), "code", "verifier")
		var got openRouterAnswer
		if err != nil {
			message := err.Error()
			got.Err = &message
		} else {
			got.OK, _ = json.Marshal(map[string]any{"type": "oauth", "access": creds.Access, "refresh": creds.Refresh, "expires": creds.ExpiresMillis()})
		}
		want := expected[i]
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = *got.Err == *want.Err
		}
		if same && got.Err == nil {
			var a, b any
			_ = json.Unmarshal(got.OK, &a)
			_ = json.Unmarshal(want.OK, &b)
			same = reflect.DeepEqual(a, b)
		}
		if want.Err == nil {
			succeeded++
		}
		if !same {
			differing++
			if differing <= 6 {
				render := func(a openRouterAnswer) string {
					if a.Err != nil {
						return "error: " + *a.Err
					}
					return string(a.OK)
				}
				t.Errorf("status %d body %.120q\n  Pi  %s\n  Pig %s", probe.Status, probe.Body, render(want), render(got))
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes, %d succeed", len(probes), succeeded)
	if succeeded < 30 || len(probes)-succeeded < 400 {
		t.Errorf("probes are one-sided: %d succeed of %d", succeeded, len(probes))
	}
}

// openrouter.ts reads the body with response.json(), which rejects when the connection ends early: a truncated body is never parsed, even when the bytes that arrived are complete JSON. It is fatal only for a successful status.
func TestOpenRouterTruncatedBodyIsInvalidJSON(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{200, "OpenRouter OAuth returned invalid JSON"},
		{500, "OpenRouter OAuth key exchange failed (HTTP 500)"},
	} {
		withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: &erroringBody{data: `{"key":"k","error":"boom"}`}, Header: http.Header{}}, nil
		})
		_, err := exchangeOpenRouterCode(t.Context(), "code", "verifier")
		if got := errorText(err); got != tc.want {
			t.Errorf("status %d: %q, want %q", tc.status, got, tc.want)
		}
	}
}
