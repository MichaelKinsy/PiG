package ai

// Ports packages/ai/src/auth/helpers.ts

import "context"

func envAPIKeyLogin(name string) func(context.Context, AuthInteraction) (Credential, error) {
	return func(ctx context.Context, interaction AuthInteraction) (Credential, error) {
		if ctx.Err() != nil {
			return Credential{}, context.Cause(ctx)
		}
		credential, err := promptAPIKey(ctx, interaction, "Enter "+name)
		if err != nil {
			return Credential{}, err
		}
		if ctx.Err() != nil {
			return Credential{}, context.Cause(ctx)
		}
		return credential, nil
	}
}
