package ai

// Union and record aliases for packages/ai/src/types.ts and api/google-shared.ts.

// OpenRouterRouting is OpenRouter's provider-routing preferences, sent verbatim as the request's `provider` field (types.ts:977).
type OpenRouterRouting = map[string]any

// VercelGatewayRouting is the Vercel AI Gateway routing preference: which upstream providers the gateway may use and in which order (types.ts VercelGatewayRouting).
type VercelGatewayRouting struct {
	// Only lists provider slugs to exclusively use for a request, for example ["bedrock", "anthropic"]. An empty list is set.
	Only []string `json:"only,omitzero"`
	// Order lists provider slugs to try in order, for example ["anthropic", "openai"]. An empty list is set.
	Order []string `json:"order,omitzero"`
}

// ImagesInputContent is one block of an image-generation request: text or an image (types.ts:614).
type ImagesInputContent = ContentBlock

// ImagesOutputContent is one block of an image-generation result: text or an image (types.ts:615).
type ImagesOutputContent = ContentBlock

// RoleMessage is upstream's `{ role: string }`: anything with a role, including an agent message type the transcript helpers do not know.
type RoleMessage interface{ MessageRole() string }

// TranscriptMessages is any message list: `readonly { role: string }[]` (utils/transcript.ts:40). The replay helpers read only the entries whose role is "system".
type TranscriptMessages[M RoleMessage] = []M

// ChatTemplateKwargValue is one chat-template keyword value: a string, number, boolean or null, or a `{ "$var": ... }` placeholder resolved per request (types.ts:90).
type ChatTemplateKwargValue = any
