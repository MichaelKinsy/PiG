package codingagent

// Ports packages/coding-agent/src/core/provider-composer.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

func adaptExtensionOAuthLogin(login func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error)) func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
	if login == nil {
		return nil
	}
	return func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
		notify := func(event ai.AuthEvent) {
			if interaction.Notify != nil {
				interaction.Notify(event)
			}
		}
		prompt := func(signal context.Context, value ai.OAuthPrompt) (string, error) {
			return interaction.Prompt(signal, ai.AuthTextPrompt{Message: value.Message, Placeholder: value.Placeholder})
		}
		manual := func(signal context.Context) (string, error) {
			return interaction.Prompt(signal, ai.AuthManualCodePrompt{Message: "Paste the authorization code"})
		}
		selectPrompt := func(signal context.Context, value ai.OAuthSelectPrompt) (string, error) {
			options := make([]ai.AuthSelectOption, 0, len(value.Options))
			for _, option := range value.Options {
				options = append(options, ai.AuthSelectOption{ID: option.ID, Label: option.Label})
			}
			return interaction.Prompt(signal, ai.AuthSelectPrompt{Message: value.Message, Options: options})
		}
		credential, err := login(ctx, ai.OAuthLoginCallbacks{
			OnAuth: func(info ai.OAuthAuthInfo) { notify(ai.AuthURLEvent(info)) },
			OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
				notify(ai.AuthDeviceCodeEvent{UserCode: info.UserCode, VerificationURI: info.VerificationURI, IntervalSeconds: new(info.IntervalSeconds), ExpiresInSeconds: new(info.ExpiresInSeconds)})
			},
			OnProgress: func(message string) { notify(ai.AuthProgressEvent{Message: message}) },
			OnPrompt:   func(value ai.OAuthPrompt) (string, error) { return prompt(ctx, value) }, OnPromptContext: prompt,
			OnManualCodeInput: func() (string, error) { return manual(ctx) }, OnManualCodeInputContext: manual,
			OnSelect: func(value ai.OAuthSelectPrompt) (string, error) { return selectPrompt(ctx, value) }, OnSelectContext: selectPrompt,
		})
		if err != nil {
			return ai.Credential{}, err
		}
		if ctx.Err() != nil {
			return ai.Credential{}, context.Cause(ctx)
		}
		credential.Type = ai.CredentialOAuth
		return credential, nil
	}
}
