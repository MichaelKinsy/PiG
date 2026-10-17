package requestvalidity

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// The contracts below state what each provider documents and answers with HTTP 400. Pi 1.1.0 packages/ai/src/api/transform-messages.ts closes unanswered calls and holds system messages, so a request built from a transformed transcript meets the tool-flow rules; a rule here that Pi's own converter does not meet is a finding against Pi.
var (
	// Anthropic Messages API: "tool_use ids were found without tool_result blocks immediately after", "unexpected tool_use_id found in tool_result blocks. Each tool_result block must have a corresponding tool_use block in the previous message", "tool_use ids must be unique"; tool_result blocks must come first in the user message; text content blocks must be non-empty; the id matches ^[a-zA-Z0-9_-]+$ (max 64); a final assistant message is a prefill that current models reject. A "system" message inside messages is the mid-conversation system message of the mid-conversation-output-config beta (anthropic-messages.ts MID_CONVERSATION_OUTPUT_CONFIG_BETA).
	Anthropic = Contract{
		API: "anthropic-messages", Cite: "https://docs.claude.com/en/api/messages (tool use, tool_result ordering, prefill)",
		Roles: []string{"system", "user", "assistant"}, ResultsInUserTurn: true, UniqueCallIDs: true,
		IDPattern: lazyregexp.New(`^[a-zA-Z0-9_-]{1,64}$`), NonEmpty: true, LastRoles: []string{"user"},
	}
	// Bedrock Converse: toolResult blocks for every toolUse of the previous assistant message, in the next user message ("Expected toolResult blocks at messages.N.content for Ids"); roles alternate and start with user; toolUseId matches [a-zA-Z0-9_-]+ (max 64); content blocks are non-empty.
	Bedrock = Contract{
		API: "bedrock-converse-stream", Cite: "https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html (ToolUseBlock, ToolResultBlock, Message)",
		Roles: []string{"user", "assistant"}, ResultsInUserTurn: true, UniqueCallIDs: true,
		IDPattern: lazyregexp.New(`^[a-zA-Z0-9_-]{1,64}$`), Alternate: true, FirstUser: true, NonEmpty: true, LastRoles: []string{"user"},
	}
	// OpenAI Chat Completions: the API answers "An assistant message with 'tool_calls' must be followed by tool messages responding to each 'tool_call_id'" and rejects a tool message that does not answer the tool_calls of the message before it.
	OpenAICompletions = Contract{
		API: "openai-completions", Cite: "https://platform.openai.com/docs/api-reference/chat/create (messages: assistant tool_calls, tool tool_call_id)",
		Roles: []string{"system", "developer", "user", "assistant", "tool"},
	}
	// Mistral chat: tool messages answer the tool_calls of the previous assistant message; the API answers a request whose last role is assistant with "Expected last role User or Tool (or Assistant with prefix True) for serving but got assistant". A user message after tool messages is not a rule: Pi's own Mistral end-to-end tests send it; the tool call id is exactly 9 alphanumeric characters.
	Mistral = Contract{
		API: "mistral-conversations", Cite: "https://docs.mistral.ai/capabilities/function_calling/ and the API's 400 text",
		Roles: []string{"system", "user", "assistant", "tool"}, LastRoles: []string{"user", "tool"},
		IDPattern: lazyregexp.New(`^[a-zA-Z0-9]{9}$`),
	}
	// OpenAI Responses (also Azure and Codex): every function_call needs a function_call_output with the same call_id ("No tool output found for function call"), and every output needs an earlier call ("No tool call found for function call output with call_id"). That an output follows its call's assistant items with nothing between them is Pi's transformMessages invariant (transform-messages.ts closePendingToolCalls), not a documented API error.
	OpenAIResponses = Contract{
		API: "openai-responses", Cite: "https://platform.openai.com/docs/api-reference/responses/create (input: function_call, function_call_output)",
		Roles: []string{"system", "developer", "user", "assistant", "tool"},
	}
	// Gemini generateContent: "Please ensure that the number of function response parts is equal to the number of function call parts of the function call turn", and "function call turn comes immediately after a user turn or after a function response turn"; parts must not be empty.
	Google = Contract{
		API: "google-generative-ai", Cite: "https://ai.google.dev/gemini-api/docs/function-calling (function response turn) and the API's 400 messages",
		Roles: []string{"user", "assistant"}, ResultsInUserTurn: true, CallsFollowUser: true, NonEmpty: true,
	}
	// Transcript is Pi's own message contract after transformMessages: results follow the assistant message that called them, with nothing between, and no call goes unanswered. The pi-messages backend and the faux providers receive this shape.
	Transcript = Contract{
		API: "transcript", Cite: ".upstream/current/packages/ai/src/api/transform-messages.ts (closePendingToolCalls)",
		Roles: []string{"system", "user", "assistant", "tool"},
	}
)

// ForAPI returns the contract of a PiG API.
func ForAPI(api string) (Contract, bool) {
	switch api {
	case "anthropic-messages":
		return Anthropic, true
	case "bedrock-converse-stream":
		return Bedrock, true
	case "openai-completions":
		return OpenAICompletions, true
	case "mistral-conversations":
		return Mistral, true
	case "openai-responses", "azure-openai-responses", "openai-codex-responses":
		contract := OpenAIResponses
		contract.API = api
		return contract, true
	case "google-generative-ai", "google-vertex":
		contract := Google
		contract.API = api
		return contract, true
	}
	return Contract{}, false
}

// CheckBody parses a provider's JSON request body and returns the contract violations of its message list. A body that is not the API's shape is itself a violation.
func CheckBody(api string, body []byte) []Violation {
	contract, ok := ForAPI(api)
	if !ok {
		return []Violation{{Rule: "api-known", Detail: fmt.Sprintf("no message contract for api %q", api)}}
	}
	var turns []Turn
	var err error
	switch api {
	case "anthropic-messages":
		turns, err = anthropicTurns(body)
	case "openai-completions":
		turns, err = chatTurns(body)
	case "mistral-conversations":
		turns, err = chatTurns(body)
	case "openai-responses", "azure-openai-responses", "openai-codex-responses":
		turns, err = responsesTurns(body)
	case "google-generative-ai", "google-vertex":
		turns, err = googleTurns(body)
	default:
		return []Violation{{Rule: "api-known", Detail: fmt.Sprintf("api %q has no JSON body projection", api)}}
	}
	if err != nil {
		return []Violation{{Rule: "body-shape", Detail: err.Error()}}
	}
	return contract.Check(turns)
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	Text      *string         `json:"text"`
	Content   json.RawMessage `json:"content"`
}

func anthropicTurns(body []byte) ([]Turn, error) {
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	turns := make([]Turn, 0, len(request.Messages))
	for _, message := range request.Messages {
		turn := Turn{Role: message.Role}
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			turn.Empty = text == ""
			turns = append(turns, turn)
			continue
		}
		var blocks []contentBlock
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return nil, fmt.Errorf("anthropic message content: %w", err)
		}
		turn.Empty = len(blocks) == 0
		other := false
		for _, block := range blocks {
			switch block.Type {
			case "tool_use", "server_tool_use":
				turn.Calls = append(turn.Calls, block.ID)
			case "tool_result":
				if other {
					turn.ContentBeforeResult = true
				}
				turn.Results = append(turn.Results, block.ToolUseID)
			case "text":
				other = true
				if block.Text != nil && *block.Text == "" {
					turn.Empty = true
				}
			default:
				other = true
			}
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

func chatTurns(body []byte) ([]Turn, error) {
	var request struct {
		Messages []struct {
			Role        string `json:"role"`
			ToolCallID  string `json:"tool_call_id"`
			ToolCallID2 string `json:"toolCallId"`
			ToolCalls   []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
			ToolCalls2 []struct {
				ID string `json:"id"`
			} `json:"toolCalls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	turns := make([]Turn, 0, len(request.Messages))
	for _, message := range request.Messages {
		turn := Turn{Role: message.Role}
		for _, call := range message.ToolCalls {
			turn.Calls = append(turn.Calls, call.ID)
		}
		for _, call := range message.ToolCalls2 {
			turn.Calls = append(turn.Calls, call.ID)
		}
		if id := message.ToolCallID + message.ToolCallID2; message.Role == "tool" {
			turn.Results = []string{id}
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

func responsesTurns(body []byte) ([]Turn, error) {
	var request struct {
		Input []struct {
			Type   string `json:"type"`
			Role   string `json:"role"`
			CallID string `json:"call_id"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("responses request: %w", err)
	}
	// Input items carry no assistant-message boundary: Pi's converter emits the reasoning, message and call items of one assistant message in block order (openai-responses-shared.ts convertResponsesMessages), so consecutive assistant items form one turn, whatever their order.
	var turns []Turn
	assistant := func(calls ...string) {
		if n := len(turns); n > 0 && turns[n-1].Role == "assistant" {
			turns[n-1].Calls = append(turns[n-1].Calls, calls...)
			return
		}
		turns = append(turns, Turn{Role: "assistant", Calls: calls})
	}
	for _, item := range request.Input {
		switch item.Type {
		case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call":
			assistant(item.CallID)
		case "function_call_output", "custom_tool_call_output", "tool_search_output", "local_shell_call_output":
			turns = append(turns, Turn{Role: "tool", Results: []string{item.CallID}})
		case "reasoning":
			assistant()
		case "additional_tools":
			turns = append(turns, Turn{Role: "developer"})
		default:
			if item.Role == "" || item.Role == "assistant" {
				assistant()
			} else {
				turns = append(turns, Turn{Role: item.Role})
			}
		}
	}
	return turns, nil
}

func googleTurns(body []byte) ([]Turn, error) {
	var request struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				FunctionCall *struct {
					ID string `json:"id"`
				} `json:"functionCall"`
				FunctionResponse *struct {
					ID string `json:"id"`
				} `json:"functionResponse"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("google request: %w", err)
	}
	turns := make([]Turn, 0, len(request.Contents))
	for _, content := range request.Contents {
		role := content.Role
		if role == "model" {
			role = "assistant"
		}
		turn := Turn{Role: role, Empty: len(content.Parts) == 0}
		for _, part := range content.Parts {
			// Gemini ids are optional; without one, calls and responses pair by position.
			if part.FunctionCall != nil {
				turn.Calls = append(turn.Calls, positionalID(part.FunctionCall.ID, len(turn.Calls)))
			}
			if part.FunctionResponse != nil {
				turn.Results = append(turn.Results, positionalID(part.FunctionResponse.ID, len(turn.Results)))
			}
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

func positionalID(id string, position int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("#%d", position)
}
