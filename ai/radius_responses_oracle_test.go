package ai

// pi: packages/ai/src/auth/oauth/radius.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type radiusResponseProbe struct {
	Flow   string `json:"flow"`
	Status int    `json:"status"`
	Raw    []byte `json:"raw"`
}

type radiusResponseAnswer struct {
	OK     json.RawMessage `json:"ok"`
	Err    *string         `json:"err"`
	Syntax bool            `json:"syntax"`
}

// radiusResponseProbes serve every flow every shape of body and status. A scope is a non-empty string: Pi keeps a null or empty one on the credential, which Pig's optional Scope cannot tell from none. Members hold the values Go's typed results can represent, or a falsy, null or absent one: Pi also accepts any other truthy value (a number as a token, a string as expires_in), which Pig reports as missing.
func radiusResponseProbes() []radiusResponseProbe {
	random := rand.New(rand.NewSource(20261016))
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
	var probes []radiusResponseProbe
	add := func(flow string, status int, body string) {
		probes = append(probes, radiusResponseProbe{Flow: flow, Status: status, Raw: []byte(body)})
	}
	fixed := []string{``, `{`, `not json`, `[]`, `[1]`, `5`, `"text"`, `"{\"error\":\"x\"}"`, `null`, `true`, `false`, "\ufeff{}", "\ufeffnull", "\ufeff" + `{"error":"authorization_pending"}`,
		`{"error":"authorization_pending"}`, `{"error":"slow_down","error_description":"d"}`, `{"error":"expired_token"}`, `{"error":"access_denied","error_description":""}`, `{"error":"other","error_description":"why"}`,
		`{"error":"","error_description":"only"}`, `{"error":5,"error_description":"only"}`, `{"error":null}`, `{"error":{"code":"x"}}`, `{"error_description":"d"}`, `{"error_description":5}`, `{"message":"m"}`, `{}`,
		`{"authorizationEndpoint":"https://a/authorize"}`, `{"authorizationEndpoint":""}`, `{"authorizationEndpoint":null}`, `{"authorizationEndpoint":5}`, `{"authorizationEndpoint":"https://a","x":1}`,
		`{"access_token":"a","refresh_token":"r","expires_in":3600,"scope":"s"}`, `{"access_token":"a","refresh_token":"r","expires_in":3600}`, `{"access_token":"a","refresh_token":"r","expires_in":1e999}`, `{"access_token":"a","refresh_token":"r"}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://v","expires_in":600}`, `{"device_code":"d","user_code":"u","verification_uri":"https://v","expires_in":600,"interval":5}`, `{"device_code":"d","user_code":"u","verification_uri":"https://v","expires_in":-5}`, `{"device_code":"d","user_code":"u","verification_uri":"https://v","expires_in":1e-999}`, `{"device_code":"d","user_code":"u","verification_uri":"https://v","expires_in":600,"interval":null}`,
		`{"x":"\u2028é<>&"}`, `{"b":1,"a":2}`,
		// Ill-formed UTF-8: fetch decodes one U+FFFD per maximal ill-formed subpart, in error text and in JSON bodies alike.
		"\xff\xfe", "a\xe2\x82b", "{\"error\":\"x\xff\xfe \xe2\x82\"}", "{\"error\":\"\",\"error_description\":\"d\xf0\x9f\x98 \xed\xa0\x80 \xc0\x80\"}", "{\"authorizationEndpoint\":\"https://a\xe2\x82\"}",
		"{\"access_token\":\"a\xff\",\"refresh_token\":\"r\xe2\x82\",\"expires_in\":60}", "{\"device_code\":\"d\xc0\x80\",\"user_code\":\"u\",\"verification_uri\":\"v\xe2\x82\",\"expires_in\":60}"}
	for _, body := range fixed {
		for _, flow := range []string{"discovery", "token", "device", "poll"} {
			for _, status := range []int{200, 201, 202, 299, 300, 400, 401, 404, 500} {
				add(flow, status, body)
			}
		}
	}
	str := func() string { return pick(`"abc"`, `"abc"`, `"abc"`, `""`, `null`, `0`, `false`, `"é😀<>&"`) }
	for range 1500 {
		flow := pick("discovery", "token", "device", "poll", "poll")
		var body string
		switch flow {
		case "discovery":
			body = join(member("authorizationEndpoint", str()), member("extra", `1`, `"x"`))
		case "token", "poll":
			if random.Intn(3) == 0 {
				body = join(member("error", `"authorization_pending"`, `"slow_down"`, `"expired_token"`, `"access_denied"`, `"x"`, `""`, `null`, `5`), member("error_description", str()))
			} else {
				body = join(member("access_token", str()), member("refresh_token", str()), member("expires_in", `60`, `3600`, `0`, `-1`, `1.5`, `null`, `1e999`, `1e-999`, `-0`), member("scope", `"s"`))
			}
		default:
			body = join(member("device_code", str()), member("user_code", str()), member("verification_uri", str()), member("expires_in", `600`, `0`, `-0`, `1.5`, `null`, `1e999`, `1e-999`, `-5`), member("interval", `5`, `0`, `1.5`, `null`, `1e999`, `-1`))
		}
		add(flow, []int{200, 200, 200, 400, 400, 401, 500}[random.Intn(7)], body)
	}
	return probes
}

func runPigRadiusResponse(t *testing.T, probe radiusResponseProbe) radiusResponseAnswer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(probe.Status)
		_, _ = w.Write(probe.Raw)
	}))
	defer server.Close()
	oauth := CreateRadiusOAuth(RadiusOAuthOptions{ID: "radius", Name: "Radius", Gateway: server.URL})
	oauth.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	ctx := context.Background()
	fail := func(err error) radiusResponseAnswer {
		message := strings.ReplaceAll(err.Error(), server.URL, "https://gw.example")
		return radiusResponseAnswer{Err: &message, Syntax: errors.Is(err, errOAuthResponseNotJSON)}
	}
	credentials := func(c OAuthCredentials) map[string]any {
		value := map[string]any{"type": "oauth", "access": c.Access, "refresh": c.Refresh, "expires": c.ExpiresMillis()}
		if c.Scope != "" {
			value["scope"] = c.Scope
		}
		return value
	}
	var value any
	switch probe.Flow {
	case "discovery":
		endpoint, err := oauth.loadOAuthDiscovery(ctx)
		if err != nil {
			return fail(err)
		}
		value = map[string]any{"authorizationEndpoint": endpoint}
	case "token":
		creds, err := oauth.requestOAuthToken(ctx, formFields{{"grant_type", "x"}})
		if err != nil {
			return fail(err)
		}
		value = credentials(creds)
	case "device":
		device, err := oauth.requestDeviceAuthorization(ctx)
		if err != nil {
			return fail(err)
		}
		answer := map[string]any{"device_code": device.DeviceCode, "user_code": device.UserCode, "verification_uri": device.VerificationURI, "expires_in": device.ExpiresIn}
		if device.Interval != nil {
			answer["interval"] = *device.Interval
		}
		value = answer
	default:
		result, err := oauth.pollDeviceToken(ctx, formFields{{"grant_type", "x"}})
		if err != nil {
			return fail(err)
		}
		switch result.Status {
		case DevicePollComplete:
			value = map[string]any{"poll": map[string]any{"status": "complete", "value": credentials(result.Value)}}
		case DevicePollPending:
			value = map[string]any{"poll": map[string]any{"status": "pending"}}
		case DevicePollSlowDown:
			value = map[string]any{"poll": map[string]any{"status": "slow_down"}}
		default:
			value = map[string]any{"poll": map[string]any{"status": "failed", "message": result.Message}}
		}
	}
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return radiusResponseAnswer{OK: encoded}
}

func TestRadiusResponsesMatchPi(t *testing.T) {
	probes := radiusResponseProbes()
	// The poll flow's device request is answered by the driver itself; the Go side polls the token endpoint directly.
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/radius_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "radius.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []radiusResponseAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	decode := func(raw json.RawMessage) any {
		var value any
		_ = json.Unmarshal(raw, &value)
		// A null interval reads as an absent one: both take the default poll interval.
		if object, ok := value.(map[string]any); ok && object["interval"] == nil {
			delete(object, "interval")
		}
		return value
	}
	// Pi builds credentials from whatever members the body has. Pig's string tokens hold an absent or null token as "", which the answer Pi gave is mapped to; a token of another JSON type cannot be represented, and Pig reports the body as an error instead.
	credentialsOf := func(probe radiusResponseProbe, answer any) map[string]any {
		if probe.Flow != "token" && probe.Flow != "poll" {
			return nil
		}
		value, _ := answer.(map[string]any)
		if poll, ok := value["poll"].(map[string]any); ok {
			if poll["status"] != "complete" {
				return nil
			}
			value, _ = poll["value"].(map[string]any)
		}
		return value
	}
	unrepresentable := func(probe radiusResponseProbe, want radiusResponseAnswer) bool {
		if want.Err != nil {
			return false
		}
		value := credentialsOf(probe, decode(want.OK))
		for _, key := range []string{"access", "refresh"} {
			if token, present := value[key]; present && token != nil {
				if _, isString := token.(string); !isString {
					return true
				}
			}
		}
		return false
	}
	piAnswer := func(probe radiusResponseProbe, want radiusResponseAnswer) any {
		answer := decode(want.OK)
		if value := credentialsOf(probe, answer); value != nil {
			for _, key := range []string{"access", "refresh"} {
				if value[key] == nil {
					value[key] = ""
				}
			}
		}
		return answer
	}
	differing, succeeded, failed, skipped := 0, 0, 0, 0
	for i, probe := range probes {
		want := expected[i]
		if unrepresentable(probe, want) {
			skipped++
			continue
		}
		got := runPigRadiusResponse(t, probe)
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = got.Syntax == want.Syntax && *got.Err == *want.Err
		}
		if same && got.Err == nil {
			same = reflect.DeepEqual(decode(got.OK), piAnswer(probe, want))
		}
		if want.Err == nil {
			succeeded++
		} else {
			failed++
		}
		if !same {
			differing++
			if differing <= 8 {
				render := func(a radiusResponseAnswer) string {
					if a.Err != nil {
						return fmt.Sprintf("error (syntax %v): %s", a.Syntax, *a.Err)
					}
					return string(a.OK)
				}
				t.Errorf("%s status %d body %.140q\n  Pi  %.300s\n  Pig %.300s", probe.Flow, probe.Status, probe.Raw, render(want), render(got))
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes)-skipped)
	}
	t.Logf("%d probes (%d skipped as unrepresentable): %d succeed, %d fail", len(probes), skipped, succeeded, failed)
	if succeeded < 200 || failed < 1000 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}

// radius.ts reads an error body with response.text().catch(() => "") and a success body with response.json(), which rejects when the connection ends early: a truncated body is never parsed, even when the bytes that arrived are complete JSON. Discovery's error text() is not caught, so its failure propagates.
func TestRadiusTruncatedBodyReadsAsNoBody(t *testing.T) {
	status := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"authorizationEndpoint":"https://a","access_token":"a","refresh_token":"r","expires_in":60,"error":"boom","error_description":"why"}`
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	oauth := CreateRadiusOAuth(RadiusOAuthOptions{ID: "radius", Name: "Radius", Gateway: server.URL})
	for _, tc := range []struct {
		name   string
		status int
		run    func() (string, error)
		want   string
	}{
		{"token failed", 500, func() (string, error) {
			_, err := oauth.requestOAuthToken(t.Context(), formFields{{"a", "b"}})
			return errorText(err), err
		}, "Radius OAuth token request failed: 500"},
		{"device failed", 500, func() (string, error) {
			_, err := oauth.requestDeviceAuthorization(t.Context())
			return errorText(err), err
		}, "Radius OAuth device authorization failed: 500"},
	} {
		status = tc.status
		if got, _ := tc.run(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// response.json() of a success and the discovery error's response.text() are not caught: they reject with undici's TypeError "terminated" (probed on Node 24).
	status = 200
	if _, err := oauth.requestOAuthToken(t.Context(), formFields{{"a", "b"}}); errorText(err) != "terminated" {
		t.Errorf("token response cut off: %v, want terminated", err)
	}
	if _, err := oauth.requestDeviceAuthorization(t.Context()); errorText(err) != "terminated" {
		t.Errorf("device response cut off: %v, want terminated", err)
	}
	if endpoint, err := oauth.loadOAuthDiscovery(t.Context()); errorText(err) != "terminated" {
		t.Errorf("discovery response cut off: %q %v, want terminated", endpoint, err)
	}
	status = 500
	if _, err := oauth.loadOAuthDiscovery(t.Context()); errorText(err) != "terminated" {
		t.Errorf("discovery error body cut off: %v, want terminated", err)
	}
}
