package ai

// Mirrors upstream .upstream/current/packages/ai/src/api/anthropic-messages.ts
// createClient, getBetaFeatures, and the Claude Code identity sent with
// Anthropic subscription (OAuth) tokens, plus the ANTHROPIC_AUTH_TOKEN bearer
// branch of providers/anthropic.ts anthropicApiKeyAuth().resolve.

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// claudeCodeVersion is the Claude Code release named in the user-agent of
// OAuth-token requests.
const claudeCodeVersion = "2.1.280"

// claudeCodeSystemPrompt is the first system block of every OAuth-token request.
const claudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

const (
	claudeCodeBeta               = "claude-code-20250219"
	oauthBeta                    = "oauth-2025-04-20"
	fineGrainedToolStreamingBeta = "fine-grained-tool-streaming-2025-05-14"
	interleavedThinkingBeta      = "interleaved-thinking-2025-05-14"
	midConversationEffortBeta    = "mid-conversation-output-config-2026-07-01"
	thinkingBindingControlsBeta  = "thinking-binding-controls-2026-08-01"
	// midConversationToolChangesBeta enables tool_addition and tool_removal
	// blocks in mid-conversation system messages.
	midConversationToolChangesBeta = "mid-conversation-tool-changes-2026-07-01"
)

// claudeCodeTools lists the Claude Code 2.x tool names in canonical casing.
var claudeCodeTools = []string{
	"Read",
	"Write",
	"Edit",
	"Bash",
	"Grep",
	"Glob",
	"AskUserQuestion",
	"EnterPlanMode",
	"ExitPlanMode",
	"KillShell",
	"NotebookEdit",
	"Skill",
	"Task",
	"TaskOutput",
	"TodoWrite",
	"WebFetch",
	"WebSearch",
}

var ccToolLookup = func() map[string]string {
	lookup := make(map[string]string, len(claudeCodeTools))
	for _, name := range claudeCodeTools {
		lookup[strings.ToLower(name)] = name
	}
	return lookup
}()

// toClaudeCodeName returns the Claude Code casing of a tool name that matches a
// Claude Code tool case-insensitively, and any other name unchanged.
func toClaudeCodeName(name string) string {
	if canonical, ok := ccToolLookup[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

// fromClaudeCodeName maps a streamed tool name back to the casing of the
// matching current tool, and returns an unmatched name unchanged.
func fromClaudeCodeName(name string, tools []ToolSchema) string {
	lowerName := strings.ToLower(name)
	for _, tool := range tools {
		if strings.ToLower(tool.Name) == lowerName {
			return tool.Name
		}
	}
	return name
}

// isOAuthToken reports whether an API key is an Anthropic subscription token.
func isOAuthToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

// anthropicHeaderEntry is one key of a JavaScript header record.
type anthropicHeaderEntry struct {
	name string
	// value nil is JavaScript null: it removes the header.
	value *string
	// defined false reserves the key's position without a value.
	defined bool
}

// anthropicHeaders is a JavaScript header record built with Object.assign:
// keys are case-sensitive, and a reassigned key keeps its first position. When
// the record reaches the request, keys apply in order and the last key wins
// case-insensitively.
type anthropicHeaders struct {
	entries []anthropicHeaderEntry
}

func (headers *anthropicHeaders) assign(name string, value *string) {
	for index := range headers.entries {
		if headers.entries[index].name == name {
			headers.entries[index].value = value
			headers.entries[index].defined = true
			return
		}
	}
	headers.entries = append(headers.entries, anthropicHeaderEntry{name: name, value: value, defined: true})
}

func (headers *anthropicHeaders) set(name, value string) {
	headers.assign(name, &value)
}

// mergeAnthropicHeaders mirrors upstream mergeHeaders (Object.assign over each
// source in order).
func mergeAnthropicHeaders(sources ...anthropicHeaders) anthropicHeaders {
	var merged anthropicHeaders
	for _, source := range sources {
		for _, entry := range source.entries {
			if entry.defined {
				merged.assign(entry.name, entry.value)
			}
		}
	}
	return merged
}

// mergeAnthropicClientHeaders mirrors upstream mergeClientHeaders, which
// seeds User-Agent with getPiUserAgent(). The key holds the first position: a
// later exact-case User-Agent value (a configured header) replaces it in
// place, while a differently-cased user-agent key (the Claude Code OAuth
// identity) sorts after it as a separate key and wins on the request.
func mergeAnthropicClientHeaders(sources ...anthropicHeaders) anthropicHeaders {
	userAgent := PiUserAgent()
	seeded := anthropicHeaders{entries: []anthropicHeaderEntry{{name: "User-Agent", value: &userAgent, defined: true}}}
	for _, entry := range mergeAnthropicHeaders(sources...).entries {
		seeded.assign(entry.name, entry.value)
	}
	return seeded
}

// anthropicHeadersFromMap converts configured headers in sorted key order,
// which fixes the order of keys that differ only by case.
func anthropicHeadersFromMap(values map[string]string) anthropicHeaders {
	var headers anthropicHeaders
	for _, name := range slices.Sorted(maps.Keys(values)) {
		headers.set(name, values[name])
	}
	return headers
}

// anthropicHeadersFromProviderHeaders converts request headers in sorted key
// order; a nil value stays JavaScript null.
func anthropicHeadersFromProviderHeaders(values ProviderHeaders) anthropicHeaders {
	var headers anthropicHeaders
	for _, name := range slices.Sorted(maps.Keys(values)) {
		headers.assign(name, values[name])
	}
	return headers
}

// anthropicClient is the upstream createClient result: the SDK auth options,
// the client default headers, and whether the request uses the Claude Code
// identity.
type anthropicClient struct {
	// apiKey is sent as X-Api-Key when non-empty.
	apiKey string
	// authToken is sent as Authorization: Bearer when non-empty.
	authToken      string
	defaultHeaders anthropicHeaders
	isOAuthToken   bool
}

// createClient mirrors upstream createClient. The Copilot branch (UseBearerAuth)
// and the OAuth-token branch send the key as a bearer token; the OAuth-token
// branch also presents the Claude Code identity. Every other request sends the
// key as X-Api-Key, or leaves auth to the headers.
func (p *anthropicProvider) createClient(apiKey string, optionsHeaders, dynamicHeaders, sessionAffinityHeaders anthropicHeaders) anthropicClient {
	modelHeaders := anthropicHeadersFromMap(p.cfg.ExtraHeaders)
	var base anthropicHeaders
	base.set("accept", "application/json")
	base.set("anthropic-dangerous-direct-browser-access", "true")
	if p.cfg.UseBearerAuth {
		return anthropicClient{
			authToken:      apiKey,
			defaultHeaders: mergeAnthropicClientHeaders(base, modelHeaders, dynamicHeaders, optionsHeaders),
		}
	}
	if apiKey != "" && isOAuthToken(apiKey) {
		base.set("user-agent", "claude-cli/"+claudeCodeVersion)
		base.set("x-app", "cli")
		return anthropicClient{
			authToken:      apiKey,
			defaultHeaders: mergeAnthropicClientHeaders(base, modelHeaders, optionsHeaders),
			isOAuthToken:   true,
		}
	}
	return anthropicClient{
		apiKey:         apiKey,
		defaultHeaders: mergeAnthropicClientHeaders(base, sessionAffinityHeaders, modelHeaders, optionsHeaders),
	}
}

// authTokenHeaders mirrors the ANTHROPIC_AUTH_TOKEN branch of upstream
// anthropicApiKeyAuth().resolve. When the anthropic provider has no stored or
// configured key, the token travels as an Authorization bearer header, and the
// request keeps API-key request shaping.
func (p *anthropicProvider) authTokenHeaders(apiKey string, env ProviderEnv) anthropicHeaders {
	var headers anthropicHeaders
	if p.cfg.ProviderID != "anthropic" || apiKey != "" {
		return headers
	}
	if token := getProviderEnvValue(AnthropicAuthTokenEnv, env); token != "" {
		headers.set("Authorization", "Bearer "+token)
	}
	return headers
}

// applyAnthropicRequestHeaders layers request headers the way the Anthropic SDK
// buildHeaders does: SDK defaults, auth, client default headers, the JSON body
// content type, then the anthropic-beta header derived from params.betas.
func applyAnthropicRequestHeaders(request *http.Request, client anthropicClient, betas *string) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	request.Header.Set("anthropic-version", "2023-06-01")
	if client.apiKey != "" {
		request.Header.Set("X-Api-Key", client.apiKey)
	}
	if client.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+client.authToken)
	}
	for _, entry := range client.defaultHeaders.entries {
		if !entry.defined {
			continue
		}
		if strings.EqualFold(entry.name, "host") {
			if entry.value == nil {
				request.Host = ""
			} else {
				request.Host = *entry.value
			}
			continue
		}
		if entry.value == nil {
			request.Header.Del(entry.name)
			continue
		}
		request.Header.Set(entry.name, *entry.value)
	}
	request.Header.Set("Content-Type", "application/json")
	if betas != nil {
		request.Header.Set("anthropic-beta", *betas)
	}
}

// anthropicBetaInputs are the request facts upstream getBetaFeatures reads.
type anthropicBetaInputs struct {
	isOAuthToken                    bool
	hasTools                        bool
	supportsEagerToolInputStreaming bool
	reasoning                       bool
	thinkingEnabled                 bool
	forceAdaptiveThinking           bool
	supportsMidConvoEffort          bool
	// nativeToolChanges reports that the request carries tool_addition or
	// tool_removal blocks and deferred tool declarations.
	nativeToolChanges bool
}

// getBetaFeatures mirrors upstream getBetaFeatures. A configured anthropic-beta
// header (the last case-insensitive match across model headers, then request
// headers) replaces the computed list: null suppresses every beta, and a value
// is split on commas, trimmed, and deduplicated.
func getBetaFeatures(modelHeaders, optionsHeaders anthropicHeaders, inputs anthropicBetaInputs) []string {
	var configured *string
	found := false
	for _, headers := range []anthropicHeaders{modelHeaders, optionsHeaders} {
		for _, entry := range headers.entries {
			if entry.defined && strings.EqualFold(entry.name, "anthropic-beta") {
				configured, found = entry.value, true
			}
		}
	}
	if found {
		if configured == nil {
			return nil
		}
		var features []string
		for feature := range strings.SplitSeq(*configured, ",") {
			if feature = strings.TrimSpace(feature); feature != "" {
				features = append(features, feature)
			}
		}
		return uniqueStrings(features)
	}

	var features []string
	if inputs.isOAuthToken {
		features = append(features, claudeCodeBeta, oauthBeta)
	}
	if inputs.hasTools && !inputs.supportsEagerToolInputStreaming {
		features = append(features, fineGrainedToolStreamingBeta)
	}
	if inputs.reasoning && inputs.thinkingEnabled && !inputs.forceAdaptiveThinking {
		features = append(features, interleavedThinkingBeta)
	}
	if inputs.supportsMidConvoEffort {
		features = append(features, midConversationEffortBeta, thinkingBindingControlsBeta)
	}
	if inputs.nativeToolChanges {
		features = append(features, midConversationToolChangesBeta)
	}
	return uniqueStrings(features)
}

func uniqueStrings(values []string) []string {
	var unique []string
	for _, value := range values {
		if !slices.Contains(unique, value) {
			unique = append(unique, value)
		}
	}
	return unique
}

// anthropicWireBody splits request params into the HTTP body and the
// anthropic-beta header value. The SDK removes params.betas from the body and
// sends betas.toString(); absent betas send no header.
func anthropicWireBody(payload any) ([]byte, *string, error) {
	if request, ok := payload.(anthRequest); ok {
		var betas *string
		if request.Betas != nil {
			joined := strings.Join(request.Betas, ",")
			betas = &joined
		}
		request.Betas = nil
		body, err := json.Marshal(request)
		return body, betas, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return body, nil, nil
	}
	raw, ok := fields["betas"]
	if !ok {
		return body, nil, nil
	}
	delete(fields, "betas")
	var betas *string
	var list []string
	var single string
	switch {
	case json.Unmarshal(raw, &list) == nil && list != nil:
		joined := strings.Join(list, ",")
		betas = &joined
	case json.Unmarshal(raw, &single) == nil:
		betas = &single
	}
	body, err = json.Marshal(fields)
	return body, betas, err
}
