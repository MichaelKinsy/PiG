package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestCopilotZeroAndOmittedPollingIntervals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval *float64
		wait     time.Duration
	}{{"explicit zero uses minimum", new(float64(0)), time.Second}, {"omitted uses RFC default", nil, 5 * time.Second}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := &copilotUpstreamFixture{}
				base := fixture.transport(t).Transport
				previous := http.DefaultClient
				http.DefaultClient = &http.Client{Transport: metaRoundTripper(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/login/device/code") {
						body := map[string]any{"device_code": "device-code", "user_code": "ABCD-EFGH", "verification_uri": "https://github.com/login/device", "expires_in": 60}
						if tc.interval != nil {
							body["interval"] = *tc.interval
						}
						return metaJSONResponse(200, body), nil
					}
					return base.RoundTrip(r)
				})}
				t.Cleanup(func() { http.DefaultClient = previous })
				start := time.Now()
				_, err := loginCopilotUpstream(t, func(OAuthDeviceCodeInfo) {})
				if err != nil || time.Since(start) != tc.wait {
					t.Fatalf("login=%v elapsed=%v want=%v", err, time.Since(start), tc.wait)
				}
			})
		})
	}
}

const copilotUpstreamAccess = "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;"
const copilotUpstreamModelsURL = "https://api.individual.githubcopilot.com/models"

type copilotUpstreamFixture struct {
	models            []any
	policy            func(string) (*http.Response, error)
	poll              func() (*http.Response, error)
	verification      string
	interval, expires int
	catalogCalls      int
}

func (f *copilotUpstreamFixture) transport(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: metaRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/login/device/code"):
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if r.Method != "POST" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || !strings.Contains(string(body), "client_id=") || !strings.Contains(string(body), "scope=read%3Auser") {
				t.Errorf("device request=%s %v %s", r.Method, r.Header, body)
			}
			uri := f.verification
			if uri == "" {
				uri = "https://github.com/login/device"
			}
			interval := f.interval
			if interval == 0 {
				interval = 1
			}
			expires := f.expires
			if expires == 0 {
				expires = 900
			}
			return metaJSONResponse(200, map[string]any{"device_code": "device-code", "user_code": "ABCD-EFGH", "verification_uri": uri, "interval": interval, "expires_in": expires}), nil
		case strings.HasSuffix(r.URL.Path, "/login/oauth/access_token"):
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if r.Method != "POST" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || !strings.Contains(string(body), "client_id=") || !strings.Contains(string(body), "device_code=device-code") || !strings.Contains(string(body), "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code") {
				t.Errorf("poll request=%s %v %s", r.Method, r.Header, body)
			}
			if f.poll != nil {
				return f.poll()
			}
			return metaJSONResponse(200, map[string]any{"access_token": "ghu_refresh_token"}), nil
		case strings.Contains(r.URL.Path, "/copilot_internal/v2/token"):
			return metaJSONResponse(200, map[string]any{"token": copilotUpstreamAccess, "expires_at": 9999999999}), nil
		case r.URL.String() == copilotUpstreamModelsURL:
			f.catalogCalls++
			models := f.models
			if models == nil {
				models = []any{}
			}
			return metaJSONResponse(200, map[string]any{"data": models}), nil
		case strings.HasPrefix(r.URL.String(), copilotUpstreamModelsURL+"/") && strings.HasSuffix(r.URL.Path, "/policy"):
			if f.policy != nil {
				return f.policy(strings.TrimSuffix(strings.TrimPrefix(r.URL.String(), copilotUpstreamModelsURL+"/"), "/policy"))
			}
		}
		t.Errorf("unexpected URL %s", r.URL)
		return metaJSONResponse(400, nil), nil
	})}
}
func withCopilotUpstreamFixture(t *testing.T, f *copilotUpstreamFixture) {
	t.Helper()
	previous := http.DefaultClient
	http.DefaultClient = f.transport(t)
	t.Cleanup(func() { http.DefaultClient = previous })
}
func loginCopilotUpstream(t *testing.T, notify func(OAuthDeviceCodeInfo)) (OAuthCredentials, error) {
	t.Helper()
	return (copilotOAuthRegistryProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{OnPrompt: func(OAuthPrompt) (string, error) { return "", nil }, OnDeviceCode: notify})
}
func copilotTestModelIDs(t *testing.T) []string {
	t.Helper()
	models := ListModels("github-copilot")
	if len(models) < 3 {
		t.Fatal("upstream fixture needs first three catalog models")
	}
	return []string{models[0].ID, models[1].ID, models[2].ID}
}
func copilotAccountRow(id string, picker bool, policy string, tools *bool) map[string]any {
	row := map[string]any{"id": id, "model_picker_enabled": picker}
	if policy != "" {
		row["policy"] = map[string]any{"state": policy}
	}
	if tools != nil {
		row["capabilities"] = map[string]any{"supports": map[string]any{"tool_calls": *tools}}
	}
	return row
}

// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:263
func TestCopilotReportsDeviceCodeDetailsUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Date(2026, time.March, 9, 0, 0, 0, 0, time.UTC)
		time.Sleep(start.Sub(time.Now()))
		f := &copilotUpstreamFixture{}
		withCopilotUpstreamFixture(t, f)
		var events []OAuthDeviceCodeInfo
		_, err := loginCopilotUpstream(t, func(info OAuthDeviceCodeInfo) {
			events = append(events, info)
			if !time.Now().Equal(start) {
				t.Errorf("device callback time=%s", time.Now())
			}
		})
		if err != nil || !reflect.DeepEqual(events, []OAuthDeviceCodeInfo{{UserCode: "ABCD-EFGH", VerificationURI: "https://github.com/login/device", IntervalSeconds: 1, ExpiresInSeconds: 900}}) {
			t.Fatalf("events=%#v error=%v", events, err)
		}
	})
}

// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:322
func TestCopilotUpdatesOnlyKnownToolCapableUnconfiguredPoliciesUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ids := copilotTestModelIDs(t)
		var policies []string
		f := &copilotUpstreamFixture{models: []any{copilotAccountRow(ids[0], true, "enabled", new(true)), copilotAccountRow(ids[1], true, "unconfigured", new(true)), copilotAccountRow("remote-only-model", true, "unconfigured", new(true)), copilotAccountRow(ids[2], true, "unconfigured", new(false))}, policy: func(id string) (*http.Response, error) {
			policies = append(policies, id)
			return cannedResp(200, ""), nil
		}}
		withCopilotUpstreamFixture(t, f)
		_, err := loginCopilotUpstream(t, func(OAuthDeviceCodeInfo) {})
		if err != nil || f.catalogCalls != 1 || !reflect.DeepEqual(policies, []string{ids[1]}) {
			t.Fatalf("policies=%v catalog=%d error=%v", policies, f.catalogCalls, err)
		}
	})
}

// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:380
func TestCopilotRetriesThrottledPolicyAfterRetryAfterUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		id := copilotTestModelIDs(t)[0]
		var times []time.Duration
		start := time.Now()
		f := &copilotUpstreamFixture{models: []any{copilotAccountRow(id, true, "unconfigured", nil)}, policy: func(string) (*http.Response, error) {
			times = append(times, time.Since(start))
			if len(times) == 1 {
				response := metaJSONResponse(429, map[string]any{"error": "too many requests"})
				response.Header.Set("Retry-After", "1")
				return response, nil
			}
			return cannedResp(200, ""), nil
		}}
		withCopilotUpstreamFixture(t, f)
		_, err := loginCopilotUpstream(t, func(OAuthDeviceCodeInfo) {})
		if err != nil || !reflect.DeepEqual(times, []time.Duration{time.Second, 2 * time.Second}) {
			t.Fatalf("policy times=%v error=%v", times, err)
		}
	})
}

// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:412
func TestCopilotContinuesPolicyUpdatesAfterTransportFailureUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ids := copilotTestModelIDs(t)[:2]
		var policies []string
		f := &copilotUpstreamFixture{models: []any{copilotAccountRow(ids[0], true, "unconfigured", nil), copilotAccountRow(ids[1], true, "unconfigured", nil)}, policy: func(id string) (*http.Response, error) {
			policies = append(policies, id)
			if len(policies) == 1 {
				return nil, errors.New("fetch failed")
			}
			return cannedResp(200, ""), nil
		}}
		withCopilotUpstreamFixture(t, f)
		_, err := loginCopilotUpstream(t, func(OAuthDeviceCodeInfo) {})
		if err != nil || !reflect.DeepEqual(policies, ids) {
			t.Fatalf("policies=%v error=%v", policies, err)
		}
	})
}

// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:440
func TestCopilotStopsPoliciesWhenDelayExceedsBudgetUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ids := copilotTestModelIDs(t)
		var policies []string
		f := &copilotUpstreamFixture{models: []any{copilotAccountRow(ids[0], true, "unconfigured", nil), copilotAccountRow(ids[1], true, "unconfigured", nil)}, policy: func(id string) (*http.Response, error) {
			policies = append(policies, id)
			response := metaJSONResponse(429, map[string]any{"error": "too many requests"})
			response.Header.Set("Retry-After", "5")
			return response, nil
		}}
		withCopilotUpstreamFixture(t, f)
		store := NewInMemoryCredentialStore()
		models := CreateModels(CreateModelsOptions{Credentials: store})
		models.SetProvider(oauthAuthCatalogProvider(t, "github-copilot"))
		got, err := models.Login(t.Context(), "github-copilot", CredentialOAuth, AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { return "", nil }, Notify: func(AuthEvent) {}})
		if err != nil || got.Type != CredentialOAuth || got.Access != copilotUpstreamAccess || !reflect.DeepEqual(policies, []string{ids[0]}) {
			t.Fatalf("credential=%#v policies=%v error=%v", got, policies, err)
		}
		stored, err := store.Read(t.Context(), "github-copilot")
		if err != nil || !reflect.DeepEqual(stored, &got) {
			t.Fatalf("stored=%#v error=%v", stored, err)
		}
	})
}

func TestCopilotVerificationURIUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		bad             bool
	}{
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:477
		{"rejects a non-http(s) verification_uri before it reaches onDeviceCode", "$(id>/tmp/pwned)", "", true},
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:507
		{"normalizes verification_uri before it reaches onDeviceCode", "https://github.com/login/\x1b]8;;evil", "https://github.com/login/%1B]8;;evil", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &copilotUpstreamFixture{verification: tc.raw}
				withCopilotUpstreamFixture(t, f)
				var events []OAuthDeviceCodeInfo
				_, err := loginCopilotUpstream(t, func(info OAuthDeviceCodeInfo) { events = append(events, info) })
				if tc.bad {
					if err == nil || !strings.Contains(err.Error(), "Untrusted verification_uri") || len(events) != 0 {
						t.Fatalf("events=%v error=%v", events, err)
					}
					return
				}
				if err != nil || !reflect.DeepEqual(events, []OAuthDeviceCodeInfo{{UserCode: "ABCD-EFGH", VerificationURI: tc.want, IntervalSeconds: 1, ExpiresInSeconds: 900}}) {
					t.Fatalf("events=%#v error=%v", events, err)
				}
			})
		})
	}
}

func TestCopilotPollTimingUpstream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expires int
		replies []map[string]any
		want    []time.Duration
		message string
	}{
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:572
		{"waits before polling and increases the interval after slow_down", 900, []map[string]any{{"error": "authorization_pending", "error_description": "pending"}, {"error": "slow_down", "error_description": "slow down", "interval": 7}, {"access_token": "ghu_refresh_token"}}, []time.Duration{5 * time.Second, 10 * time.Second, 17 * time.Second}, ""},
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:676
		{"times out after repeated slow_down responses", 25, []map[string]any{{"error": "slow_down", "error_description": "slow down"}, {"error": "slow_down", "error_description": "still too fast"}, {"error": "authorization_pending", "error_description": "pending"}}, []time.Duration{5 * time.Second, 15 * time.Second}, "Device flow timed out after one or more slow_down responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Date(2026, time.March, 9, 0, 0, 0, 0, time.UTC)
				time.Sleep(start.Sub(time.Now()))
				var times []time.Duration
				f := &copilotUpstreamFixture{interval: 5, expires: tc.expires, poll: func() (*http.Response, error) {
					index := len(times)
					times = append(times, time.Since(start))
					if index >= len(tc.replies) {
						t.Errorf("extra token poll")
						return metaJSONResponse(400, nil), nil
					}
					return metaJSONResponse(200, tc.replies[index]), nil
				}}
				withCopilotUpstreamFixture(t, f)
				_, err := loginCopilotUpstream(t, func(OAuthDeviceCodeInfo) {})
				if !reflect.DeepEqual(times, tc.want) {
					t.Errorf("poll times=%v want %v", times, tc.want)
				}
				if tc.message != "" {
					if err == nil || !strings.Contains(err.Error(), tc.message) {
						t.Fatalf("error=%v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
