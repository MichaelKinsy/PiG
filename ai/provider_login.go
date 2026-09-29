package ai

// Ports packages/ai/src/providers/amazon-bedrock.ts
// Ports packages/ai/src/providers/google-vertex.ts
// Ports packages/ai/src/providers/cloudflare-auth.ts

import (
	"context"
	"encoding/json"
	"fmt"
)

func bedrockLogin(ctx context.Context, interaction AuthInteraction) (Credential, error) {
	if ctx.Err() != nil {
		return Credential{}, context.Cause(ctx)
	}
	method, err := interaction.Prompt(ctx, AuthSelectPrompt{Message: "Select Amazon Bedrock authentication method:", Options: []AuthSelectOption{
		{ID: "bearer-token", Label: "Bearer token"}, {ID: "aws-profile", Label: "AWS profile"}, {ID: "credential-chain", Label: "Existing AWS credential chain"},
	}})
	if err != nil {
		return Credential{}, err
	}
	if ctx.Err() != nil {
		return Credential{}, context.Cause(ctx)
	}
	if method == "bearer-token" {
		return promptAPIKey(ctx, interaction, "Enter Amazon Bedrock bearer token")
	}
	interaction.Notify(AuthInfoEvent{Message: "Amazon Bedrock supports AWS profiles, IAM credentials, and role-based credentials.", Links: []AuthInfoLink{{Label: "AWS credential provider chain", URL: "https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html"}}})
	if method == "aws-profile" {
		profile, err := interaction.Prompt(ctx, AuthTextPrompt{Message: "Enter AWS profile name"})
		if err != nil {
			return Credential{}, err
		}
		return Credential{Type: CredentialAPIKey, Env: map[string]string{"AWS_PROFILE": profile}}, nil
	}
	if method != "credential-chain" {
		return Credential{}, fmt.Errorf("Unknown Amazon Bedrock auth method: %s", method)
	}
	if _, err := interaction.Prompt(ctx, AuthTextPrompt{Message: "Configure AWS credentials, then press Enter to continue"}); err != nil {
		return Credential{}, err
	}
	return Credential{Type: CredentialAPIKey}, nil
}

func vertexLogin(ctx context.Context, interaction AuthInteraction) (Credential, error) {
	if ctx.Err() != nil {
		return Credential{}, context.Cause(ctx)
	}
	method, err := interaction.Prompt(ctx, AuthSelectPrompt{Message: "Select Google Vertex AI authentication method:", Options: []AuthSelectOption{
		{ID: "api-key", Label: "Google Cloud API key"}, {ID: "adc", Label: "Application Default Credentials"}, {ID: "service-account", Label: "Service account credentials file"},
	}})
	if err != nil {
		return Credential{}, err
	}
	if ctx.Err() != nil {
		return Credential{}, context.Cause(ctx)
	}
	if method == "api-key" {
		return promptAPIKey(ctx, interaction, "Enter Google Cloud API key")
	}
	if method != "adc" && method != "service-account" {
		return Credential{}, fmt.Errorf("Unknown Google Vertex AI auth method: %s", method)
	}
	message := "Run `gcloud auth application-default login`, then provide the project and location."
	if method == "service-account" {
		message = "Provide a service account credentials file, project, and location."
	}
	interaction.Notify(AuthInfoEvent{Message: message, Links: []AuthInfoLink{{Label: "Application Default Credentials", URL: "https://cloud.google.com/docs/authentication/provide-credentials-adc"}}})
	project, err := interaction.Prompt(ctx, AuthTextPrompt{Message: "Enter Google Cloud project ID"})
	if err != nil {
		return Credential{}, err
	}
	location, err := interaction.Prompt(ctx, AuthTextPrompt{Message: "Enter Google Cloud location"})
	if err != nil {
		return Credential{}, err
	}
	env := map[string]string{"GOOGLE_CLOUD_PROJECT": project, "GOOGLE_CLOUD_LOCATION": location}
	if method == "service-account" {
		path, err := interaction.Prompt(ctx, AuthTextPrompt{Message: "Enter service account credentials file path"})
		if err != nil {
			return Credential{}, err
		}
		if path != "" {
			env["GOOGLE_APPLICATION_CREDENTIALS"] = path
		}
	}
	return Credential{Type: CredentialAPIKey, Env: env}, nil
}

func cloudflareLogin(gateway bool) func(context.Context, AuthInteraction) (Credential, error) {
	return func(ctx context.Context, interaction AuthInteraction) (Credential, error) {
		credential, err := promptAPIKey(ctx, interaction, "Enter Cloudflare API key")
		if err != nil {
			return Credential{}, err
		}
		accountID, err := interaction.Prompt(ctx, AuthTextPrompt{Message: "Enter Cloudflare account ID"})
		if err != nil {
			return Credential{}, err
		}
		credential.Env = map[string]string{cloudflareAccountID: accountID}
		if gateway {
			gatewayID, err := interaction.Prompt(ctx, AuthTextPrompt{Message: "Enter Cloudflare AI Gateway ID"})
			if err != nil {
				return Credential{}, err
			}
			credential.Env[cloudflareGatewayID] = gatewayID
		}
		return credential, nil
	}
}

func promptAPIKey(ctx context.Context, interaction AuthInteraction, message string) (Credential, error) {
	key, err := interaction.Prompt(ctx, AuthSecretPrompt{Message: message})
	if err != nil {
		return Credential{}, err
	}
	credential := Credential{Type: CredentialAPIKey, Key: key}
	if key == "" {
		credential.Extra = map[string]json.RawMessage{"key": json.RawMessage(`""`)}
	}
	return credential, nil
}
