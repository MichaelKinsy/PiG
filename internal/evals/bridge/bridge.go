// Package bridge connects the eval harness to the extension that runs inside the agent process under test.
//
// Pi's harness registers an inline extension and custom tools inside its own process (harness.ts:287-301, 345-355,
// createAgentSessionFromServices) and keeps the API key in that process's memory. A PiG Session runs as a separate
// pig process, so the harness serves these over a Unix socket: the system prompt transform, the custom tool executions
// and the API key. The extension binary (cmd/pig-eval-extension) is the client.
package bridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
)

// Environment names the variables the harness sets for the extension process.
const (
	// EnvSocket is the Unix socket the harness serves.
	EnvSocket = "PIG_EVAL_BRIDGE_SOCKET"
	// EnvSpec is the path of the JSON Spec file.
	EnvSpec = "PIG_EVAL_BRIDGE_SPEC"
)

// ExtensionName is the registered name of the bridge extension. PiG requires it to equal the extension executable's file
// name (Pi's inline extension is named "eval-system-prompt-transform", harness.ts:292).
const ExtensionName = "pig-eval-extension"

// Tool declares one custom tool the extension registers.
type Tool struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description"`
	// PromptSnippet is the tool's line in the system prompt's tool list (defineTool promptSnippet).
	PromptSnippet string         `json:"promptSnippet,omitempty"`
	Parameters    map[string]any `json:"parameters"`
	// ConstrainedSampling is the tool's constrainedSampling request, nil when absent.
	ConstrainedSampling *ConstrainedSampling `json:"constrainedSampling,omitempty"`
}

// ConstrainedSampling is a provider-side constrained sampling request.
type ConstrainedSampling struct {
	Type   string `json:"type"`
	Strict string `json:"strict,omitempty"`
}

// Spec is what the extension registers at startup.
type Spec struct {
	// TransformSystemPrompt registers a before_agent_start handler that sends the prompt to the harness.
	TransformSystemPrompt bool   `json:"transformSystemPrompt"`
	Tools                 []Tool `json:"tools"`
}

// Request is one extension-to-harness call.
type Request struct {
	Op           string         `json:"op"`
	SystemPrompt string         `json:"systemPrompt,omitempty"`
	Tool         string         `json:"tool,omitempty"`
	ToolCallID   string         `json:"toolCallId,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
}

// Operations of a Request.
const (
	OpTransform = "transform"
	OpTool      = "tool"
	// OpCredential returns the run's API key to the credential command.
	OpCredential = "credential"
)

// CredentialCommand is the argument that makes the extension binary print the run's API key and exit. The agent's
// auth.json names it in a "!" command, so the key never reaches a file.
const CredentialCommand = "credential"

// Response answers a Request. A non-empty Error is the harness handler's failure.
type Response struct {
	SystemPrompt string `json:"systemPrompt,omitempty"`
	Content      string `json:"content,omitempty"`
	Details      any    `json:"details,omitempty"`
	Terminate    bool   `json:"terminate,omitempty"`
	Credential   string `json:"credential,omitempty"`
	Error        string `json:"error,omitempty"`
}

// LoadSpec reads the Spec file named by EnvSpec.
func LoadSpec() (Spec, error) {
	path := os.Getenv(EnvSpec)
	if path == "" {
		return Spec{}, fmt.Errorf("%s is not set", EnvSpec)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, err
	}
	var spec Spec
	return spec, json.Unmarshal(data, &spec)
}

// Call sends request to the harness and returns its response.
func Call(request Request) (Response, error) {
	socket := os.Getenv(EnvSocket)
	if socket == "" {
		return Response{}, fmt.Errorf("%s is not set", EnvSocket)
	}
	// The harness chooses this local Unix socket; no network URL is resolved.
	//nolint:gosec // G704 models arbitrary network input, not a required local IPC endpoint.
	connection, err := net.Dial("unix", socket)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = connection.Close() }()
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(connection).ReadBytes('\n')
	if err != nil {
		return Response{}, err
	}
	var response Response
	if err := json.Unmarshal(line, &response); err != nil {
		return Response{}, err
	}
	if response.Error != "" {
		return Response{}, errors.New(response.Error)
	}
	return response, nil
}
