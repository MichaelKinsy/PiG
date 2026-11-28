package ai

// Ports packages/ai/src/api/openai-completions-reasoning.ts:
// Reasoning replay data of the openai-completions API: the reasoning fields, OpenRouter's `reasoning_details`,
// GitHub Copilot's `reasoning_opaque`, and how they are stored in a thinking block's `thinkingSignature`.

import (
	"encoding/json"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsonwire"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// openAICompletionsReasoningFields are the assistant message fields that carry plain reasoning text,
// in the order the stream prefers them.
var openAICompletionsReasoningFields = []string{"reasoning_content", "reasoning", "reasoning_text"}

type completionsThinkingSignatureKind string

const (
	completionsThinkingSignatureField   completionsThinkingSignatureKind = "field"
	completionsThinkingSignatureDetails completionsThinkingSignatureKind = "details"
	completionsThinkingSignatureOpaque  completionsThinkingSignatureKind = "opaque"
)

// completionsThinkingSignature is the decoded replay data stored in a thinking block's `thinkingSignature`.
type completionsThinkingSignature struct {
	kind    completionsThinkingSignatureKind
	field   string
	details []json.RawMessage
	opaque  string
}

func isOpenAICompletionsReasoningField(field string) bool {
	return slices.Contains(openAICompletionsReasoningFields, field)
}

// decodeCompletionsThinkingSignature decodes a stored `thinkingSignature`; returns nil when it is empty
// or not one of the known formats.
func decodeCompletionsThinkingSignature(signature string) *completionsThinkingSignature {
	if signature == "" {
		return nil
	}
	if isOpenAICompletionsReasoningField(signature) {
		return &completionsThinkingSignature{kind: completionsThinkingSignatureField, field: signature}
	}
	switch signature[0] {
	case '[':
		details := parseOpenAIReasoningDetails(signature)
		if len(details) == 0 {
			return nil
		}
		return &completionsThinkingSignature{kind: completionsThinkingSignatureDetails, details: details}
	case '{':
		return decodeOpaqueThinkingSignature(signature)
	default:
		return nil
	}
}

func decodeOpaqueThinkingSignature(signature string) *completionsThinkingSignature {
	if !jsonwire.Valid(signature) {
		return nil
	}
	var parsed map[string]json.RawMessage
	if json.Unmarshal([]byte(signature), &parsed) != nil {
		return nil
	}
	opaqueRaw, hasOpaque := parsed["reasoning_opaque"]
	if !hasOpaque || len(opaqueRaw) == 0 || opaqueRaw[0] != '"' {
		return nil
	}
	var opaque string
	if json.Unmarshal(opaqueRaw, &opaque) != nil || opaque == "" {
		return nil
	}
	fieldRaw, hasField := parsed["field"]
	if !hasField {
		return &completionsThinkingSignature{kind: completionsThinkingSignatureOpaque, opaque: opaque}
	}
	if len(fieldRaw) == 0 || fieldRaw[0] != '"' {
		return nil
	}
	var field string
	if json.Unmarshal(fieldRaw, &field) != nil || !isOpenAICompletionsReasoningField(field) {
		return nil
	}
	return &completionsThinkingSignature{kind: completionsThinkingSignatureOpaque, field: field, opaque: opaque}
}

type opaqueThinkingSignatureWire struct {
	Field           string `json:"field,omitempty"`
	ReasoningOpaque string `json:"reasoning_opaque"`
}

// encodeCompletionsThinkingSignature encodes replay data for `thinkingSignature`,
// in the format `decodeCompletionsThinkingSignature` reads.
func encodeCompletionsThinkingSignature(sig completionsThinkingSignature) string {
	switch sig.kind {
	case completionsThinkingSignatureField:
		return sig.field
	case completionsThinkingSignatureDetails:
		details := sig.details
		if details == nil {
			details = []json.RawMessage{}
		}
		encoded, err := jsstring.MarshalJSON(details)
		if err != nil {
			return ""
		}
		return string(encoded)
	case completionsThinkingSignatureOpaque:
		encoded, err := jsstring.MarshalJSON(opaqueThinkingSignatureWire{
			Field:           sig.field,
			ReasoningOpaque: sig.opaque,
		})
		if err != nil {
			return ""
		}
		return string(encoded)
	default:
		return ""
	}
}

// decodeLegacyToolCallReasoningDetail reads the encrypted `reasoning_details` entry that older sessions
// stored in a tool call's `thoughtSignature` before details moved to the thinking block.
func decodeLegacyToolCallReasoningDetail(thoughtSignature string) json.RawMessage {
	if len(thoughtSignature) == 0 || thoughtSignature[0] != '{' {
		return nil
	}
	return parseLegacyOpenAIReasoningDetail(thoughtSignature)
}

// resolveOpenAICompletionsReasoningReplay resolves the reasoning replay data of one assistant message:
// the first `reasoning_details` (or, for older sessions, the encrypted details on its tool calls),
// else the latest Copilot `reasoning_opaque`, else the reasoning field of the first thinking block with text.
func resolveOpenAICompletionsReasoningReplay(thinkingBlocks []ThinkingContent, legacyDetails []json.RawMessage) *completionsThinkingSignature {
	signatures := make([]*completionsThinkingSignature, len(thinkingBlocks))
	for i, block := range thinkingBlocks {
		signatures[i] = decodeCompletionsThinkingSignature(block.ThinkingSignature)
	}
	for _, sig := range signatures {
		if sig != nil && sig.kind == completionsThinkingSignatureDetails {
			return sig
		}
	}
	if len(legacyDetails) > 0 {
		return &completionsThinkingSignature{kind: completionsThinkingSignatureDetails, details: legacyDetails}
	}
	for _, signature := range slices.Backward(signatures) {
		if signature != nil && signature.kind == completionsThinkingSignatureOpaque {
			return signature
		}
	}
	for i, block := range thinkingBlocks {
		if trimJSWhitespace(block.Thinking) != "" {
			if signatures[i] != nil && signatures[i].kind == completionsThinkingSignatureField {
				return signatures[i]
			}
			return nil
		}
	}
	return nil
}
