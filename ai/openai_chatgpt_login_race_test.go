package ai

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
)

// openai-chatgpt.ts loginOpenAIChatGPT races the callback server's result against the pasted code (Promise.race), aborts the manual prompt and closes the
// server in finally, and reports the signal's abort as "Login cancelled". Whoever settles the login, the prompt has returned and the callback port is free
// again when it does.
func TestOpenAIChatGPTLoginEndsWithThePromptSettledAndTheCallbackServerClosed(t *testing.T) {
	assertPortFree := func(t *testing.T) {
		t.Helper()
		listener, err := net.Listen("tcp", net.JoinHostPort(oauthCallbackHost(), strconv.Itoa(chatgptCallbackPort)))
		if err != nil {
			t.Fatalf("the callback server still holds its port after the login ended: %v", err)
		}
		_ = listener.Close()
	}
	t.Run("cancelled while waiting", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		endpoint := stubChatGPTTokenEndpoint(t, chatgptTokenResponse(chatgptRequiredScope))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var promptReturned atomic.Bool
		interaction := AuthInteraction{
			Notify: func(event AuthEvent) {
				if _, ok := event.(AuthURLEvent); ok {
					cancel()
				}
			},
			Prompt: func(promptCtx context.Context, _ AuthPrompt) (string, error) {
				<-promptCtx.Done()
				promptReturned.Store(true)
				return "", promptCtx.Err()
			},
		}
		_, err := oauth.Login(ctx, interaction, chatgptRaceLoginOptions)
		if err == nil || err.Error() != "Login cancelled" {
			t.Fatalf("error = %v, want Login cancelled", err)
		}
		if !promptReturned.Load() {
			t.Fatal("the login returned while the manual prompt was still pending")
		}
		if len(endpoint.bodies) != 0 {
			t.Fatalf("token requests = %d, want none", len(endpoint.bodies))
		}
		assertPortFree(t)
	})
	t.Run("pasted code settles first", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		stubChatGPTTokenEndpoint(t, chatgptTokenResponse(chatgptRequiredScope))
		var promptContext atomic.Pointer[context.Context]
		inner := chatgptLoginInteraction("oaiapp_issued", nil)
		interaction := inner
		interaction.Prompt = func(ctx context.Context, prompt AuthPrompt) (string, error) {
			promptContext.Store(&ctx)
			return inner.Prompt(ctx, prompt)
		}
		if _, err := oauth.Login(t.Context(), interaction, chatgptRaceLoginOptions); err != nil {
			t.Fatal(err)
		}
		if ctx := promptContext.Load(); ctx == nil || (*ctx).Err() == nil {
			t.Fatal("the manual prompt's context was not cancelled when the login settled")
		}
		assertPortFree(t)
	})
	t.Run("prompt rejection", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		stubChatGPTTokenEndpoint(t, chatgptTokenResponse(chatgptRequiredScope))
		sentinel := errors.New("prompt failed")
		interaction := AuthInteraction{
			Notify: func(AuthEvent) {},
			Prompt: func(context.Context, AuthPrompt) (string, error) { return "", sentinel },
		}
		if _, err := oauth.Login(t.Context(), interaction, chatgptRaceLoginOptions); !errors.Is(err, sentinel) {
			t.Fatalf("error = %v, want the prompt's rejection", err)
		}
		assertPortFree(t)
	})
}

var chatgptRaceLoginOptions = LoginOptions{GetDeviceID: func() string { return "123e4567-e89b-12d3-a456-426614174000" }}
