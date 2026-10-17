package ai

// pi: packages/ai/src/auth/oauth/github-copilot.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type copilotResponseProbe struct {
	Flow       string `json:"flow"`
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Raw        []byte `json:"raw"`
	Token      string `json:"token,omitempty"`
}

type copilotResponseAnswer struct {
	OK     json.RawMessage `json:"ok"`
	Err    *string         `json:"err"`
	Syntax bool            `json:"syntax"`
}

// copilotResponseProbes serve the device, poll and token flows every shape of body and status, including error_description values of every JSON type (Pi appends any truthy one as String(value)).
func copilotResponseProbes() []copilotResponseProbe {
	random := rand.New(rand.NewSource(20261018))
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
	var probes []copilotResponseProbe
	add := func(flow string, status int, body string) {
		probes = append(probes, copilotResponseProbe{Flow: flow, Status: status, StatusText: http.StatusText(status), Raw: []byte(body)})
	}
	fixed := []string{`{"error":"x","error_description":5}`, `{"error":"x","error_description":0}`, `{"error":"x","error_description":-0}`, `{"error":"x","error_description":1e21}`, `{"error":"x","error_description":0.1}`, `{"error":"x","error_description":1e999}`,
		`{"error":"x","error_description":true}`, `{"error":"x","error_description":false}`, `{"error":"x","error_description":[]}`, `{"error":"x","error_description":[1,null,"a",[2,[3]],{},true,-0]}`, `{"error":"x","error_description":{}}`, `{"error":"x","error_description":{"a":1}}`, ``, `{`, `not json`, `[]`, `[1]`, `5`, `0`, `"text"`, `""`, `null`, `true`, `false`, "\ufeff{}", " {\"access_token\":\"t\"} ", " \n x \n ",
		`{"error":"authorization_pending"}`, `{"error":"slow_down"}`, `{"error":"slow_down","interval":10}`, `{"error":"slow_down","interval":"10"}`, `{"error":"slow_down","interval":1e999}`, `{"error":"slow_down","interval":-1}`, `{"error":"slow_down","interval":null}`,
		`{"error":""}`, `{"error":"","error_description":"d"}`, `{"error":"access_denied","error_description":"nope"}`, `{"error":"x","error_description":""}`, `{"error":"x","error_description":null}`, `{"error":5}`, `{"error":null,"access_token":"t"}`,
		`{"access_token":"t"}`, `{"access_token":""}`, `{"access_token":5}`, `{"access_token":null,"error":"x"}`, `{"access_token":"t","error":"slow_down"}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device","expires_in":900}`, `{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device","expires_in":900,"interval":null}`, `{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device","expires_in":900,"interval":"5"}`,
		`{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device","expires_in":1e999,"interval":1e999}`, `{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device","expires_in":"900"}`,
		`{"device_code":"","user_code":"","verification_uri":"","expires_in":0}`, `{"device_code":"d","user_code":"u","verification_uri":"ftp://x","expires_in":1}`, `{"device_code":"d","user_code":"u","verification_uri":"javascript:x","expires_in":1}`, `{"device_code":"d","user_code":"u","verification_uri":"HTTPS://GitHub.com/a b?x#y","expires_in":1}`, `{"device_code":"d","user_code":"u","verification_uri":"not a url","expires_in":1}`,
		`{"token":"t","expires_at":1700000000}`, `{"token":"tid=1;proxy-ep=proxy.individual.githubcopilot.com;","expires_at":1700000000.5}`, `{"token":"t","expires_at":1e999}`, `{"token":"t","expires_at":"1700000000"}`, `{"token":"","expires_at":0}`, `{"token":5,"expires_at":1}`, `{"token":"t"}`, `{"expires_at":1}`, `{"token":"t","expires_at":null}`,
		`{"a":1,"b":[1,2],"x":"\u2028é<>&"}`,
		`{"error":"x","error_description":5}`, `{"error":"x","error_description":-0.5}`, `{"error":"x","error_description":0}`, `{"error":"x","error_description":-0}`, `{"error":"x","error_description":1e999}`, `{"error":"x","error_description":1e21}`, `{"error":"x","error_description":true}`, `{"error":"x","error_description":false}`,
		`{"error":"x","error_description":[]}`, `{"error":"x","error_description":[1,[2,null],{},"s",true]}`, `{"error":"x","error_description":{}}`, `{"error":"x","error_description":{"a":1}}`,
		// Ill-formed UTF-8: fetch decodes one U+FFFD per maximal ill-formed subpart.
		"\xff\xfe", "x\xe2\x82y", "{\"error\":\"x\xff\",\"error_description\":\"d\xe2\x82 \xf0\x9f\x98\"}", "{\"token\":\"t\xc0\x80\",\"expires_at\":1}", "{\"device_code\":\"d\xed\xa0\x80\",\"user_code\":\"u\",\"verification_uri\":\"https://g.com\",\"expires_in\":1}"}
	for _, body := range fixed {
		for _, flow := range []string{"device", "poll", "token"} {
			for _, status := range []int{200, 201, 299, 300, 400, 401, 403, 404, 429, 500, 503} {
				add(flow, status, body)
			}
		}
	}
	str := func() string { return pick(`"abc"`, `"abc"`, `"abc"`, `""`, `null`, `0`, `false`, `"é😀<>&"`) }
	number := func() string { return pick(`5`, `0`, `-1`, `1.5`, `1e999`, `900`, `"5"`, `null`, `true`, `[]`, `{}`) }
	uri := func() string {
		return pick(`"https://github.com/login/device"`, `"http://g.example"`, `"ftp://g"`, `"javascript:x"`, `"https://"`, `"//g"`, `""`, `null`, `5`, `"HTTPS://G.EXAMPLE:443/é?x=1#f"`, `"https://user:pw@g.example/"`)
	}
	for range 1500 {
		flow := pick("device", "device", "poll", "token")
		var body string
		switch flow {
		case "device":
			body = join(member("device_code", str()), member("user_code", str()), member("verification_uri", uri()), member("expires_in", number()), member("interval", number()), member("extra", `{"b":1}`))
		case "poll":
			body = join(member("access_token", str()), member("error", `"authorization_pending"`, `"slow_down"`, `"access_denied"`, `"x"`, `""`, `null`, `5`), member("interval", number()), member("error_description", str(), number()))
		default:
			body = join(member("token", str()), member("expires_at", number()), member("extra", `1`))
		}
		add(flow, []int{200, 200, 200, 400, 401, 500}[random.Intn(6)], body)
	}
	return probes
}

func runPigCopilotResponse(t *testing.T, probe copilotResponseProbe) copilotResponseAnswer {
	t.Helper()
	withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: probe.Status, Status: fmt.Sprintf("%d %s", probe.Status, probe.StatusText), Body: io.NopCloser(bytes.NewReader(probe.Raw)), Header: http.Header{}}, nil
	})
	ctx := t.Context()
	fail := func(err error) copilotResponseAnswer {
		message := err.Error()
		return copilotResponseAnswer{Err: &message, Syntax: errors.Is(err, errOAuthResponseNotJSON)}
	}
	var value any
	switch probe.Flow {
	case "device":
		device, err := startDeviceFlow(ctx, "github.com")
		if err != nil {
			return fail(err)
		}
		answer := map[string]any{"device_code": device.DeviceCode, "user_code": device.UserCode, "verification_uri": device.VerificationURI, "expires_in": device.ExpiresIn}
		if device.Interval != nil {
			answer["interval"] = *device.Interval
		}
		value = answer
	case "poll":
		interval := 5.0
		result, err := copilotPollStep(ctx, "github.com", &deviceCodeResponse{DeviceCode: "dc", Interval: &interval, ExpiresIn: 900})
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
		credential, err := refreshCopilotAccessToken(ctx, "gh-token", "")
		if err != nil {
			return fail(err)
		}
		value = map[string]any{"type": "oauth", "refresh": credential.Refresh, "access": credential.Access, "expires": credential.ExpiresMillis()}
	}
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return copilotResponseAnswer{OK: encoded}
}

func TestCopilotResponsesMatchPi(t *testing.T) {
	probes := copilotResponseProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/copilot_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "github-copilot.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []copilotResponseAnswer
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
		got := runPigCopilotResponse(t, probe)
		want := expected[i]
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = got.Syntax == want.Syntax && *got.Err == *want.Err
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
				render := func(a copilotResponseAnswer) string {
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
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes: %d succeed, %d fail", len(probes), succeeded, failed)
	if succeeded < 300 || failed < 1000 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}

// github-copilot.ts fetchJson reads an error body with response.text() (uncaught) and a success body with response.json(): neither survives a connection that ends early, even when the bytes that arrived are complete JSON.
func TestCopilotTruncatedBodyIsAnError(t *testing.T) {
	complete := `{"access_token":"t","token":"k","expires_at":1,"device_code":"d","user_code":"u","verification_uri":"https://g.example","expires_in":9}`
	for _, status := range []int{200, 500} {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d x", status), Body: &erroringBody{data: complete}, Header: http.Header{}}, nil
		})
		// Both reads reject with undici's TypeError "terminated" (probed on Node 24).
		if _, err := startDeviceFlow(t.Context(), "github.com"); errorText(err) != "terminated" {
			t.Errorf("status %d device: %v, want terminated", status, err)
		}
		if _, err := copilotPollStep(t.Context(), "github.com", &deviceCodeResponse{}); errorText(err) != "terminated" {
			t.Errorf("status %d poll: %v, want terminated", status, err)
		}
		if _, err := refreshCopilotAccessToken(t.Context(), "gh", ""); errorText(err) != "terminated" {
			t.Errorf("status %d token: %v, want terminated", status, err)
		}
	}
}

// piCopilotModelIDs asks Pi's driver for the ids of its GITHUB_COPILOT_MODELS catalog.
func piCopilotModelIDs(t *testing.T) []string {
	t.Helper()
	answers := runPiCopilotOracle(t, []copilotResponseProbe{{Flow: "ids"}})
	var ids []string
	if err := json.Unmarshal(answers[0].OK, &ids); err != nil || len(ids) == 0 {
		t.Fatalf("Pi model ids: %v %s", err, answers[0].OK)
	}
	return ids
}

func runPiCopilotOracle(t *testing.T, probes []copilotResponseProbe) []copilotResponseAnswer {
	t.Helper()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/copilot_responses.mjs", filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "github-copilot.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []copilotResponseAnswer
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	return answers
}

// copilotCatalogProbes serve fetchGitHubCopilotModels catalogs of every shape: ids Pi's catalog knows and does not, picker flags, policy states and tool-call support of every type, items and `data` of every JSON type, for the individual endpoint (policy fallback) and another one.
func copilotCatalogProbes(known []string) []copilotResponseProbe {
	random := rand.New(rand.NewSource(20261019))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	id := func() string {
		if random.Intn(3) == 0 {
			return pick(`"unknown-model"`, `5`, `null`, `""`, `"__proto__"`, `"constructor"`, `"toString"`)
		}
		return strconv.Quote(known[random.Intn(len(known))])
	}
	item := func() string {
		if random.Intn(12) == 0 {
			return pick(`null`, `5`, `"x"`, `[]`, `true`)
		}
		var parts []string
		parts = append(parts, `"id":`+id())
		if random.Intn(8) != 0 {
			parts = append(parts, `"model_picker_enabled":`+pick(`true`, `true`, `false`, `"true"`, `null`, `1`))
		}
		if random.Intn(4) != 0 {
			parts = append(parts, `"policy":`+pick(`{"state":"enabled"}`, `{"state":"disabled"}`, `{"state":"unconfigured"}`, `{"state":"unconfigured"}`, `{"state":"other"}`, `{"state":5}`, `{}`, `null`, `"enabled"`, `[]`))
		}
		if random.Intn(3) == 0 {
			parts = append(parts, `"capabilities":`+pick(`{"supports":{"tool_calls":false}}`, `{"supports":{"tool_calls":true}}`, `{"supports":{"tool_calls":0}}`, `{"supports":{"tool_calls":"false"}}`, `{"supports":{}}`, `{"supports":null}`, `{}`, `null`, `5`))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	tokens := []string{"", "tid=1;proxy-ep=proxy.individual.githubcopilot.com;", "tid=1;proxy-ep=proxy.enterprise.example;", "x"}
	var probes []copilotResponseProbe
	add := func(status int, body string) {
		probes = append(probes, copilotResponseProbe{Flow: "catalog", Status: status, StatusText: http.StatusText(status), Raw: []byte(body), Token: tokens[random.Intn(len(tokens))]})
	}
	for _, body := range []string{``, `{`, `not json`, `[]`, `5`, `null`, `{}`, `{"data":null}`, `{"data":{}}`, `{"data":"x"}`, `{"data":[]}`, `{"data":[null,5]}`, "\ufeff" + `{"data":[]}`, "\xff\xfe", `{"data":[{"id":"a\u2028é"}]}`} {
		for _, status := range []int{200, 201, 299, 300, 400, 401, 403, 404, 500, 503} {
			add(status, body)
		}
	}
	for range 2500 {
		var items []string
		for range random.Intn(7) {
			items = append(items, item())
		}
		body := `{"data":[` + strings.Join(items, ",") + `]}`
		if random.Intn(15) == 0 {
			body = pick(`{"data":`+item()+`}`, `[`+strings.Join(items, ",")+`]`, `{"models":[`+strings.Join(items, ",")+`]}`)
		}
		add([]int{200, 200, 200, 200, 400, 401, 500}[random.Intn(7)], body)
	}
	return probes
}

func TestCopilotCatalogMatchesPi(t *testing.T) {
	probes := copilotCatalogProbes(piCopilotModelIDs(t))
	expected := runPiCopilotOracle(t, probes)
	differing, succeeded, failed := 0, 0, 0
	for i, probe := range probes {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: probe.Status, Status: fmt.Sprintf("%d %s", probe.Status, probe.StatusText), Body: io.NopCloser(bytes.NewReader(probe.Raw)), Header: http.Header{}}, nil
		})
		var got copilotResponseAnswer
		catalog, err := fetchGitHubCopilotModels(t.Context(), probe.Token, "", copilotRetryPolicy{})
		if err != nil {
			message := err.Error()
			got = copilotResponseAnswer{Err: &message, Syntax: errors.Is(err, errOAuthResponseNotJSON)}
		} else {
			encoded, _ := json.Marshal(map[string]any{"availableModelIds": append([]string{}, catalog.AvailableModelIDs...), "policyModelIds": append([]string{}, catalog.PolicyModelIDs...)})
			got = copilotResponseAnswer{OK: encoded}
		}
		want := expected[i]
		same := (got.Err == nil) == (want.Err == nil)
		if same && got.Err != nil {
			same = got.Syntax == want.Syntax && *got.Err == *want.Err
		}
		if same && got.Err == nil {
			same = bytes.Equal(got.OK, want.OK)
		}
		if want.Err == nil {
			succeeded++
		} else {
			failed++
		}
		if !same {
			differing++
			if differing <= 8 {
				render := func(a copilotResponseAnswer) string {
					if a.Err != nil {
						return fmt.Sprintf("error (syntax %v): %s", a.Syntax, *a.Err)
					}
					return string(a.OK)
				}
				t.Errorf("token %q status %d body %.200q\n  Pi  %.300s\n  Pig %.300s", probe.Token, probe.Status, probe.Raw, render(want), render(got))
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d answers differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes: %d succeed, %d fail", len(probes), succeeded, failed)
	if succeeded < 1000 || failed < 80 {
		t.Errorf("probes are one-sided: %d succeed, %d fail", succeeded, failed)
	}
}

// github-copilot.ts returns a response from fetchWithRateLimitRetry before its body is read: fetchGitHubCopilotModels fails when reading it does, and a policy POST that is neither rate limited nor read still succeeds.
func TestCopilotTruncatedCatalogAndPolicyBodies(t *testing.T) {
	for _, status := range []int{200, 500} {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d x", status), Body: &erroringBody{data: `{"data":[]}`}, Header: http.Header{}}, nil
		})
		if _, err := fetchGitHubCopilotModels(t.Context(), "", "", copilotRetryPolicy{}); errorText(err) != "terminated" {
			t.Errorf("status %d catalog: %v, want terminated", status, err)
		}
	}
	for _, tc := range []struct {
		status      int
		enabled     bool
		stopBatch   bool
		description string
	}{{200, true, false, "a successful policy POST is never read"}, {500, false, false, "a failed policy POST is never read"}} {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Status: fmt.Sprintf("%d x", tc.status), Body: &erroringBody{data: `{}`}, Header: http.Header{}}, nil
		})
		enabled, stop, err := enableGitHubCopilotModel(t.Context(), "", "claude-sonnet-4.6", "")
		if err != nil || enabled != tc.enabled || stop != tc.stopBatch {
			t.Errorf("%s: enabled %v stop %v err %v", tc.description, enabled, stop, err)
		}
	}
}
