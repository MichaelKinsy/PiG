package ai

// pi: packages/ai/src/auth/oauth/meta.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
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

type metaResponseProbe struct {
	Flow   string `json:"flow"`
	Status int    `json:"status"`
	Raw    []byte `json:"raw"`
}

type metaResponseAnswer struct {
	OK  json.RawMessage `json:"ok"`
	Err *string         `json:"err"`
}

// metaResponseProbes serve the device, poll and mint flows every shape of body and status: members absent, empty, null and of each JSON type, URLs of every scheme, numbers past the double range, bodies that are not objects, malformed, with a byte order mark or ill-formed UTF-8.
func metaResponseProbes() []metaResponseProbe {
	random := rand.New(rand.NewSource(20261017))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	member := func(key string, values ...string) string {
		if random.Intn(5) == 0 {
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
	var probes []metaResponseProbe
	add := func(flow string, status int, body string) {
		probes = append(probes, metaResponseProbe{Flow: flow, Status: status, Raw: []byte(body)})
	}
	fixed := []string{``, `{`, `not json`, `[]`, `[1,{"a":2}]`, `5`, `"text"`, `null`, `true`, `false`, "\ufeff{}", "\ufeff" + `{"error":"slow_down","interval":3}`, `{}`, ` {"api_key":"k"} `, `{"api_key":"a","api_key":"b"}`,
		`{"error":"authorization_pending"}`, `{"error":"slow_down"}`, `{"error":"slow_down","interval":3}`, `{"error":"slow_down","interval":1e999}`, `{"error":"slow_down","interval":-1}`, `{"error":"slow_down","interval":0}`, `{"error":"slow_down","interval":"3"}`,
		`{"error":"access_denied"}`, `{"error":"expired_token","error_description":"late"}`, `{"error":"other","error_description":"  why  "}`, `{"error":"other","detail":"d","message":"m"}`, `{"error":"other","message":" "}`, `{"error":"other","error_description":5,"detail":"d"}`, `{"error":5,"message":"m"}`,
		`{"access_token":"t"}`, `{"access_token":""}`, `{"access_token":5}`, `{"access_token":"t","error":"slow_down"}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://v.example/x"}`, `{"device_code":"d","user_code":"u","verification_uri":"ftp://v","verification_uri_complete":"https://c.example/?a=1"}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://v","interval":5,"expires_in":600}`, `{"device_code":"d","user_code":"u","verification_uri":"https://v","interval":1e999,"expires_in":-1}`, `{"device_code":"d","user_code":"u","verification_uri":"https://v","interval":12345678901234567890,"expires_in":1.5e3}`,
		`{"device_code":"d","user_code":"u","verification_uri":"javascript:alert(1)"}`, `{"device_code":"d","user_code":"u","verification_uri":"HTTPS://V.EXAMPLE/a b"}`, `{"device_code":"d","user_code":"","verification_uri":"https://v"}`,
		`{"b":1,"a":[1,null,{"z":1.50,"y":-0}],"10":1,"2":2,"c":"é😀<>&\u2028","big":1e21,"tiny":1e-7,"n":12345678901234567890}`, `{"z":{"b":1,"a":2},"a":0.1}`,
		`{"api_key":"k"}`, `{"api_key":""}`, `{"api_key":5}`, `{"action_url":"https://setup.example/x"}`, `{"action_url":"ftp://x"}`, `{"action_url":5}`, `{"api_key":"k","action_url":"https://x"}`,
		// Ill-formed UTF-8: fetch decodes one U+FFFD per maximal ill-formed subpart.
		"\xff\xfe", "{\"error\":\"x\xff\xfe\",\"error_description\":\"d\xe2\x82 \xf0\x9f\x98\"}", "{\"device_code\":\"d\xe2\x82\",\"user_code\":\"\",\"verification_uri\":\"https://v\"}", "{\"api_key\":\"k\xc0\x80\"}", "{\"message\":\"\xed\xa0\x80\"}", `{"a":1,"b":2,"a":3}`, `{"__proto__":{"x":1},"device_code":"d"}`, `{"__proto__":null}`, `{"s":"\ud800","t":"\udc00\ud83d","u":"\u0000\u001f\u007f\b\f","v":"\/"}`, `{"error_description":"\ud800x"}`, "\ufeff\ufeff{}", "\ufeff\ufeff\ufeff{}", "\ufeff\ufeffnot json", "\ufeff\ufeff" + `{"error":"slow_down","interval":3}`, "\ufeff", `{"1e3":1,"01":2,"1":3,"-1":4,"4294967294":5,"4294967295":6,"9007199254740993":7}`, `{"n":[1E+2,0.0000001,123e-20,5e-324,1e-324,1.7976931348623157e308,1.7976931348623159e308,-0.0,100000000000000000000,1e20,0.1e1]}`, `{"device_code":"d","user_code":"u","verification_uri":"https://v","interval":1e-400,"expires_in":5e-324}`, `{"error_description":"\u00a0\u2028 x \ufeff\u3000"}`, `[[[[[[[[[[{"a":[]}]]]]]]]]]]`, `{"error":"slow_down","interval":0.5}`, `{"error":"slow_down","interval":-0}`}
	for _, body := range fixed {
		for _, flow := range []string{"device", "poll", "mint"} {
			for _, status := range []int{200, 201, 299, 300, 400, 401, 403, 404, 429, 500} {
				add(flow, status, body)
			}
		}
	}
	str := func() string {
		return pick(`"abc"`, `"abc"`, `"abc"`, `""`, `"  "`, `null`, `0`, `false`, `"é😀<>&"`)
	}
	number := func() string {
		return pick(`5`, `0`, `-1`, `1.5`, `1e999`, `-1e999`, `1e-999`, `"5"`, `null`, `true`, `[]`, `12345678901234567`, `1e21`)
	}
	url := func() string {
		return pick(`"https://v.example/a"`, `"http://v.example"`, `"ftp://v"`, `"javascript:x"`, `"https://"`, `"https://v.example/ a"`, `"//v"`, `""`, `null`, `5`, `"HTTPS://V.EXAMPLE:443/"`, `"https://v.example:8080/é?x=1#f"`, `"http://[::1]/"`, `"https://user:pw@v.example/"`)
	}
	for range 1500 {
		flow := pick("device", "device", "poll", "mint")
		var body string
		switch flow {
		case "device":
			body = join(member("device_code", str()), member("user_code", str()), member("verification_uri", url()), member("verification_uri_complete", url()), member("interval", number()), member("expires_in", number()), member("extra", `{"b":1,"a":[1.0]}`, `"v"`))
		case "poll":
			body = join(member("access_token", str()), member("error", `"authorization_pending"`, `"slow_down"`, `"access_denied"`, `"expired_token"`, `"x"`, `""`, `null`, `5`), member("interval", number()), member("error_description", str()), member("detail", str()), member("message", str()))
		default:
			body = join(member("api_key", str()), member("action_url", url()), member("error_description", str()), member("detail", str()), member("message", str()), member("error", str()))
		}
		add(flow, []int{200, 200, 200, 400, 401, 403, 500}[random.Intn(7)], body)
	}
	return probes
}

func runPigMetaResponse(t *testing.T, probe metaResponseProbe) metaResponseAnswer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(probe.Status)
		_, _ = w.Write(probe.Raw)
	}))
	defer server.Close()
	provider := newMetaOAuthProvider()
	provider.deviceAuthorizationURL, provider.deviceTokenURL, provider.apiKeyMintURL = server.URL, server.URL, server.URL
	provider.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	fail := func(err error) metaResponseAnswer {
		message := err.Error()
		return metaResponseAnswer{Err: &message}
	}
	var value any
	switch probe.Flow {
	case "device":
		device, err := provider.startDeviceAuthorization(t.Context())
		if err != nil {
			return fail(err)
		}
		answer := map[string]any{"deviceCode": device.deviceCode, "userCode": device.userCode, "verificationUri": device.verificationURI}
		if device.intervalSeconds != nil {
			answer["intervalSeconds"] = *device.intervalSeconds
		}
		if device.expiresInSeconds != nil {
			answer["expiresInSeconds"] = *device.expiresInSeconds
		}
		value = answer
	case "poll":
		result, err := provider.pollIdentityToken(t.Context(), "dc")
		if err != nil {
			return fail(err)
		}
		switch result.Status {
		case DevicePollComplete:
			value = map[string]any{"status": "complete", "value": result.Value}
		case DevicePollPending:
			value = map[string]any{"status": "pending"}
		case DevicePollSlowDown:
			answer := map[string]any{"status": "slow_down"}
			if result.IntervalSeconds != nil {
				answer["intervalSeconds"] = *result.IntervalSeconds
			}
			value = answer
		default:
			value = map[string]any{"status": "failed", "message": result.Message}
		}
	default:
		creds, err := provider.mintAPIKey(t.Context(), "tok")
		if err != nil {
			return fail(err)
		}
		value = map[string]any{"type": "oauth", "refresh": creds.Refresh, "access": creds.Access, "expires": creds.ExpiresMillis()}
	}
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return metaResponseAnswer{OK: encoded}
}

func TestMetaResponsesMatchPi(t *testing.T) {
	probes := metaResponseProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/meta_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "meta.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []metaResponseAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	decode := func(raw json.RawMessage) any {
		var value any
		_ = json.Unmarshal(raw, &value)
		return value
	}
	differing, succeeded, failed := 0, 0, 0
	for i, probe := range probes {
		got := runPigMetaResponse(t, probe)
		want := expected[i]
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = *got.Err == *want.Err
		}
		if same && got.Err == nil {
			same = reflect.DeepEqual(decode(got.OK), decode(want.OK))
		}
		if want.Err == nil {
			succeeded++
		} else {
			failed++
		}
		if !same {
			differing++
			if differing <= 8 {
				render := func(a metaResponseAnswer) string {
					if a.Err != nil {
						return "error: " + *a.Err
					}
					return string(a.OK)
				}
				t.Errorf("%s status %d body %.140q\n  Pi  %.300s\n  Pig %.300s", probe.Flow, probe.Status, probe.Raw, render(want), render(got))
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes: %d succeed, %d fail", len(probes), succeeded, failed)
	if succeeded < 500 || failed < 1000 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}

// meta.ts readJson is `try { await response.json() } catch { null }`: a body that fails to read is null, even when the bytes that arrived are complete JSON.
func TestMetaTruncatedBodyReadsAsNull(t *testing.T) {
	complete := `{"api_key":"k","device_code":"d","user_code":"u","verification_uri":"https://v","access_token":"t","error":"slow_down","message":"boom"}`
	provider := testMetaProvider(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: &erroringBody{data: complete}, Header: http.Header{}}, nil
	})
	if _, err := provider.mintAPIKey(t.Context(), "tok"); err == nil || err.Error() != "Meta did not issue an API key." {
		t.Errorf("mint of a truncated body: %v", err)
	}
	if _, err := provider.startDeviceAuthorization(t.Context()); err == nil || err.Error() != "Invalid Meta device authorization response: null" {
		t.Errorf("device authorization of a truncated body: %v", err)
	}
	result, err := provider.pollIdentityToken(t.Context(), "dc")
	if err != nil || result.Status != DevicePollFailed || result.Message != "Meta device token request failed with status 200" {
		t.Errorf("poll of a truncated body: %+v %v", result, err)
	}
}
