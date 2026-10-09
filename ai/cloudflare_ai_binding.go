package ai

import (
	"errors"
	"net/http"
)

// Ports packages/ai/src/api/cloudflare-ai-binding.ts.

// CloudflareGatewayBindingAuthSentinel is the placeholder value of an auth header on a binding-routed request. API implementations require an API key or a recognized auth header before dispatch; binding calls are pre-authenticated, so a request sends `cf-aig-authorization: Bearer <sentinel>` to satisfy the check, with nil `Authorization` and `x-api-key` headers so no SDK placeholder reaches the gateway (CLOUDFLARE_GATEWAY_BINDING_AUTH_SENTINEL).
const CloudflareGatewayBindingAuthSentinel = "cloudflare-gateway-binding"

// AIBinding is the Workers AI binding (`env.AI`). Fetch is the binding's `fetch()` passthrough; it is optional because the published binding type does not declare it, so CreateAIBindingFetch checks it. Upstream's `aiGatewayLogId` member only pins the TypeScript type to the AI binding and has no behaviour, so Go does not carry it.
type AIBinding struct {
	Fetch FetchFunction
}

// CreateAIBindingFetch returns a fetch backed by the AI binding, for models whose base URL names a route the binding serves, including the gateway's provider passthrough `https://workers-binding.ai/ai-gateway/gateways/{gateway}/{provider}/...`. Requests pass through untouched. A binding without Fetch is rejected here, not on the first request. Set the result as `StreamOptions.Fetch` with `&http.Client{Transport: fetch}`.
func CreateAIBindingFetch(binding AIBinding) (FetchFunction, error) {
	if binding.Fetch == nil {
		return nil, errors.New("createAiBindingFetch: the AI binding does not expose fetch()")
	}
	bindingFetch := binding.Fetch
	return func(request *http.Request) (*http.Response, error) { return bindingFetch(request) }, nil
}
