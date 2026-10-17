package ai

// pi: packages/ai/src/auth/oauth/kimi-coding.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
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

type kimiResponseProbe struct {
	Flow     string `json:"flow"`
	Status   int    `json:"status"`
	Body     string `json:"body"`
	Previous string `json:"previous,omitempty"`
	// Raw is Body's exact bytes for the oracle, which builds its Response from them: a JSON string cannot carry ill-formed UTF-8.
	Raw []byte `json:"raw"`
}

type kimiResponseAnswer struct {
	OK  json.RawMessage `json:"ok"`
	Err *string         `json:"err"`
}

// kimiResponseProbes serve the device-authorization and token endpoints bodies a server sends and the ones it should not: every field absent, null, empty, of another JSON type, out of range, repeated or non-finite, bodies that are not objects, not JSON, empty or start with a byte order mark, and error bodies, over several statuses (the refresh retries and backs off on 429 and 5xx).
func kimiResponseProbes() []kimiResponseProbe {
	random := rand.New(rand.NewSource(20261013))
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
	url := func() string {
		return pick(`"https://auth.kimi.com/device"`, `"https://auth.kimi.com/device?user_code=ABCD"`, `"http://auth.kimi.com/device"`, `"ftp://x/"`, `"javascript:alert(1)"`, `"not a url"`, `""`, `null`, `5`, `"HTTPS://AUTH.KIMI.COM:443/a b"`, `"https://auth.kimi.com/#frag"`)
	}
	str := func() string {
		return pick(`"abc"`, `"abc"`, `"abc"`, `""`, `null`, `5`, `true`, `[]`, `{}`, `"é😀<>&"`, `"\u2028"`)
	}
	num := func() string {
		return pick(`600`, `600`, `1800`, `0`, `-5`, `1.5`, `0.5`, `null`, `"600"`, `true`, `1e999`, `-1e999`, `1e-999`, `[]`, `1e21`, `100000000000000000000000`)
	}
	statuses := []int{200, 200, 200, 400, 401, 403, 429, 500, 503, 201}
	fixed := []string{`[]`, `[1]`, `5`, `"text"`, `null`, `true`, ``, `{`, `not json`, `{} {}`, `{}x`, "\ufeff\ufeff{}", "\ufeff\ufeff\ufeff{}", "\ufeff\ufeffnot json", "\ufeff\ufeff" + `{"error":"slow_down"}`, "\ufeff{}", ` {"access_token":"a","refresh_token":"r","expires_in":60} `, `{"access_token":"a","access_token":"b","refresh_token":"r","expires_in":60}`,
		`{"error":"slow_down","interval":7}`, `{"error":"slow_down","interval":0}`, `{"error":"slow_down","interval":-1}`, `{"error":"slow_down","interval":"7"}`, `{"error":"slow_down","interval":1e999}`, `{"error":"slow_down"}`,
		`{"error":"authorization_pending"}`, `{"error":"expired_token"}`, `{"error":"access_denied"}`, `{"error":"invalid_grant","error_description":"gone"}`, `{"error":"other","error_description":"why"}`, `{"error":"other"}`, `{"error":5,"error_description":["x"]}`,
		`{"error":"e","error_description":""}`, `{"b":1,"a":2,"10":3,"2":4}`, `{"x":"\u2028é<>&"}`, `{"n":1.0,"m":-0,"k":1e21,"j":0.1}`, `{"a":{"b":[1,{"c":null}]}}`, `{"access_token":"a","refresh_token":"r","expires_in":3600}`, `{"access_token":"a","refresh_token":"r","expires_in":3600,"extra":{"b":1,"a":[1,2]}}`}
	var probes []kimiResponseProbe
	for _, body := range fixed {
		for _, flow := range []string{"device", "refresh", "poll"} {
			for _, status := range []int{200, 400, 401, 500, 429} {
				probes = append(probes, kimiResponseProbe{Flow: flow, Status: status, Body: body, Previous: "old-refresh"})
			}
		}
	}
	for range 900 {
		flow := pick("device", "refresh", "poll", "poll")
		var body string
		if flow == "device" {
			body = join(member("device_code", str()), member("user_code", str()), member("verification_uri", url()), member("verification_uri_complete", url()), member("interval", num()), member("expires_in", num()), member("error", str()))
		} else {
			body = join(member("access_token", str()), member("refresh_token", str()), member("expires_in", num()), member("error", `"slow_down"`, `"authorization_pending"`, `"x"`, `""`, `5`, `"invalid_grant"`, `"expired_token"`, `"access_denied"`), member("interval", num()), member("error_description", str()), member("extra", `{"b":1,"a":[1,null]}`, `"v"`, `1.50`))
		}
		probes = append(probes, kimiResponseProbe{Flow: flow, Status: statuses[random.Intn(len(statuses))], Body: body, Previous: "old-refresh"})
	}
	for _, body := range []string{
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.kimi.com/device","verification_uri_complete":"https://auth.kimi.com/device?c=1"}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.kimi.com/device","verification_uri_complete":"https://auth.kimi.com/device?c=1","interval":10,"expires_in":300.5}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.kimi.com/device","verification_uri_complete":"https://auth.kimi.com/device?c=1","interval":1e999,"expires_in":1e999}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://auth.kimi.com/device","verification_uri_complete":"https://auth.kimi.com/device?c=1","interval":-1,"expires_in":0}`,
	} {
		probes = append(probes, kimiResponseProbe{Flow: "device", Status: 200, Body: body})
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
				probes = append(probes, kimiResponseProbe{Flow: flow, Status: status, Body: body, Previous: "old-refresh"})
			}
		}
	}
	for i := range probes {
		probes[i].Raw = []byte(probes[i].Body)
	}
	return probes
}

// runPigKimiResponse sends one raw response through the Go provider's device-authorization, refresh or poll step.
func runPigKimiResponse(t *testing.T, probe kimiResponseProbe) kimiResponseAnswer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(probe.Status)
		_, _ = io.WriteString(w, probe.Body)
	}))
	defer server.Close()
	provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL, sleep: func(context.Context, time.Duration) error { return nil }}
	ctx := context.Background()
	fail := func(err error) kimiResponseAnswer { message := err.Error(); return kimiResponseAnswer{Err: &message} }
	// A credential's expiry is reported as the time to live from the call, to the nearest 100 ms: Pi's clock is fixed, Go's is not.
	credentials := func(access, refresh string, expires float64, started int64) map[string]any {
		return map[string]any{"access": access, "refresh": refresh, "ttlMs": math.Round((expires-float64(started))/100) * 100}
	}
	started := nowMillis()
	var value any
	switch probe.Flow {
	case "device":
		device, err := provider.startDeviceAuthorization(ctx)
		if err != nil {
			return fail(err)
		}
		value = map[string]any{"deviceCode": device.DeviceCode, "userCode": device.UserCode, "verificationUri": device.VerificationURI, "verificationUriComplete": device.VerificationURIComplete, "intervalSeconds": device.Interval, "expiresInSeconds": device.ExpiresIn}
	case "refresh":
		refreshed, err := provider.RefreshTokenContext(ctx, OAuthCredentials{Refresh: probe.Previous})
		if err != nil {
			return fail(err)
		}
		value = credentials(refreshed.Access, refreshed.Refresh, refreshed.ExpiresMillis(), started)
	default:
		result, err := provider.pollToken(ctx, "dc")
		if err != nil {
			return fail(err)
		}
		switch result.Status {
		case DevicePollComplete:
			creds, err := kimiCredentials(result.Value)
			if err != nil {
				return fail(err)
			}
			value = map[string]any{"status": "complete", "value": credentials(creds.Access, creds.Refresh, creds.ExpiresMillis(), started)}
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
	return kimiResponseAnswer{OK: encoded}
}

func TestKimiResponsesMatchPi(t *testing.T) {
	probes := kimiResponseProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/kimi_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "kimi-coding.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []kimiResponseAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	// Pi's credentials carry an absolute expiry from its fixed clock; both sides are compared as a time to live.
	normalize := func(raw json.RawMessage, pi bool) any {
		var value any
		if len(raw) == 0 {
			return nil
		}
		_ = json.Unmarshal(raw, &value)
		var walk func(any)
		walk = func(node any) {
			if object, ok := node.(map[string]any); ok {
				if expires, ok := object["expires"].(float64); ok && pi {
					object["ttlMs"] = math.Round((expires-1_700_000_000_000)/100) * 100
					delete(object, "expires")
				}
				for _, child := range object {
					walk(child)
				}
			}
		}
		walk(value)
		return value
	}
	differing, succeeded, failed := 0, 0, 0
	for i, probe := range probes {
		got := runPigKimiResponse(t, probe)
		want := expected[i]
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = *got.Err == *want.Err
		}
		if same && got.Err == nil {
			same = reflect.DeepEqual(normalize(got.OK, false), normalize(want.OK, true))
		}
		if want.Err == nil {
			succeeded++
		} else {
			failed++
		}
		if !same {
			differing++
			if differing <= 8 {
				render := func(a kimiResponseAnswer) string {
					if a.Err != nil {
						return "error: " + *a.Err
					}
					return string(a.OK)
				}
				t.Errorf("%s status %d body %.140q\n  Pi  %.300s\n  Pig %.300s", probe.Flow, probe.Status, probe.Body, render(want), render(got))
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes: %d succeed, %d fail", len(probes), succeeded, failed)
	if succeeded < 200 || failed < 600 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}
