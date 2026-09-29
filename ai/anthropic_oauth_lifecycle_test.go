package ai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// anthropic.ts:268-350 races callback/manual input and aborts the manual prompt in finally.
func TestAnthropicOAuthCallbackWinnerCancelsAndJoinsManualPrompt(t *testing.T) {
	calls := mockAnthropicOAuthToken(t, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, func(*http.Request, map[string]string) {})
	var callbackURL string
	exited := make(chan struct{})
	credential, err := LoginAnthropic(t.Context(), OAuthLoginCallbacks{OnAuth: func(info OAuthAuthInfo) {
		parsed, parseErr := url.Parse(info.URL)
		if parseErr != nil {
			t.Error(parseErr)
			return
		}
		redirect, parseErr := url.Parse(parsed.Query().Get("redirect_uri"))
		if parseErr != nil {
			t.Error(parseErr)
			return
		}
		redirect.Host = net.JoinHostPort(os.Getenv("PI_OAUTH_CALLBACK_HOST"), "53692")
		callbackURL = redirect.String() + "?code=callback-code&state=" + url.QueryEscape(parsed.Query().Get("state"))
	}, OnManualCodeInput: func() (string, error) { return "", errors.New("contextual prompt was not invoked") }, OnManualCodeInputContext: func(ctx context.Context) (string, error) {
		defer close(exited)
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, callbackURL, nil)
		if requestErr != nil {
			return "", requestErr
		}
		client := &http.Client{}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return "", requestErr
		}
		if closeErr := response.Body.Close(); closeErr != nil {
			return "", closeErr
		}
		<-ctx.Done()
		return "", ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Access != "access" || *calls != 1 {
		t.Fatalf("credential=%+v calls=%d", credential, *calls)
	}
	select {
	case <-exited:
	default:
		t.Fatal("login returned before the manual prompt exited")
	}
}

// packages/ai/src/auth/oauth/anthropic.ts:306-310 returns (rather than awaits) the exchange Promise, so finally dismisses the prompt while the token response is still pending.
func TestAnthropicOAuthDismissesPromptBeforeTokenResponse(t *testing.T) {
	var manualSignal context.Context
	mockAnthropicOAuthToken(t, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, func(request *http.Request, _ map[string]string) {
		if manualSignal == nil {
			t.Error("manual prompt context is missing")
			return
		}
		// Hold the token response until the prompt is dismissed. The request deadline bounds a deadlock; it is not an allowance for delayed cleanup.
		<-manualSignal.Done()
		if request.Context().Err() != nil {
			t.Error("manual prompt remained active until the token request deadline; expected dismissal before the token response")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	credential, err := LoginAnthropic(ctx, OAuthLoginCallbacks{OnAuth: func(OAuthAuthInfo) {}, OnManualCodeInputContext: func(ctx context.Context) (string, error) {
		manualSignal = ctx
		return "the-code", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Access != "access" {
		t.Fatalf("access=%q", credential.Access)
	}
}

func TestAnthropicOAuthManualFailureCancelsPromptContext(t *testing.T) {
	isolateAnthropicCallbackHost(t)
	rejected := errors.New("manual input rejected")
	var promptContext context.Context
	_, err := LoginAnthropic(t.Context(), OAuthLoginCallbacks{OnAuth: func(OAuthAuthInfo) {}, OnManualCodeInput: func() (string, error) { return "", rejected }, OnManualCodeInputContext: func(ctx context.Context) (string, error) { promptContext = ctx; return "", rejected }})
	if !errors.Is(err, rejected) {
		t.Fatalf("error=%v", err)
	}
	if promptContext == nil || promptContext.Err() != context.Canceled {
		t.Fatal("failed manual prompt context was not canceled")
	}
}
