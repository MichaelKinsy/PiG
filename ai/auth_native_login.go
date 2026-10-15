package ai

// Adapts packages/ai/src/auth/types.ts login interactions to the Go OAuth callback contract, preserving caller and per-prompt cancellation contexts.

import (
	"context"
	"net/url"
	"sync"
)

func oauthNativeLogin(provider OAuthProviderInterface) func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
	return func(ctx context.Context, interaction AuthInteraction, options LoginOptions) (Credential, error) {
		if ctx.Err() != nil {
			return Credential{}, context.Cause(ctx)
		}
		notify := func(event AuthEvent) {
			if interaction.Notify != nil {
				interaction.Notify(event)
			}
		}
		prompt := func(promptCtx context.Context, prompt OAuthPrompt) (string, error) {
			return interaction.Prompt(promptCtx, AuthTextPrompt{Message: prompt.Message, Placeholder: prompt.Placeholder})
		}
		selectPrompt := func(promptCtx context.Context, prompt OAuthSelectPrompt) (string, error) {
			options := make([]AuthSelectOption, 0, len(prompt.Options))
			for _, option := range prompt.Options {
				options = append(options, AuthSelectOption{ID: option.ID, Label: option.Label})
			}
			return interaction.Prompt(promptCtx, AuthSelectPrompt{Message: prompt.Message, Options: options})
		}
		var promptMu sync.Mutex
		placeholder := ""
		manualMessage := "Complete login in your browser, or paste the authorization code / redirect URL here:"
		if provider.ID() == "openrouter" {
			manualMessage = "Complete sign-in in your browser, or paste the authorization code / redirect URL here:"
		}
		manual := func(promptCtx context.Context) (string, error) {
			promptMu.Lock()
			hint := placeholder
			promptMu.Unlock()
			return interaction.Prompt(promptCtx, AuthManualCodePrompt{Message: manualMessage, Placeholder: hint})
		}
		callbacks := OAuthLoginCallbacks{
			OnAuth: func(info OAuthAuthInfo) {
				if parsed, err := url.Parse(info.URL); err == nil {
					hint := parsed.Query().Get("redirect_uri")
					if provider.ID() == "openrouter" {
						hint = parsed.Query().Get("callback_url")
					}
					promptMu.Lock()
					placeholder = hint
					promptMu.Unlock()
				}
				notify(AuthURLEvent(info))
			},
			OnDeviceCode: func(info OAuthDeviceCodeInfo) {
				notify(AuthDeviceCodeEvent{UserCode: info.UserCode, VerificationURI: info.VerificationURI, IntervalSeconds: new(info.IntervalSeconds), ExpiresInSeconds: new(info.ExpiresInSeconds)})
			},
			OnProgress: func(message string) { notify(AuthProgressEvent{Message: message}) },
			OnPrompt:   func(value OAuthPrompt) (string, error) { return prompt(ctx, value) }, OnPromptContext: prompt,
			OnSelect: func(value OAuthSelectPrompt) (string, error) { return selectPrompt(ctx, value) }, OnSelectContext: selectPrompt,
			OnManualCodeInput: func() (string, error) { return manual(ctx) }, OnManualCodeInputContext: manual,
			OnManualCodePromptContext: func(promptCtx context.Context, prompt AuthManualCodePrompt) (string, error) {
				return interaction.Prompt(promptCtx, prompt)
			},
			GetDeviceID: options.GetDeviceID,
			AgentName:   options.AgentName,
		}
		var credential OAuthCredentials
		var err error
		if login, ok := provider.(interface {
			LoginContext(context.Context, OAuthLoginCallbacks) (OAuthCredentials, error)
		}); ok {
			credential, err = login.LoginContext(ctx, callbacks)
		} else {
			credential, err = provider.Login(callbacks)
		}
		if err != nil {
			return Credential{}, err
		}
		if ctx.Err() != nil {
			return Credential{}, context.Cause(ctx)
		}
		return credentialFromOAuth(credential)
	}
}
