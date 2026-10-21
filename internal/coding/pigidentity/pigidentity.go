// Package pigidentity holds the product identity PiG sends to services in place
// of Pi's.
//
// identity.json is the one source. automation/gen/pi-identity-patches.mjs
// applies its values to the Pi JavaScript that the Node extension runtime
// vendors, so a Node extension that calls the Pi SDK or pi-ai sends the identity
// the Go host sends. The Go host spells the same values as constants below, so a
// process decodes nothing at start; TestIdentityJSONIsTheSingleSource fails when
// identity.json and the constants differ.
package pigidentity

// pig divergence (D26): every value below replaces a Pi-branded literal in
// upstream. Values a provider requires from a specific client (GitHub Copilot's
// editor headers, Anthropic OAuth's claude-cli) are not here; PiG keeps them as
// Pi does.
const (
	// AttributionName replaces PI_ATTRIBUTION.name (`pi`): the OpenRouter app title, the NVIDIA invoke origin (first letter upper-cased) and the OpenCode client.
	AttributionName = "PiG"
	// AttributionURL replaces PI_ATTRIBUTION.url (`https://pi.dev`): the OpenRouter `HTTP-Referer`.
	AttributionURL = "https://github.com/MichaelKinsy/PiG"
	// AttributionCategory is PI_ATTRIBUTION.categories' one entry, unchanged from Pi.
	AttributionCategory = "cli-agent"
	// CodexOriginator replaces the OpenAI Codex `originator: pi`, sent on requests and in the login URL.
	CodexOriginator = "pig"
	// ChatGPTAgentName replaces the `agent_name_hint=Pi` of the Sign in with ChatGPT authorization URL.
	ChatGPTAgentName = "PiG"
	// XAIReferrer replaces the xAI device-code `referrer: pi`.
	XAIReferrer = "pig"
	// AgentMarker replaces the `AI_AGENT=pi` marker child processes inherit.
	AgentMarker = "pig"
	// UserAgentProduct replaces the `pi` product name in Pi's user agents.
	UserAgentProduct = "pig"
	// HostedOrigin is the PiG-operated origin that replaces Pi's pi.dev (D64).
	HostedOrigin = "https://pi-in-go.dev"
)
