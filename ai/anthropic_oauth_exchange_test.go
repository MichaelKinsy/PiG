package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pi's waitForCallbackOrManualInput aborts the manual prompt in its own finally (.upstream/v0.99.1/packages/ai/src/auth/oauth/callback-server.ts:172-174), so the prompt is dismissed before loginAnthropic notifies exchange progress and awaits the exchange (anthropic.ts:184). Drive that boundary through the native Model Runtime, including persisted credentials and rejected exchanges.
func TestAnthropicNativeLoginCleansUpBeforeExchangeSettles(t *testing.T) {
	for _, winner := range []string{"manual", "browser"} {
		for _, outcome := range []string{"success", "failure", "cancel"} {
			t.Run(winner+"/"+outcome, func(t *testing.T) {
				host := isolateAnthropicCallbackHost(t)
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				promptStarted := make(chan struct{})
				promptExited := make(chan struct{})
				cleanedDuringExchange := make(chan struct{})
				releaseExchange := make(chan struct{})
				release := sync.OnceFunc(func() { close(releaseExchange) })
				defer release()
				var promptContext context.Context
				previous := http.DefaultClient
				http.DefaultClient = &http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
					<-promptStarted
					<-promptContext.Done()
					<-promptExited
					if request.Context().Err() != nil {
						t.Error("prompt cleanup waited for the token request deadline")
					}
					close(cleanedDuringExchange)
					select {
					case <-releaseExchange:
					case <-request.Context().Done():
					}
					if err := request.Context().Err(); err != nil {
						return nil, err
					}
					status, body := http.StatusOK, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`
					if outcome == "failure" {
						status, body = http.StatusUnauthorized, `{"error":"invalid_grant"}`
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				t.Cleanup(func() { http.DefaultClient = previous })

				store := NewInMemoryCredentialStore()
				old := Credential{Type: CredentialOAuth, Access: "old-access", Refresh: "old-refresh"}
				if _, err := store.Modify(ctx, "anthropic", func(*Credential) (*Credential, error) { return &old, nil }); err != nil {
					t.Fatal(err)
				}
				models := CreateModels(CreateModelsOptions{Credentials: store})
				auth, err := BuiltinProviderAuth("anthropic")
				if err != nil {
					t.Fatal(err)
				}
				models.SetProvider(&ModelsProvider{ID: "anthropic", Name: "Anthropic", Auth: auth})
				var callbackURL string
				var credential Credential
				var loginErr error
				loginDone := make(chan struct{})
				go func() {
					defer close(loginDone)
					credential, loginErr = models.Login(ctx, "anthropic", CredentialOAuth, AuthInteraction{
						Notify: func(event AuthEvent) {
							switch event := event.(type) {
							case AuthURLEvent:
								parsed, err := url.Parse(event.URL)
								if err != nil {
									t.Error(err)
									return
								}
								callbackURL = "http://" + net.JoinHostPort(host, "53692") + "/callback?code=browser-code&state=" + url.QueryEscape(parsed.Query().Get("state"))
							case AuthProgressEvent:
								<-promptStarted
								if promptContext.Err() == nil {
									t.Error("prompt still open at the exchange progress notification")
								}
							}
						},
						Prompt: func(promptCtx context.Context, prompt AuthPrompt) (string, error) {
							// Pi 1.0.0 asks for the login method first; the upstream tests answer browser login (.upstream/v1.0.0/packages/ai/test/anthropic-oauth.test.ts:195).
							if _, ok := prompt.(AuthSelectPrompt); ok {
								return AnthropicBrowserLoginMethod, nil
							}
							defer close(promptExited)
							promptContext = promptCtx
							close(promptStarted)
							if _, ok := prompt.(AuthManualCodePrompt); !ok {
								return "", errors.New("expected manual_code prompt")
							}
							if winner == "manual" {
								return "the-code", nil
							}
							request, err := http.NewRequestWithContext(promptCtx, http.MethodGet, callbackURL, nil)
							if err != nil {
								return "", err
							}
							response, err := (&http.Client{}).Do(request)
							if err != nil {
								return "", err
							}
							if err := response.Body.Close(); err != nil {
								return "", err
							}
							<-promptCtx.Done()
							return "", promptCtx.Err()
						},
					})
				}()
				defer func() {
					cancel()
					release()
					<-loginDone
					models.operations.Wait()
					store.operations.Wait()
				}()
				select {
				case <-cleanedDuringExchange:
				case <-loginDone:
					t.Fatalf("login returned before authorization cleanup: %v", loginErr)
				case <-ctx.Done():
					t.Fatal("authorization cleanup did not complete while exchange was pending")
				}
				select {
				case <-loginDone:
					t.Fatal("login returned before the token exchange settled")
				default:
				}
				if outcome == "cancel" {
					cancel()
				}
				release()
				<-loginDone
				models.operations.Wait()
				store.operations.Wait()
				if outcome == "success" {
					if loginErr != nil || credential.Access != "new-access" || credential.Refresh != "new-refresh" {
						t.Fatalf("credential=%+v error=%v", credential, loginErr)
					}
				} else if loginErr == nil || (outcome == "cancel" && !errors.Is(loginErr, context.Canceled)) || (outcome == "failure" && !strings.Contains(loginErr.Error(), "invalid_grant")) {
					t.Fatalf("outcome=%s error=%v", outcome, loginErr)
				}
				stored, err := store.Read(t.Context(), "anthropic")
				if err != nil {
					t.Fatal(err)
				}
				wantAccess := "old-access"
				if outcome == "success" {
					wantAccess = "new-access"
				}
				if stored == nil || stored.Access != wantAccess {
					t.Fatalf("stored=%+v want access=%s", stored, wantAccess)
				}
			})
		}
	}
}

func BenchmarkAnthropicNativeOAuthLogin(b *testing.B) {
	b.Setenv("PI_OAUTH_CALLBACK_HOST", anthropicCallbackTestHost())
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: responsesTestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))}, nil
	})}
	b.Cleanup(func() { http.DefaultClient = previous })
	store := NewInMemoryCredentialStore()
	models := CreateModels(CreateModelsOptions{Credentials: store})
	auth, err := BuiltinProviderAuth("anthropic")
	if err != nil {
		b.Fatal(err)
	}
	models.SetProvider(&ModelsProvider{ID: "anthropic", Name: "Anthropic", Auth: auth})
	b.Cleanup(func() {
		models.operations.Wait()
		store.operations.Wait()
	})
	interaction := AuthInteraction{Notify: func(AuthEvent) {}, Prompt: func(context.Context, AuthPrompt) (string, error) { return "the-code", nil }}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := models.Login(b.Context(), "anthropic", CredentialOAuth, interaction); err != nil {
			b.Fatal(err)
		}
	}
}
