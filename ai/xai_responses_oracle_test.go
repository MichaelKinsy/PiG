package ai

// pi: packages/ai/src/auth/oauth/xai.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type xaiResponseProbe struct {
	Flow     string `json:"flow"`
	Status   int    `json:"status"`
	Body     string `json:"body"`
	Previous string `json:"previous,omitempty"`
	// Raw is Body's exact bytes for the oracle, which builds its Response from them: a JSON string cannot carry ill-formed UTF-8.
	Raw []byte `json:"raw"`
}

type xaiResponseAnswer struct {
	OK  json.RawMessage `json:"ok"`
	Err *string         `json:"err"`
}

// xaiResponseProbes serve the device-authorization and token endpoints bodies a server sends and the ones it should not: every field absent, null, empty, of another JSON type, out of range, repeated or non-finite, a body that is not an object, not JSON, empty, or starts with a byte order mark, over several statuses.
func xaiResponseProbes() []xaiResponseProbe {
	random := rand.New(rand.NewSource(20261012))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	any1 := func() string {
		return pick(`"v"`, `""`, `null`, `5`, `0`, `-1`, `1.5`, `true`, `false`, `[]`, `{}`, `["a"]`, `{"a":1}`, `1e999`, `-1e999`, `1e-999`, `"3600"`, `3600`, `7`, `0.5`)
	}
	member := func(key string, values ...string) string {
		if random.Intn(5) == 0 {
			return "" // absent
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
	url := func() string {
		return pick(`"https://auth.x.ai/device"`, `"https://auth.x.ai/device?user_code=ABCD"`, `"http://auth.x.ai/device"`, `"ftp://x.ai/"`, `"javascript:alert(1)"`, `"not a url"`, `""`, `null`, `5`, `"HTTPS://AUTH.X.AI:443/a b"`, `"https://auth.x.ai/#frag"`, `"https://user:pw@auth.x.ai/"`)
	}
	str := func() string { return pick(`"abc"`, `"abc"`, `"abc"`, `""`, `null`, `5`, `true`, `[]`, `{}`) }
	num := func() string {
		return pick(`600`, `600`, `1800`, `0`, `-5`, `1.5`, `null`, `"600"`, `true`, `1e999`, `1e-999`, `[]`)
	}
	statuses := []int{200, 200, 200, 400, 401, 429, 500, 201}
	nonObjects := []string{`[]`, `[1]`, `5`, `"text"`, `null`, `true`, ``, `{`, `not json`, `{} {}`, `{}x`, "\ufeff{}", "\ufeff" + `{"access_token":"a","refresh_token":"r"}`, ` {"access_token":"a","refresh_token":"r"} `, `{"access_token":"a","access_token":"b","refresh_token":"r"}`, `{"error":"slow_down","interval":1e999}`, `{"error":"slow_down","interval":"7"}`, `{"error":"slow_down","interval":-3}`, `{"error":"slow_down","interval":0}`, `{"error":"slow_down"}`, `{"error":"slow_down","interval":7.5}`, `{"error":"authorization_pending"}`, `{"error":"access_denied"}`, `{"error":"authorization_denied"}`, `{"error":"expired_token"}`, `{"error":"other","error_description":"why"}`, `{"error":"","error_description":"only desc"}`, `{"error":5,"error_description":["x"]}`, `{"error":"e","error_description":""}`, `{"error":null}`}
	var probes []xaiResponseProbe
	for _, body := range nonObjects {
		for _, flow := range []string{"device", "refresh", "poll"} {
			for _, status := range []int{200, 400} {
				probes = append(probes, xaiResponseProbe{Flow: flow, Status: status, Body: body, Previous: "old-refresh"})
			}
		}
	}
	for range 900 {
		status := statuses[random.Intn(len(statuses))]
		var body string
		flow := pick("device", "refresh", "poll", "poll")
		switch flow {
		case "device":
			body = join(member("device_code", str()), member("user_code", str()), member("verification_uri", url()), member("verification_uri_complete", url()), member("interval", num()), member("expires_in", num()), member("error", str()), member("error_description", str()))
		default:
			body = join(member("access_token", str()), member("refresh_token", str()), member("expires_in", num()), member("error", `"slow_down"`, `"authorization_pending"`, `"x"`, `""`, `5`), member("interval", num()), member("error_description", str()), member("extra", any1()))
		}
		probes = append(probes, xaiResponseProbe{Flow: flow, Status: status, Body: body, Previous: pick("old-refresh", "", "old-refresh")})
	}
	// Well-formed bodies, so every success path is compared too.
	for _, body := range []string{
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.x.ai/device","expires_in":600}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.x.ai/device","verification_uri_complete":"https://auth.x.ai/device?c=1","interval":5,"expires_in":600}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.x.ai/device","verification_uri_complete":"","interval":0,"expires_in":600.5}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.x.ai/device","expires_in":1e999}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.x.ai/device","interval":1e999,"expires_in":600}`,
	} {
		probes = append(probes, xaiResponseProbe{Flow: "device", Status: 200, Body: body})
	}
	for _, body := range []string{
		`{"access_token":"a","refresh_token":"r","expires_in":7200}`, `{"access_token":"a","refresh_token":"r"}`, `{"access_token":"a","expires_in":10}`, `{"access_token":"a","refresh_token":"r","expires_in":0.5}`,
	} {
		for _, flow := range []string{"refresh", "poll"} {
			probes = append(probes, xaiResponseProbe{Flow: flow, Status: 200, Body: body, Previous: "old-refresh"}, xaiResponseProbe{Flow: flow, Status: 200, Body: body})
		}
	}
	// Ill-formed UTF-8: fetch decodes one U+FFFD per maximal ill-formed subpart (a truncated sequence is one, two stray bytes are two, an
	// encoded surrogate or overlong form is one per byte), in the error text, the JSON text and the member values alike.
	for _, body := range []string{
		"\xff\xfe", "a\xe2\x82b",
		"{\"error\":\"invalid_grant\",\"error_description\":\"\xff\xfe a\xe2\x82b \xf0\x9f\x98 \xed\xa0\x80 \xc0\x80\"}",
		"{\"error\":\"other\",\"error_description\":\"\xe2\x82\"}",
		"{\"access_token\":\"a\xff\",\"refresh_token\":\"r\xe2\x82\",\"expires_in\":60}",
		"{\"access_token\":\"\",\"refresh_token\":\"r\xf0\x9f\x98\",\"expires_in\":60}",
		"{\"device_code\":\"d\xc0\x80\",\"user_code\":\"u\xff\",\"verification_uri\":\"https://auth.kimi.com/device\",\"verification_uri_complete\":\"https://auth.kimi.com/d\xe2\x82\"}",
	} {
		for _, flow := range []string{"device", "refresh", "poll"} {
			for _, status := range []int{200, 400, 401, 500} {
				probes = append(probes, xaiResponseProbe{Flow: flow, Status: status, Body: body, Previous: "old-refresh"})
			}
		}
	}
	for i := range probes {
		probes[i].Raw = []byte(probes[i].Body)
	}
	return probes
}

// runPigXaiResponse sends one raw response through the Go provider's device-authorization, refresh or poll step and reports what Pi's does: the parsed result as JSON, or the error message.
func runPigXaiResponse(t *testing.T, probe xaiResponseProbe) xaiResponseAnswer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(probe.Status)
		_, _ = io.WriteString(w, probe.Body)
	}))
	defer server.Close()
	provider := xaiOAuthProvider{client: server.Client(), deviceURL: server.URL, tokenURL: server.URL, now: func() time.Time { return time.UnixMilli(1_700_000_000_000) }}
	ctx := context.Background()
	fail := func(err error) xaiResponseAnswer { message := err.Error(); return xaiResponseAnswer{Err: &message} }
	credentials := func(c OAuthCredentials) map[string]any {
		return map[string]any{"type": "oauth", "access": c.Access, "refresh": c.Refresh, "expires": c.ExpiresMillis()}
	}
	var value any
	switch probe.Flow {
	case "device":
		status, body, err := provider.postForm(ctx, provider.deviceURL, nil)
		if err != nil {
			return fail(err)
		}
		if status < 200 || status >= 300 {
			return fail(xaiRequestFailure("device authorization", status, body))
		}
		device, err := parseXaiDeviceCode(body)
		if err != nil {
			return fail(err)
		}
		out := map[string]any{"deviceCode": device.deviceCode, "userCode": device.userCode, "verificationUri": device.verificationURI, "expiresInSeconds": device.expiresInSeconds}
		if device.verificationURIComplete != "" {
			out["verificationUriComplete"] = device.verificationURIComplete
		}
		if device.intervalSeconds != nil {
			out["intervalSeconds"] = *device.intervalSeconds
		}
		value = out
	case "refresh":
		refreshed, err := provider.RefreshTokenContext(ctx, OAuthCredentials{Refresh: probe.Previous})
		if err != nil {
			return fail(err)
		}
		value = credentials(refreshed)
	default:
		result, err := provider.pollToken(ctx, "dc")
		if err != nil {
			return fail(err)
		}
		switch result.Status {
		case DevicePollComplete:
			value = map[string]any{"status": "complete", "value": credentials(result.Value)}
		case DevicePollPending:
			value = map[string]any{"status": "pending"}
		case DevicePollSlowDown:
			out := map[string]any{"status": "slow_down"}
			if result.IntervalSeconds != nil {
				out["intervalSeconds"] = *result.IntervalSeconds
			}
			value = out
		default:
			value = map[string]any{"status": "failed", "message": result.Message}
		}
	}
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return xaiResponseAnswer{OK: encoded}
}

func TestXaiResponsesMatchPi(t *testing.T) {
	probes := xaiResponseProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/xai_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "xai.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []xaiResponseAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(raw json.RawMessage) any {
		var value any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &value)
		}
		return value
	}
	differing, succeeded, failed := 0, 0, 0
	for i, probe := range probes {
		got := runPigXaiResponse(t, probe)
		want := expected[i]
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = *got.Err == *want.Err
		}
		if same && got.Err == nil {
			same = reflect.DeepEqual(canonical(got.OK), canonical(want.OK))
		}
		if want.Err == nil {
			succeeded++
		} else {
			failed++
		}
		if !same {
			differing++
			if differing <= 8 {
				render := func(a xaiResponseAnswer) string {
					if a.Err != nil {
						return "error: " + *a.Err
					}
					return string(a.OK)
				}
				t.Errorf("%s status %d body %.160q previous %q\n  Pi  %s\n  Pig %s", probe.Flow, probe.Status, probe.Body, probe.Previous, render(want), render(got))
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes: %d succeed, %d fail", len(probes), succeeded, failed)
	if succeeded < 150 || failed < 600 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}
