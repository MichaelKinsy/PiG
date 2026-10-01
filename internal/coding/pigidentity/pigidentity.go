// Package pigidentity holds the product identity PiG sends to services in place
// of Pi's.
//
// identity.json is the one source. The Go host reads it here, and
// automation/gen/pi-identity-patches.mjs applies the same values to the Pi
// JavaScript that the Node extension runtime vendors, so a Node extension that
// calls the Pi SDK or pi-ai sends the identity the Go host sends.
package pigidentity

import (
	_ "embed"
	"encoding/json"
)

// pig divergence (D26): every value below replaces a Pi-branded literal in
// upstream. Values a provider requires from a specific client (GitHub Copilot's
// editor headers, Anthropic OAuth's claude-cli) are not here; PiG keeps them as
// Pi does.
var (
	// OpenRouterReferer replaces provider-attribution.ts's `HTTP-Referer: https://pi.dev`.
	OpenRouterReferer string
	// OpenRouterTitle replaces `X-OpenRouter-Title: pi`.
	OpenRouterTitle string
	// OpenRouterCategories is `X-OpenRouter-Categories`, unchanged from Pi.
	OpenRouterCategories string
	// NvidiaBillingOrigin replaces `X-BILLING-INVOKE-ORIGIN: Pi`.
	NvidiaBillingOrigin string
	// CloudflareUserAgent replaces the Cloudflare `User-Agent: pi-coding-agent`.
	CloudflareUserAgent string
	// OpenCodeClient replaces `x-opencode-client: pi`.
	OpenCodeClient string
	// CodexOriginator replaces the OpenAI Codex `originator: pi`, sent on requests and in the login URL.
	CodexOriginator string
	// XAIReferrer replaces the xAI device-code `referrer: pi`.
	XAIReferrer string
	// AgentMarker replaces the `AI_AGENT=pi` marker child processes inherit.
	AgentMarker string
	// UserAgentProduct replaces the `pi` product name in Pi's user agents.
	UserAgentProduct string
	// HostedOrigin is the PiG-operated origin that replaces Pi's pi.dev (D64).
	HostedOrigin string
)

//go:embed identity.json
var identityJSON []byte

func init() {
	var identity struct {
		OpenRouterReferer    string `json:"openRouterReferer"`
		OpenRouterTitle      string `json:"openRouterTitle"`
		OpenRouterCategories string `json:"openRouterCategories"`
		NvidiaBillingOrigin  string `json:"nvidiaBillingOrigin"`
		CloudflareUserAgent  string `json:"cloudflareUserAgent"`
		OpenCodeClient       string `json:"openCodeClient"`
		CodexOriginator      string `json:"codexOriginator"`
		XAIReferrer          string `json:"xaiReferrer"`
		AgentMarker          string `json:"agentMarker"`
		UserAgentProduct     string `json:"userAgentProduct"`
		HostedOrigin         string `json:"hostedOrigin"`
	}
	if err := json.Unmarshal(identityJSON, &identity); err != nil {
		panic("pigidentity: identity.json: " + err.Error())
	}
	// A missing key would send an empty identity, so an incomplete file stops startup.
	for name, value := range map[string]string{
		"openRouterReferer": identity.OpenRouterReferer, "openRouterTitle": identity.OpenRouterTitle, "openRouterCategories": identity.OpenRouterCategories,
		"nvidiaBillingOrigin": identity.NvidiaBillingOrigin, "cloudflareUserAgent": identity.CloudflareUserAgent, "openCodeClient": identity.OpenCodeClient,
		"codexOriginator": identity.CodexOriginator, "xaiReferrer": identity.XAIReferrer, "agentMarker": identity.AgentMarker,
		"userAgentProduct": identity.UserAgentProduct, "hostedOrigin": identity.HostedOrigin,
	} {
		if value == "" {
			panic("pigidentity: identity.json has no " + name)
		}
	}
	OpenRouterReferer = identity.OpenRouterReferer
	OpenRouterTitle = identity.OpenRouterTitle
	OpenRouterCategories = identity.OpenRouterCategories
	NvidiaBillingOrigin = identity.NvidiaBillingOrigin
	CloudflareUserAgent = identity.CloudflareUserAgent
	OpenCodeClient = identity.OpenCodeClient
	CodexOriginator = identity.CodexOriginator
	XAIReferrer = identity.XAIReferrer
	AgentMarker = identity.AgentMarker
	UserAgentProduct = identity.UserAgentProduct
	HostedOrigin = identity.HostedOrigin
}
