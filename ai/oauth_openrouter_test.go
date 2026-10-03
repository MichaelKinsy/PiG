package ai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// upstream: packages/ai/test/openrouter-oauth.test.ts:29,37.
func TestOpenRouterOAuthProvidersUpstream(t *testing.T) {
	auth, err := BuiltinProviderAuth("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	// .upstream/v0.99.1/packages/ai/test/openrouter-oauth.test.ts:29
	t.Run("is exposed alongside API-key auth", func(t *testing.T) {
		if auth.APIKey == nil || auth.OAuth == nil || auth.OAuth.LoginLabel != "Sign in with OpenRouter" {
			t.Fatalf("OpenRouter auth = %#v", auth)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/openrouter-oauth.test.ts:34
	t.Run("resolves the same stored OAuth key for chat and image models", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		defer credentials.operations.Wait()
		_, err := credentials.Modify(t.Context(), "openrouter", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialOAuth, Access: "sk-or-stored", Refresh: "", Expires: 9007199254740991}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(openRouterTestProvider(auth))
		chat := models.GetModels("openrouter")
		images := models.GetModelsOfType(ModelTypeImage, "openrouter")
		if len(chat) == 0 || len(images) == 0 {
			t.Fatalf("chat=%d image=%d models", len(chat), len(images))
		}
		for _, model := range []AnyModel{chat[0], images[0]} {
			result, err := models.GetModelAuth(t.Context(), model)
			if err != nil || result == nil || result.Auth.APIKey != "sk-or-stored" {
				t.Fatalf("stored OpenRouter OAuth key for %s = %#v, %v", GetModelType(model), result, err)
			}
		}
	})
}

// openRouterTestProvider is the OpenRouter provider with its chat and image models under one credential.
func openRouterTestProvider(auth ProviderAuth) *ModelsProvider {
	var list []AnyModel
	for _, m := range ListModels("openrouter") {
		list = append(list, m.ToModel())
	}
	for _, image := range GetImageModels(ProviderImagesOpenRouter) {
		list = append(list, &image)
	}
	return CreateProvider(CreateProviderOptions{ID: "openrouter", Name: new("OpenRouter"), Auth: auth, Models: list,
		Images: ProviderImageAPIMap{APIImagesOpenRouter: {GenerateImages: func(ctx context.Context, model *ImageModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
			return GenerateImagesOpenRouter(ctx, *model, request, options), nil
		}}}})
}

type openRouterHTTPResult struct {
	status int
	err    error
}

func requestOpenRouterCallback(ctx context.Context, rawURL string) openRouterHTTPResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return openRouterHTTPResult{err: err}
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return openRouterHTTPResult{err: err}
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	return openRouterHTTPResult{status: response.StatusCode, err: errors.Join(readErr, closeErr)}
}

func openRouterCallbackFromAuth(t *testing.T, event AuthURLEvent, code string) (*url.URL, *url.URL) {
	t.Helper()
	authorize, err := url.Parse(event.URL)
	if err != nil {
		t.Fatal(err)
	}
	callback, err := url.Parse(authorize.Query().Get("callback_url"))
	if err != nil {
		t.Fatal(err)
	}
	if code != "" {
		query := callback.Query()
		query.Set("code", code)
		callback.RawQuery = query.Encode()
	}
	return authorize, callback
}

// upstream: packages/ai/test/openrouter-oauth.test.ts:55. The browser wins the race and aborts the separate pending manual prompt before login returns.
func TestOpenRouterOAuthPKCEUpstream(t *testing.T) {
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "")
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	manualStarted := make(chan struct{})
	manualFinished := make(chan struct{})
	var manualCtx context.Context
	var exchangeBody map[string]string
	var exchanges atomic.Int32
	withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
		<-manualStarted
		exchanges.Add(1)
		if err := json.NewDecoder(r.Body).Decode(&exchangeBody); err != nil {
			return nil, err
		}
		return cannedResp(200, `{"key":"sk-or-test"}`), nil
	})
	var authorize, callback *url.URL
	response := make(chan openRouterHTTPResult, 1)
	auth, _ := OAuthProviderAuth("openrouter")
	credential, err := auth.Login(ctx, AuthInteraction{
		Prompt: func(promptCtx context.Context, prompt AuthPrompt) (string, error) {
			manualCtx = promptCtx
			if _, ok := prompt.(AuthManualCodePrompt); !ok {
				t.Errorf("unexpected prompt: %#v", prompt)
			}
			close(manualStarted)
			<-promptCtx.Done()
			close(manualFinished)
			return "", context.Cause(promptCtx)
		},
		Notify: func(event AuthEvent) {
			if event, ok := event.(AuthURLEvent); ok {
				authorize, callback = openRouterCallbackFromAuth(t, event, "authorization-code")
				workers.Go(func() { response <- requestOpenRouterCallback(ctx, callback.String()) })
			}
		},
	}, LoginOptions{})
	want := Credential{Type: CredentialOAuth, Access: "sk-or-test", Refresh: "", Expires: 9007199254740991}
	if err != nil || !reflect.DeepEqual(credential, want) {
		t.Errorf("login = %#v, %v; want %#v", credential, err, want)
	}
	if result := <-response; result.err != nil || result.status != http.StatusOK {
		t.Errorf("callback = %+v", result)
	}
	<-manualStarted
	if manualCtx.Err() == nil {
		t.Error("browser login returned without aborting the pending manual prompt")
	}
	if ctx.Err() != nil {
		t.Error("browser login cancelled the caller instead of the prompt")
	}
	select {
	case <-manualFinished:
	default:
		t.Error("browser login returned before the cancelled prompt finished")
	}
	cancel()
	<-manualFinished
	if authorize.Scheme != "https" || authorize.Host != "openrouter.ai" || authorize.Path != "/auth" || authorize.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("authorize URL = %s", authorize)
	}
	if callback.Hostname() != "127.0.0.1" || !regexp.MustCompile(`^/oauth/callback/[0-9a-f-]+$`).MatchString(callback.Path) {
		t.Errorf("callback URL = %s", callback)
	}
	if exchangeBody["code"] != "authorization-code" || exchangeBody["code_challenge_method"] != "S256" || exchangeBody["code_verifier"] == "" {
		t.Errorf("exchange body = %#v", exchangeBody)
	}
	digest := sha256.Sum256([]byte(exchangeBody["code_verifier"]))
	if authorize.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) || exchanges.Load() != 1 {
		t.Errorf("challenge or exchanges differ: body=%#v exchanges=%d", exchangeBody, exchanges.Load())
	}
}

// upstream: packages/ai/test/openrouter-oauth.test.ts:110,167.
func TestOpenRouterOAuthCallbackErrorsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, body, code, want string
		status                 int
	}{
		{"reports token exchange failures through both the callback page and login", `{"error":{"message":"invalid code"}}`, "bad-code", "OpenRouter OAuth key exchange failed (HTTP 403): invalid code", 403},
		{"rejects a successful response that does not contain a key", `{"user_id":"user-1"}`, "code-without-key", `OpenRouter OAuth response carries no "key"`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withOpenRouterToken(t, func(*http.Request) (*http.Response, error) { return cannedResp(tc.status, tc.body), nil })
			ctx, cancel := context.WithCancel(t.Context())
			var workers sync.WaitGroup
			defer workers.Wait()
			defer cancel()
			response := make(chan openRouterHTTPResult, 1)
			auth, _ := OAuthProviderAuth("openrouter")
			_, err := auth.Login(ctx, AuthInteraction{
				Prompt: func(ctx context.Context, _ AuthPrompt) (string, error) { <-ctx.Done(); return "", context.Cause(ctx) },
				Notify: func(event AuthEvent) {
					if event, ok := event.(AuthURLEvent); ok {
						_, callback := openRouterCallbackFromAuth(t, event, tc.code)
						workers.Go(func() { response <- requestOpenRouterCallback(ctx, callback.String()) })
					}
				},
			}, LoginOptions{})
			if err == nil || err.Error() != tc.want {
				t.Errorf("login error = %v, want %s", err, tc.want)
			}
			if result := <-response; result.err != nil || result.status != http.StatusBadGateway {
				t.Errorf("callback = %+v", result)
			}
		})
	}
}

// upstream: packages/ai/test/openrouter-oauth.test.ts:132.
func TestOpenRouterOAuthOneShotUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	exchangeStarted := make(chan struct{})
	completeExchange := make(chan struct{})
	defer close(completeExchange)
	var exchanges atomic.Int32
	withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
		if exchanges.Add(1) == 1 {
			close(exchangeStarted)
		}
		<-completeExchange
		return cannedResp(200, `{"key":"sk-or-test"}`), nil
	})
	var callbackResult openRouterHTTPResult
	var secondResult openRouterHTTPResult
	auth, _ := OAuthProviderAuth("openrouter")
	credential, err := auth.Login(ctx, AuthInteraction{
		Prompt: func(ctx context.Context, _ AuthPrompt) (string, error) { <-ctx.Done(); return "", context.Cause(ctx) },
		Notify: func(event AuthEvent) {
			if event, ok := event.(AuthURLEvent); ok {
				_, callback := openRouterCallbackFromAuth(t, event, "authorization-code")
				workers.Go(func() { callbackResult = requestOpenRouterCallback(ctx, callback.String()) })
				workers.Go(func() {
					<-exchangeStarted
					secondResult = requestOpenRouterCallback(ctx, callback.String())
					if exchanges.Load() != 1 {
						t.Errorf("exchanges before release = %d, want 1", exchanges.Load())
					}
					completeExchange <- struct{}{}
				})
			}
		},
	}, LoginOptions{})
	workers.Wait()
	if err != nil || credential.Access != "sk-or-test" || exchanges.Load() != 1 {
		t.Errorf("login = %#v, %v; exchanges=%d", credential, err, exchanges.Load())
	}
	if callbackResult.err != nil || callbackResult.status != 200 || secondResult.err != nil || secondResult.status != 409 {
		t.Errorf("first callback = %+v; second callback = %+v", callbackResult, secondResult)
	}
}

// upstream: packages/ai/test/openrouter-oauth.test.ts:189,222,242,258.
func TestOpenRouterOAuthManualUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		promptErr         error
		redirect          bool
	}{
		{"mints a key from a pasted redirect URL when the loopback callback never arrives", "manual-code", "", nil, true},
		{"accepts a bare authorization code from the manual prompt", "  manual-code  ", "", nil, false},
		{"fails login when the manual prompt is cancelled", "", "Login cancelled", errors.New("Login cancelled"), false},
		{"rejects empty manual input without exchanging a code", "   ", "Missing authorization code", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var exchangeBody map[string]string
			var exchanges atomic.Int32
			withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
				exchanges.Add(1)
				if err := json.NewDecoder(r.Body).Decode(&exchangeBody); err != nil {
					return nil, err
				}
				return cannedResp(200, `{"key":"sk-or-manual"}`), nil
			})
			var callback *url.URL
			auth, _ := OAuthProviderAuth("openrouter")
			credential, err := auth.Login(t.Context(), AuthInteraction{
				Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
					if _, ok := prompt.(AuthManualCodePrompt); !ok {
						t.Errorf("unexpected prompt = %#v", prompt)
					}
					if tc.redirect {
						return callback.String() + "?code=" + tc.input, nil
					}
					return tc.input, tc.promptErr
				},
				Notify: func(event AuthEvent) {
					if event, ok := event.(AuthURLEvent); ok {
						_, callback = openRouterCallbackFromAuth(t, event, "")
					}
				},
			}, LoginOptions{})
			if tc.want != "" {
				if err == nil || err.Error() != tc.want || exchanges.Load() != 0 {
					t.Fatalf("login error = %v, exchanges = %d; want %s without exchange", err, exchanges.Load(), tc.want)
				}
				return
			}
			want := Credential{Type: CredentialOAuth, Access: "sk-or-manual", Refresh: "", Expires: 9007199254740991}
			if err != nil || !reflect.DeepEqual(credential, want) || exchanges.Load() != 1 || exchangeBody["code"] != "manual-code" || exchangeBody["code_challenge_method"] != "S256" {
				t.Fatalf("login = %#v, %v; body=%#v exchanges=%d", credential, err, exchangeBody, exchanges.Load())
			}
		})
	}
}

// upstream: packages/ai/test/openrouter-oauth.test.ts:272,305.
func TestOpenRouterOAuthCancelledCallbackUpstream(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("PI_OAUTH_CALLBACK_HOST", host)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var callback *url.URL
			manualFinished := make(chan struct{})
			_, err := LoginOpenRouter(ctx, OAuthLoginCallbacks{
				OnManualCodeInputContext: func(ctx context.Context) (string, error) {
					defer close(manualFinished)
					<-ctx.Done()
					return "", context.Cause(ctx)
				},
				OnAuth: func(info OAuthAuthInfo) {
					_, callback = openRouterCallbackFromAuth(t, AuthURLEvent(info), "")
					cancel()
				},
			})
			if err == nil || err.Error() != "Login cancelled" || callback == nil || callback.Hostname() != host {
				t.Fatalf("login error = %v, callback = %v", err, callback)
			}
			select {
			case <-manualFinished:
			default:
				t.Error("cancelled login retained its manual prompt")
			}
			// Use a fresh context so a closed listener, not a cancelled request, proves cleanup.
			if result := requestOpenRouterCallback(t.Context(), callback.String()); result.err == nil {
				t.Fatalf("callback listener survived cancellation: %+v", result)
			}
		})
	}
}

// upstream: packages/ai/test/openrouter-oauth.test.ts:290.
func TestOpenRouterOAuthAlreadyCancelledUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := LoginOpenRouter(ctx, OAuthLoginCallbacks{
		OnAuth:            func(OAuthAuthInfo) { t.Error("Cancelled login must not emit events") },
		OnProgress:        func(string) { t.Error("Cancelled login must not emit events") },
		OnManualCodeInput: func() (string, error) { t.Error("Cancelled login must not prompt"); return "", nil },
	})
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("login error = %v", err)
	}
}

func BenchmarkOpenRouterOAuthManualLogin(b *testing.B) {
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: orRoundTripper{handler: func(*http.Request) (*http.Response, error) {
		return cannedResp(200, `{"key":"sk-or-manual"}`), nil
	}}}
	b.Cleanup(func() { http.DefaultClient = previous })
	b.ReportAllocs()
	for b.Loop() {
		credential, err := LoginOpenRouter(b.Context(), OAuthLoginCallbacks{OnManualCodeInputContext: func(context.Context) (string, error) { return "manual-code", nil }})
		if err != nil || credential.Access != "sk-or-manual" {
			b.Fatalf("login = %#v, %v", credential, err)
		}
	}
}

// orRoundTripper intercepts only the OpenRouter token endpoint, letting
// loopback callback requests reach the real transport.
type orRoundTripper struct {
	handler func(*http.Request) (*http.Response, error)
}

func (rt orRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "openrouter.ai" {
		return rt.handler(r)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func withOpenRouterToken(t *testing.T, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	prev := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: orRoundTripper{handler: handler}}
	t.Cleanup(func() { http.DefaultClient = prev })
}

func cannedResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestParseOpenRouterAuthorizationInput(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"redirect_url", "http://127.0.0.1:8976/oauth/callback/x?code=abc123&state=y", "abc123"},
		{"query_fragment", "code=fromquery&extra=1", "fromquery"},
		{"bare_code", "just-a-code", "just-a-code"},
		{"empty", "   ", ""},
		{"url_without_code", "https://openrouter.ai/auth?state=nope", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseOpenRouterAuthorizationInput(c.in); got != c.want {
				t.Fatalf("parse(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestOpenRouterPKCEChallengeIsS256(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(pkce.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pkce.Challenge != want {
		t.Fatalf("PKCE challenge is not S256(verifier): got %q want %q", pkce.Challenge, want)
	}
}

func TestOpenRouterExchangeSuccess(t *testing.T) {
	var gotBody map[string]string
	withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != openRouterTokenURL {
			t.Fatalf("unexpected exchange request %s %s", r.Method, r.URL)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		return cannedResp(200, `{"key":"sk-or-permanent"}`), nil
	})

	creds, err := exchangeOpenRouterCode(t.Context(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if creds.Access != "sk-or-permanent" || creds.Refresh != "" {
		t.Fatalf("creds = %+v", creds)
	}
	if creds.Expires != openRouterMaxSafeInteger {
		t.Fatalf("expires = %d, want MAX_SAFE_INTEGER %d", creds.Expires, openRouterMaxSafeInteger)
	}
	if gotBody["code"] != "the-code" || gotBody["code_verifier"] != "the-verifier" || gotBody["code_challenge_method"] != "S256" {
		t.Fatalf("PKCE exchange body = %+v", gotBody)
	}
}

func TestOpenRouterExchangeErrorBody(t *testing.T) {
	cases := []struct {
		name, body, wantSub string
		status              int
	}{
		{"error_object_message", `{"error":{"message":"bad grant"}}`, "bad grant", 400},
		{"error_description", `{"error_description":"expired code"}`, "expired code", 403},
		{"message_field", `{"message":"nope"}`, "nope", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
				return cannedResp(c.status, c.body), nil
			})
			_, err := exchangeOpenRouterCode(t.Context(), "c", "v")
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.wantSub) || !strings.Contains(err.Error(), "key exchange failed") {
				t.Fatalf("error = %q, want detail %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestOpenRouterExchangeMissingKey(t *testing.T) {
	withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
		return cannedResp(200, `{"not_a_key":"x"}`), nil
	})
	_, err := exchangeOpenRouterCode(t.Context(), "c", "v")
	if err == nil || !strings.Contains(err.Error(), `carries no "key"`) {
		t.Fatalf("expected no-key error, got %v", err)
	}
}

func TestOpenRouterExchangeInvalidJSON(t *testing.T) {
	withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
		return cannedResp(200, `not json at all`), nil
	})
	_, err := exchangeOpenRouterCode(t.Context(), "c", "v")
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid-JSON error, got %v", err)
	}
}

func TestOpenRouterLoginManualFallback(t *testing.T) {
	withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		return cannedResp(200, `{"key":"key-for-`+body["code"]+`"}`), nil
	})

	creds, err := LoginOpenRouter(t.Context(), OAuthLoginCallbacks{
		OnAuth: func(OAuthAuthInfo) {}, // no browser callback arrives
		OnManualCodeInput: func() (string, error) {
			return "https://x/cb?code=MANUAL", nil
		},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if creds.Access != "key-for-MANUAL" {
		t.Fatalf("manual fallback did not win: %+v", creds)
	}
}

// TestOpenRouterClaimedCallbackWinsOverManual drives a real loopback callback
// that claims and completes the exchange before manual paste resolves; the
// browser credential must win.
func TestOpenRouterClaimedCallbackWinsOverManual(t *testing.T) {
	withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		return cannedResp(200, `{"key":"key-for-`+body["code"]+`"}`), nil
	})

	creds, err := LoginOpenRouter(t.Context(), OAuthLoginCallbacks{
		OnAuth: func(info OAuthAuthInfo) {
			// Synchronously complete the browser callback before manual runs.
			u, perr := url.Parse(info.URL)
			if perr != nil {
				t.Errorf("authorize url: %v", perr)
				return
			}
			cbURL := u.Query().Get("callback_url")
			resp, gerr := http.Get(cbURL + "?code=BROWSER")
			if gerr != nil {
				t.Errorf("browser callback: %v", gerr)
				return
			}
			_ = resp.Body.Close()
		},
		OnManualCodeInput: func() (string, error) {
			return "code=MANUAL", nil
		},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if creds.Access != "key-for-BROWSER" {
		t.Fatalf("claimed callback did not win over manual: %+v", creds)
	}
}

func TestOpenRouterRefreshKeepsPermanentKey(t *testing.T) {
	// An OpenRouter API key does not expire and has no refresh token.
	creds := OAuthCredentials{Access: "sk", Expires: openRouterMaxSafeInteger}
	refreshed, err := OpenRouterOAuthProvider{}.RefreshToken(creds)
	if err != nil || !reflect.DeepEqual(refreshed, creds) {
		t.Fatalf("refresh changed a permanent key: %+v %v", refreshed, err)
	}
}

// TestOpenRouterRegisteredForLogin proves openrouter is in the OAuth registry so
// it appears in /login, matching upstream (providers/openrouter.ts declares
// auth.oauth). Before this it was API-key-only.
func TestOpenRouterRegisteredForLogin(t *testing.T) {
	p, ok := GetOAuthProvider("openrouter")
	if !ok {
		t.Fatal("openrouter OAuth provider not registered")
	}
	if p.ID() != "openrouter" || !p.UsesCallbackServer() {
		t.Fatalf("unexpected provider %+v", p)
	}
	found := false
	for _, q := range GetOAuthProviders() {
		if q.ID() == "openrouter" {
			found = true
		}
	}
	if !found {
		t.Fatal("openrouter missing from GetOAuthProviders (the /login source)")
	}
}
