package ai

// Built-in provider auth methods. Mirrors the `auth` field of each upstream
// provider in packages/ai/src/providers/*.ts, envApiKeyAuth in
// packages/ai/src/auth/helpers.ts and the Cloudflare auth in
// packages/ai/src/providers/cloudflare-auth.ts. OAuth methods adapt the
// ported OAuth provider registry.

import (
	"context"
	"fmt"
)

// EnvAPIKeyAuth is standard API-key auth: a stored credential key wins,
// otherwise the first set environment variable resolves. Mirrors upstream
// envApiKeyAuth.
func EnvAPIKeyAuth(name string, envVars ...string) *APIKeyAuth {
	return &APIKeyAuth{
		Name: name,
		Resolve: func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if input.Credential != nil && input.Credential.Key != "" {
				return &AuthResult{Auth: ModelAuth{APIKey: input.Credential.Key}, Env: input.Credential.Env, Source: "stored credential"}, nil
			}
			for _, envVar := range envVars {
				if value, ok := input.Ctx.Env(envVar); ok {
					return &AuthResult{Auth: ModelAuth{APIKey: value}, Source: envVar}, nil
				}
			}
			return nil, nil
		},
	}
}

func anthropicAPIKeyAuth() *APIKeyAuth {
	return &APIKeyAuth{
		Name: "Anthropic API key",
		Resolve: func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if input.Credential != nil && input.Credential.Key != "" {
				return &AuthResult{Auth: ModelAuth{APIKey: input.Credential.Key}, Env: input.Credential.Env, Source: "stored credential"}, nil
			}
			if token, ok := input.Ctx.Env(AnthropicAuthTokenEnv); ok {
				return &AuthResult{Auth: ModelAuth{Headers: ProviderHeaders{"Authorization": new("Bearer " + token)}}, Source: AnthropicAuthTokenEnv}, nil
			}
			for _, envVar := range []string{AnthropicOAuthTokenEnv, AnthropicAPIKeyEnv} {
				if value, ok := input.Ctx.Env(envVar); ok {
					return &AuthResult{Auth: ModelAuth{APIKey: value}, Source: envVar}, nil
				}
			}
			return nil, nil
		},
	}
}

// bedrockAuth accepts a bearer token or the AWS default credential chain.
func bedrockAuth() *APIKeyAuth {
	return &APIKeyAuth{
		Name: "AWS credentials or bearer token",
		Resolve: func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error) {
			env := func(name string) (string, bool) {
				if ctx.Err() != nil {
					return "", false
				}
				return input.Ctx.Env(name)
			}
			credential := input.Credential
			if credential != nil && credential.Key != "" {
				return &AuthResult{Auth: ModelAuth{APIKey: credential.Key}, Env: credential.Env, Source: "stored credential"}, ctx.Err()
			}
			if _, ok := env("AWS_BEARER_TOKEN_BEDROCK"); ok {
				return &AuthResult{Source: "AWS_BEARER_TOKEN_BEDROCK"}, ctx.Err()
			}
			if profile, stored := credentialEnvValue(credential, "AWS_PROFILE"); stored {
				if profile != "" {
					return &AuthResult{Env: credential.Env, Source: "stored credential"}, ctx.Err()
				}
			} else if _, ok := env("AWS_PROFILE"); ok {
				return &AuthResult{Env: credentialEnv(credential), Source: "AWS_PROFILE"}, ctx.Err()
			}
			_, accessKey := env("AWS_ACCESS_KEY_ID")
			_, secretKey := env("AWS_SECRET_ACCESS_KEY")
			switch {
			case accessKey && secretKey:
				return &AuthResult{Source: "AWS access keys"}, ctx.Err()
			case envSet(env, "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"), envSet(env, "AWS_CONTAINER_CREDENTIALS_FULL_URI"):
				return &AuthResult{Source: "ECS task role"}, ctx.Err()
			case envSet(env, "AWS_WEB_IDENTITY_TOKEN_FILE"):
				return &AuthResult{Source: "web identity token"}, ctx.Err()
			}
			return nil, ctx.Err()
		},
	}
}

const vertexADCPath = "~/.config/gcloud/application_default_credentials.json"

// vertexAuth accepts an explicit API key or Application Default Credentials
// with a project and location.
func vertexAuth() *APIKeyAuth {
	return &APIKeyAuth{
		Name: "Google Cloud credentials",
		Resolve: func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			credential := input.Credential
			if credential != nil && credential.Key != "" {
				return &AuthResult{Auth: ModelAuth{APIKey: credential.Key}, Source: "stored credential"}, nil
			}
			if key, ok := input.Ctx.Env("GOOGLE_CLOUD_API_KEY"); ok {
				return &AuthResult{Auth: ModelAuth{APIKey: key}, Source: "GOOGLE_CLOUD_API_KEY"}, nil
			}
			adcPath, stored := credentialEnvValue(credential, "GOOGLE_APPLICATION_CREDENTIALS")
			if !stored {
				adcPath, _ = input.Ctx.Env("GOOGLE_APPLICATION_CREDENTIALS")
			}
			if adcPath == "" && !stored {
				adcPath = vertexADCPath
			}
			hasCredentials := input.Ctx.FileExists(adcPath)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			project, stored := credentialEnvValue(credential, "GOOGLE_CLOUD_PROJECT")
			if !stored {
				if project, stored = input.Ctx.Env("GOOGLE_CLOUD_PROJECT"); !stored {
					project, _ = input.Ctx.Env("GCLOUD_PROJECT")
				}
			}
			location, stored := credentialEnvValue(credential, "GOOGLE_CLOUD_LOCATION")
			if !stored {
				location, _ = input.Ctx.Env("GOOGLE_CLOUD_LOCATION")
			}
			if hasCredentials && project != "" && location != "" {
				source := "gcloud application default credentials"
				if credential != nil {
					source = "stored credential"
				}
				return &AuthResult{Env: credentialEnv(credential), Source: source}, nil
			}
			return nil, nil
		},
	}
}

const (
	cloudflareAPIKey    = "CLOUDFLARE_API_KEY"
	cloudflareAccountID = "CLOUDFLARE_ACCOUNT_ID"
	cloudflareGatewayID = "CLOUDFLARE_GATEWAY_ID"
)

type cloudflareResolution struct {
	apiKey string
	env    map[string]string
	source string
}

// resolveCloudflareValue merges per field: the credential value wins, then
// the ambient environment.
func resolveCloudflareValue(name string, input APIKeyAuthInput) string {
	if input.Credential != nil {
		if name == cloudflareAPIKey {
			if input.Credential.Key != "" {
				return input.Credential.Key
			}
		} else if value, ok := input.Credential.Env[name]; ok {
			return value
		}
	}
	value, _ := input.Ctx.Env(name)
	return value
}

func resolveCloudflareEnv(gateway bool, input APIKeyAuthInput) *cloudflareResolution {
	apiKey := resolveCloudflareValue(cloudflareAPIKey, input)
	accountID := resolveCloudflareValue(cloudflareAccountID, input)
	gatewayID := ""
	if gateway {
		gatewayID = resolveCloudflareValue(cloudflareGatewayID, input)
	}
	if apiKey == "" || accountID == "" || (gateway && gatewayID == "") {
		return nil
	}
	env := map[string]string{cloudflareAccountID: accountID}
	if gatewayID != "" {
		env[cloudflareGatewayID] = gatewayID
	}
	source := cloudflareAPIKey
	if input.Credential != nil {
		source = "stored credential"
	}
	return &cloudflareResolution{apiKey: apiKey, env: env, source: source}
}

func cloudflareWorkersAIAuth() *APIKeyAuth {
	return &APIKeyAuth{
		Name: "Cloudflare API key",
		Resolve: func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			resolved := resolveCloudflareEnv(false, input)
			if resolved == nil {
				return nil, nil
			}
			return &AuthResult{Auth: ModelAuth{APIKey: resolved.apiKey}, Env: resolved.env, Source: resolved.source}, nil
		},
	}
}

func cloudflareAIGatewayAuth() *APIKeyAuth {
	return &APIKeyAuth{
		Name: "Cloudflare API key",
		Resolve: func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			resolved := resolveCloudflareEnv(true, input)
			if resolved == nil {
				return nil, nil
			}
			headers := ProviderHeaders{
				"cf-aig-authorization": new("Bearer " + resolved.apiKey),
				"Authorization":        nil,
				"x-api-key":            nil,
			}
			return &AuthResult{Auth: ModelAuth{Headers: headers}, Env: resolved.env, Source: resolved.source}, nil
		},
	}
}

func credentialEnvValue(credential *Credential, name string) (string, bool) {
	if credential == nil {
		return "", false
	}
	value, ok := credential.Env[name]
	return value, ok
}

func credentialEnv(credential *Credential) map[string]string {
	if credential == nil {
		return nil
	}
	return credential.Env
}

func envSet(env func(string) (string, bool), name string) bool {
	_, ok := env(name)
	return ok
}

// oauthToAuth mirrors each built-in upstream OAuth toAuth. Providers without a
// built-in mapping use their registered API-key extraction, as upstream's
// extension OAuth adapter does.
func oauthToAuth(providerID string, provider OAuthProviderInterface) func(Credential) (ModelAuth, error) {
	return func(credential Credential) (ModelAuth, error) {
		switch providerID {
		case "kimi-coding":
			return ModelAuth{Headers: ProviderHeaders{"Authorization": new("Bearer " + credential.Access)}}, nil
		case "github-copilot":
			return ModelAuth{APIKey: credential.Access, BaseURL: getCopilotBaseURL(credential.Access, credential.EnterpriseDomain)}, nil
		case "anthropic", "openai-codex", "openrouter", "xai":
			return ModelAuth{APIKey: credential.Access}, nil
		}
		return ModelAuth{APIKey: provider.GetAPIKey(credentialToOAuth(credential))}, nil
	}
}

func credentialToOAuth(credential Credential) OAuthCredentials {
	return OAuthCredentials{Refresh: credential.Refresh, Access: credential.Access, Expires: credential.Expires, ProjectID: credential.ProjectID, Scope: credential.Scope}
}

type oauthRefreshResult struct {
	credentials OAuthCredentials
	err         error
}

// oauthRefresh adapts a registered provider's refresh. A refresh without a
// context races the context the way upstream races the refresh promise with
// its abort signal; an abandoned refresh finishes into a buffered channel and
// its result is discarded.
func oauthRefresh(provider OAuthProviderInterface) func(context.Context, Credential) (Credential, error) {
	return func(ctx context.Context, credential Credential) (Credential, error) {
		if err := ctx.Err(); err != nil {
			return Credential{}, err
		}
		done := make(chan oauthRefreshResult, 1)
		go func() {
			var refreshed OAuthCredentials
			var err error
			if refresher, ok := provider.(oauthContextRefresh); ok {
				refreshed, err = refresher.RefreshTokenContext(ctx, credentialToOAuth(credential))
			} else {
				refreshed, err = provider.RefreshToken(credentialToOAuth(credential))
			}
			done <- oauthRefreshResult{credentials: refreshed, err: err}
		}()
		select {
		case <-ctx.Done():
			return Credential{}, ctx.Err()
		case result := <-done:
			if result.err != nil {
				return Credential{}, result.err
			}
			return Credential{
				Type:             CredentialOAuth,
				Refresh:          result.credentials.Refresh,
				Access:           result.credentials.Access,
				Expires:          result.credentials.Expires,
				ProjectID:        result.credentials.ProjectID,
				Scope:            result.credentials.Scope,
				EnterpriseDomain: credential.EnterpriseDomain,
			}, nil
		}
	}
}

// builtinOAuthNames mirrors each built-in upstream lazyOAuth name and
// subscription flag.
var builtinOAuthNames = map[string]struct {
	name         string
	subscription bool
}{
	"anthropic":      {"Anthropic (Claude Pro/Max)", true},
	"github-copilot": {"GitHub Copilot", true},
	"kimi-coding":    {"Kimi Code (subscription)", true},
	"meta":           {"Meta (Muse subscription)", true},
	"openai-codex":   {"OpenAI (ChatGPT Plus/Pro)", true},
	"openrouter":     {"OpenRouter OAuth", false},
	"radius":         {"Radius", false},
	"xai":            {"xAI (Grok/X subscription)", true},
}

// OAuthProviderAuth returns the OAuth auth method for a provider registered
// in the OAuth provider registry.
func OAuthProviderAuth(providerID string) (*OAuthAuth, bool) {
	provider, ok := GetOAuthProvider(providerID)
	if !ok {
		return nil, false
	}
	name, subscription := provider.Name(), false
	if builtin, ok := builtinOAuthNames[providerID]; ok {
		name, subscription = builtin.name, builtin.subscription
	}
	return &OAuthAuth{
		Name:           name,
		IsSubscription: subscription,
		Refresh:        oauthRefresh(provider),
		ToAuth:         oauthToAuth(providerID, provider),
	}, true
}

// builtinAPIKeyAuth mirrors each built-in upstream provider's api-key auth.
// openai-codex is OAuth-only.
func builtinAPIKeyAuth(providerID string) (*APIKeyAuth, bool) {
	switch providerID {
	case "amazon-bedrock":
		return bedrockAuth(), true
	case "anthropic":
		return anthropicAPIKeyAuth(), true
	case "cloudflare-ai-gateway":
		return cloudflareAIGatewayAuth(), true
	case "cloudflare-workers-ai":
		return cloudflareWorkersAIAuth(), true
	case "google-vertex":
		return vertexAuth(), true
	case "openai-codex":
		return nil, true
	}
	name, ok := builtinAPIKeyNames[providerID]
	if !ok {
		return nil, false
	}
	return EnvAPIKeyAuth(name, getAPIKeyEnvVars(providerID)...), true
}

// builtinAPIKeyNames mirrors each envApiKeyAuth display name; the variables
// come from the env-api-keys table.
var builtinAPIKeyNames = map[string]string{
	"ant-ling":                   "Ant Ling API key",
	"azure-openai-responses":     "Azure OpenAI API key",
	"baseten":                    "Baseten API key",
	"cerebras":                   "Cerebras API key",
	"deepseek":                   "DeepSeek API key",
	"fireworks":                  "Fireworks API key",
	"github-copilot":             "GitHub Copilot token",
	"google":                     "Gemini API key",
	"groq":                       "Groq API key",
	"huggingface":                "Hugging Face token",
	"kimi-coding":                "Kimi API key",
	"meta":                       "Meta Model API key",
	"minimax":                    "MiniMax API key",
	"minimax-cn":                 "MiniMax CN API key",
	"mistral":                    "Mistral API key",
	"moonshotai":                 "Moonshot AI API key",
	"moonshotai-cn":              "Moonshot AI API key",
	"nvidia":                     "NVIDIA API key",
	"openai":                     "OpenAI API key",
	"opencode":                   "OpenCode API key",
	"opencode-go":                "OpenCode API key",
	"openrouter":                 "OpenRouter API key",
	"qwen-token-plan":            "Qwen Token Plan API key",
	"qwen-token-plan-cn":         "Qwen Token Plan CN API key",
	"qwen-token-plan-individual": "Qwen Token Plan Individual API key",
	"radius":                     "Radius API key",
	"together":                   "Together API key",
	"vercel-ai-gateway":          "Vercel AI Gateway API key",
	"xai":                        "xAI API key",
	"xiaomi":                     "Xiaomi API key",
	"xiaomi-token-plan-ams":      "Xiaomi Token Plan AMS API key",
	"xiaomi-token-plan-cn":       "Xiaomi Token Plan CN API key",
	"xiaomi-token-plan-sgp":      "Xiaomi Token Plan SGP API key",
	"zai":                        "Z.AI API key",
	"zai-coding-cn":              "Z.AI Coding CN API key",
}

// RadiusProviderAuth mirrors the auth of upstream radiusProvider: the
// RADIUS_API_KEY api-key method and the provider's gateway OAuth, for the
// built-in provider and every models.json "oauth": "radius" gateway.
func RadiusProviderAuth(provider *RadiusProvider) ProviderAuth {
	oauth := provider.OAuth()
	return ProviderAuth{
		APIKey: EnvAPIKeyAuth(builtinAPIKeyNames[RadiusProviderID], getAPIKeyEnvVars(RadiusProviderID)...),
		OAuth: &OAuthAuth{
			Name:    provider.Name(),
			Refresh: oauthRefresh(oauth),
			ToAuth:  oauthToAuth(provider.ID(), oauth),
		},
	}
}

// BuiltinProviderAuth returns a built-in provider's auth methods. The OAuth
// method is present when the provider's OAuth flow is ported.
func BuiltinProviderAuth(providerID string) (ProviderAuth, error) {
	apiKey, ok := builtinAPIKeyAuth(providerID)
	if !ok {
		return ProviderAuth{}, fmt.Errorf("no built-in auth for provider %q", providerID)
	}
	auth := ProviderAuth{APIKey: apiKey}
	if _, ok := builtinOAuthNames[providerID]; ok {
		auth.OAuth, _ = OAuthProviderAuth(providerID)
	}
	return auth, nil
}
