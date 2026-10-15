package ai

// pi: packages/ai/src/auth/oauth/openai-codex.ts

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

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type codexResponseProbe struct {
	Flow       string `json:"flow"`
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Body       string `json:"body"`
	// Raw is Body's exact bytes for the oracle, which builds its Response from them: a JSON string cannot carry ill-formed UTF-8.
	Raw      []byte `json:"raw"`
	Previous string `json:"previous,omitempty"`
}

type codexResponseAnswer struct {
	OK     json.RawMessage `json:"ok"`
	Err    *string         `json:"err"`
	Syntax bool            `json:"syntax"`
}

// codexResponseProbes serve the token and device endpoints every shape of body and status. Members hold the values the typed credentials can represent, or an empty or absent one: Pi also accepts any other truthy value (a number or object as the token or id), which Go's string credentials cannot hold and report as a missing field.
func codexResponseProbes() []codexResponseProbe {
	random := rand.New(rand.NewSource(20261015))
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
	str := func() string {
		return pick(`"abc"`, `"abc"`, `"abc"`, `""`, `null`, `0`, `false`, `"é😀<>&"`, `"\u2028"`)
	}
	interval := func() string {
		return pick(`5`, `0`, `1.5`, `-1`, `"5"`, `" 5 "`, `""`, `"  "`, `"0x10"`, `"1e3"`, `".5"`, `"5."`, `"+5"`, `"-0"`, `"-1"`, `"Infinity"`, `"-Infinity"`, `"1_0"`, `"0b11"`, `"0o7"`, `"abc"`, `"5px"`, `"\u00a05\u00a0"`, `"\ufeff5"`, `null`, `true`, `[]`, `{}`, `1e999`, `-1e999`, `1e-999`, `"1e999"`, `"٣"`, `"５"`)
	}
	extra := func() string { return pick(`{"b":1,"a":[1,null]}`, `"v"`, `1.50`, `true`, `null`, `{"10":1,"2":2}`) }
	var probes []codexResponseProbe
	text := http.StatusText
	fixed := []string{``, `{`, `not json`, `[]`, `[1]`, `5`, `"text"`, `null`, `true`, "\ufeff\ufeff{}", "\ufeff\ufeff\ufeff{}", "\ufeff\ufeffnot json", "\ufeff\ufeff" + `{"error":"slow_down"}`, "\ufeff{}", "\ufeff" + `{"error":"slow_down"}`, "\ufeff" + `{"authorization_code":"c","code_verifier":"v"}`, ` {"access_token":"a","refresh_token":"r","expires_in":60} `, `{"access_token":"a","access_token":"b","refresh_token":"r","expires_in":60}`,
		`{"error":"deviceauth_authorization_pending"}`, `{"error":"slow_down"}`, `{"error":{"code":"deviceauth_authorization_pending"}}`, `{"error":{"code":"slow_down"}}`, `{"error":{"code":5}}`, `{"error":null}`, `{"error":[]}`, `{"error":"other","x":1}`, `{"b":1,"a":2,"10":3,"2":4}`, `{"x":"\u2028é<>&"}`, `{"n":1.0,"m":-0,"k":1e21,"j":0.1}`,
		`{"access_token":"a","refresh_token":"r","expires_in":3600}`, `{"access_token":"a","refresh_token":"r","expires_in":1e999}`, `{"access_token":"a","refresh_token":"r","expires_in":"60"}`, `{"access_token":"a","refresh_token":"r"}`,
		`{"device_auth_id":"i","user_code":"u","interval":5}`, `{"device_auth_id":"i","user_code":"u","interval":"5"}`, `{"authorization_code":"c","code_verifier":"v"}`, `{"authorization_code":"c"}`, `{"authorization_code":"","code_verifier":"v"}`}
	for _, body := range fixed {
		for _, flow := range []string{"exchange", "refresh", "device", "poll"} {
			for _, status := range []int{200, 201, 400, 401, 403, 404, 429, 500} {
				probes = append(probes, codexResponseProbe{Flow: flow, Status: status, StatusText: text(status), Body: body, Previous: "old"})
			}
		}
	}
	// Ill-formed UTF-8: fetch decodes one U+FFFD per maximal ill-formed subpart, in error text and in JSON bodies alike.
	for _, body := range []string{
		"\xff\xfe", "a\xe2\x82b", "{\"error\":\"x\xff\xfe \xe2\x82 \xf0\x9f\x98 \xed\xa0\x80 \xc0\x80\"}",
		"{\"access_token\":\"a\xff\",\"refresh_token\":\"r\xe2\x82\",\"expires_in\":60}",
		"{\"access_token\":\"\",\"refresh_token\":\"r\xf0\x9f\x98\",\"expires_in\":60}",
		"{\"device_auth_id\":\"i\xc0\x80\",\"user_code\":\"\",\"interval\":5}", "{\"device_auth_id\":\"i\",\"user_code\":\"u\xff\",\"interval\":5}",
		"{\"authorization_code\":\"c\xe2\x82\",\"code_verifier\":\"\"}", "{\"authorization_code\":\"c\xe2\x82\",\"code_verifier\":\"v\xf0\x9f\"}",
	} {
		for _, flow := range []string{"exchange", "refresh", "device", "poll"} {
			for _, status := range []int{200, 400, 500} {
				probes = append(probes, codexResponseProbe{Flow: flow, Status: status, StatusText: text(status), Body: body, Previous: "old"})
			}
		}
	}
	for range 1200 {
		flow := pick("exchange", "refresh", "device", "device", "poll", "poll")
		var body string
		switch flow {
		case "exchange", "refresh":
			body = join(member("access_token", str()), member("refresh_token", str()), member("expires_in", `60`, `3600`, `0`, `-1`, `1.5`, `"60"`, `null`, `1e999`), member("extra", extra()))
		case "device":
			body = join(member("device_auth_id", str()), member("user_code", str()), member("interval", interval()), member("extra", extra()))
		default:
			body = join(member("authorization_code", str()), member("code_verifier", str()), member("error", `"slow_down"`, `"x"`, `{"code":"slow_down"}`, `null`), member("extra", extra()))
		}
		status := []int{200, 200, 200, 400, 403, 404, 500}[random.Intn(7)]
		probes = append(probes, codexResponseProbe{Flow: flow, Status: status, StatusText: text(status), Body: body, Previous: "old"})
	}
	// The token failure detail falls back to response.statusText, the reason phrase the server sent: empty, or not the canonical one.
	for _, statusText := range []string{"", "Custom Reason", "OK"} {
		for _, flow := range []string{"exchange", "refresh"} {
			for _, body := range []string{``, `x`} {
				probes = append(probes, codexResponseProbe{Flow: flow, Status: 500, StatusText: statusText, Body: body, Previous: "old"}, codexResponseProbe{Flow: flow, Status: 403, StatusText: statusText, Body: body, Previous: "old"})
			}
		}
	}
	for i := range probes {
		probes[i].Raw = []byte(probes[i].Body)
		switch i % 4 {
		case 1:
			probes[i].StatusText = "Custom Reason"
		case 2:
			probes[i].StatusText = ""
		}
	}
	return probes
}

func runPigCodexResponse(t *testing.T, probe codexResponseProbe) codexResponseAnswer {
	t.Helper()
	withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
		response := cannedResp(probe.Status, probe.Body)
		response.Status = fmt.Sprintf("%d %s", probe.Status, probe.StatusText)
		return response, nil
	})
	ctx := t.Context()
	fail := func(err error) codexResponseAnswer {
		message := err.Error()
		return codexResponseAnswer{Err: &message, Syntax: errors.Is(err, errOAuthResponseNotJSON)}
	}
	credentials := func(c OAuthCredentials) map[string]any {
		return map[string]any{"access": c.Access, "refresh": c.Refresh, "expires": c.ExpiresMillis()}
	}
	var value any
	switch probe.Flow {
	case "exchange":
		creds, err := postCodexTokenForm(ctx, formFields{{"grant_type", "authorization_code"}}, "exchange")
		if err != nil {
			return fail(err)
		}
		value = credentials(creds)
	case "refresh":
		creds, err := postCodexTokenForm(ctx, formFields{{"grant_type", "refresh_token"}}, "refresh")
		if err != nil {
			return fail(err)
		}
		value = credentials(creds)
	case "device":
		device, err := startCodexDeviceAuth(ctx)
		if err != nil {
			return fail(err)
		}
		value = map[string]any{"deviceAuthId": device.deviceAuthID, "userCode": device.userCode, "intervalSeconds": device.intervalSeconds}
	default:
		result, err := codexPollStep(ctx, codexDeviceAuthInfo{deviceAuthID: "id", userCode: "uc", intervalSeconds: 1})
		if err != nil {
			return fail(err)
		}
		switch result.Status {
		case DevicePollComplete:
			value = map[string]any{"status": "complete", "value": map[string]any{"authorizationCode": result.Value.authorizationCode, "codeVerifier": result.Value.codeVerifier}}
		case DevicePollPending:
			value = map[string]any{"status": "pending"}
		case DevicePollSlowDown:
			value = map[string]any{"status": "slow_down"}
		default:
			value = map[string]any{"status": "failed", "message": result.Message}
		}
	}
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return codexResponseAnswer{OK: encoded}
}

func TestCodexResponsesMatchPi(t *testing.T) {
	probes := codexResponseProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/codex_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "openai-codex.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []codexResponseAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	// Pi's credentials carry an absolute expiry from its fixed clock; both sides are compared as a time to live, to the nearest 100 ms.
	normalize := func(raw json.RawMessage, pi bool, started float64) any {
		var value any
		if len(raw) == 0 {
			return nil
		}
		_ = json.Unmarshal(raw, &value)
		var walk func(any)
		walk = func(node any) {
			if object, ok := node.(map[string]any); ok {
				if expires, ok := object["expires"].(float64); ok {
					base := started
					if pi {
						base = 1_700_000_000_000
					}
					object["ttlMs"] = math.Round((expires-base)/100) * 100
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
		started := float64(nowMillis())
		got := runPigCodexResponse(t, probe)
		want := expected[i]
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = got.Syntax == want.Syntax && *got.Err == *want.Err
		}
		if same && got.Err == nil {
			same = reflect.DeepEqual(normalize(got.OK, false, started), normalize(want.OK, true, 0))
		}
		if want.Err == nil {
			succeeded++
		} else {
			failed++
		}
		if !same {
			differing++
			if differing <= 8 {
				render := func(a codexResponseAnswer) string {
					if a.Err != nil {
						return fmt.Sprintf("error (syntax %v): %s", a.Syntax, *a.Err)
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
	if succeeded < 200 || failed < 1000 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}

type erroringBody struct{ data string }

func (b *erroringBody) Read(p []byte) (int, error) {
	if b.data == "" {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (*erroringBody) Close() error { return nil }

// openai-codex.ts reads an error body with response.text().catch(() => "") and a success body with response.json(), which rejects with undici's TypeError "terminated" when the connection ends early (probed on Node 24): a truncated body is never parsed, even when the bytes that arrived are complete JSON.
func TestCodexTruncatedBodyReadsAsNoBody(t *testing.T) {
	complete := `{"access_token":"a","refresh_token":"r","expires_in":60,"device_auth_id":"i","user_code":"u","interval":5,"authorization_code":"c","code_verifier":"v"}`
	for _, tc := range []struct {
		name   string
		status int
		run    func() string
		want   string
	}{
		{"exchange ok", 200, func() string {
			_, err := postCodexTokenForm(t.Context(), formFields{{"a", "b"}}, "exchange")
			return errorText(err)
		}, "terminated"},
		{"exchange failed", 500, func() string {
			_, err := postCodexTokenForm(t.Context(), formFields{{"a", "b"}}, "exchange")
			return errorText(err)
		}, "OpenAI Codex token exchange failed (500): x"},
		{"device ok", 200, func() string {
			_, err := startCodexDeviceAuth(t.Context())
			return errorText(err)
		}, "terminated"},
		{"device failed", 500, func() string {
			_, err := startCodexDeviceAuth(t.Context())
			return errorText(err)
		}, "OpenAI Codex device code request failed with status 500"},
		{"poll ok", 200, func() string {
			_, err := codexPollStep(t.Context(), codexDeviceAuthInfo{deviceAuthID: "id", userCode: "uc"})
			return errorText(err)
		}, "terminated"},
		{"poll failed", 500, func() string {
			result, err := codexPollStep(t.Context(), codexDeviceAuthInfo{deviceAuthID: "id", userCode: "uc"})
			if err != nil {
				return errorText(err)
			}
			return result.Message
		}, "OpenAI Codex device auth failed with status 500"},
	} {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Status: fmt.Sprintf("%d x", tc.status), Body: &erroringBody{data: complete}, Header: http.Header{}}, nil
		})
		if got := tc.run(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
