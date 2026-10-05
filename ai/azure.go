package ai

// Ports packages/ai/src/providers/azure.ts

// azureCompletionsEndpoint resolves the Azure endpoint and deployment for each Chat Completions request.
type azureCompletionsEndpoint struct {
	options AzureEndpointOptions
	// modelBaseURL is the model's configured base URL; the endpoint falls back to it last.
	modelBaseURL string
}

// azureProviderID is the azure provider's ID; its Chat Completions models (Foundry deployments) go through azureCompletionsEndpoint.
// upstream: packages/ai/src/providers/azure.ts:azureProvider registers azureStreams(openAICompletionsApi()) for this provider only.
const azureProviderID = "azure"

// withAzureEndpoint attaches the per-request Azure endpoint resolution to the azure provider's Chat Completions configuration.
// Azure models ship without a base URL, so each request resolves its endpoint and sends the deployment name as the request's model while the catalog ID stays the logical model.
func withAzureEndpoint(cfg OpenAIConfig) OpenAIConfig {
	if cfg.ProviderID == azureProviderID && cfg.azure == nil {
		cfg.azure = &azureCompletionsEndpoint{options: AzureEndpointOptions{Env: cfg.Env}, modelBaseURL: cfg.BaseURL}
	}
	return cfg
}

// resolve returns the provider for one request: the same configuration with the resolved base URL and deployment name.
// Request-scoped env entries outrank the provider's, as stream options carry env in upstream.
func (e *azureCompletionsEndpoint) resolve(p *openAIProvider, opts StreamOptions) (*openAIProvider, error) {
	options := e.options
	options.Env = mergeProviderEnv(options.Env, opts.Env)
	baseURL, err := ResolveAzureBaseURL(e.modelBaseURL, options)
	if err != nil {
		return nil, err
	}
	resolved := *p
	resolved.cfg.BaseURL = baseURL
	resolved.cfg.GetBaseURL = nil
	resolved.cfg.requestModel = ResolveAzureDeploymentName(p.cfg.Model, options)
	resolved.cfg.azure = nil
	return &resolved, nil
}
