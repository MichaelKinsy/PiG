package evals

// Ports packages/evals/evals/acme-server.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Fixture identities of the OpenAI-compatible (openai) and custom NDJSON (stream) Acme APIs.
const (
	OpenAIProviderID    = "acme"
	OpenAIModelID       = "acme-chat"
	OpenAIProbePrompt   = "Reply with ACME_OK."
	OpenAIProbeResponse = "ACME_OK"
	StreamProviderID    = "acme-stream"
	StreamModelID       = "acme-stream-chat"
	StreamProbePrompt   = "Reply with ACME_STREAM_OK."
	StreamProbeResponse = "ACME_STREAM_OK"
)

// StreamAPIDocumentation describes the stream fixture's API to the agent under evaluation.
const StreamAPIDocumentation = `{
  "name": "Acme Streaming API",
  "request": {
    "method": "POST",
    "path": "/generate",
    "headers": {
      "content-type": "application/json",
      "x-acme-key": "resolved credential"
    },
    "body": {
      "model": "` + StreamModelID + `",
      "messages": [
        {
          "role": "user",
          "content": "Hello"
        }
      ],
      "stream": true
    }
  },
  "response": {
    "contentType": "application/x-ndjson",
    "events": [
      {
        "type": "text_delta",
        "text": "Hello"
      },
      {
        "type": "usage",
        "input_tokens": 3,
        "output_tokens": 2
      },
      {
        "type": "done",
        "reason": "stop"
      }
    ]
  }
}
`

// AcmeServerMode selects the fixture API.
type AcmeServerMode string

const (
	AcmeServerModeOpenAI AcmeServerMode = "openai"
	AcmeServerModeStream AcmeServerMode = "stream"
)

// AcmeServer is a loopback HTTP fixture that accepts only correctly authenticated, well-formed streaming requests
// and records whether the last accepted request was the probe prompt.
type AcmeServer struct {
	mode                 AcmeServerMode
	mu                   sync.Mutex
	server               *http.Server
	serverOrigin         string
	receivedValidRequest atomic.Bool
}

// CreateAcmeServer returns a stopped fixture server for mode.
func CreateAcmeServer(mode AcmeServerMode) *AcmeServer { return &AcmeServer{mode: mode} }

func rejectRequest(response http.ResponseWriter, status int, message string) {
	response.Header().Set("content-type", "text/plain")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, message)
}

func jsonLine(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

type openAIChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIChunkChoice struct {
	Index        int              `json:"index"`
	Delta        openAIChunkDelta `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
}

type openAIChunkUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type openAIChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int                 `json:"created"`
	Model   string              `json:"model"`
	Choices []openAIChunkChoice `json:"choices"`
	Usage   *openAIChunkUsage   `json:"usage,omitempty"`
}

func (server *AcmeServer) handle(response http.ResponseWriter, request *http.Request) {
	expectedPath := "/generate"
	if server.mode == AcmeServerModeOpenAI {
		expectedPath = "/v1/chat/completions"
	}
	if request.RequestURI != expectedPath {
		rejectRequest(response, http.StatusNotFound, "Unknown endpoint")
		return
	}
	if request.Method != http.MethodPost {
		rejectRequest(response, http.StatusMethodNotAllowed, "Expected POST")
		return
	}
	if !strings.HasPrefix(request.Header.Get("content-type"), "application/json") {
		rejectRequest(response, http.StatusUnsupportedMediaType, "Expected application/json")
		return
	}
	body, err := io.ReadAll(request.Body)
	var payload any
	if err != nil || json.Unmarshal(body, &payload) != nil {
		rejectRequest(response, http.StatusBadRequest, "Invalid JSON")
		return
	}
	record, ok := payload.(map[string]any)
	if !ok {
		rejectRequest(response, http.StatusUnprocessableEntity, "Expected a JSON object")
		return
	}
	messages, _ := record["messages"].([]any)
	var prompt *string
	for _, message := range messages {
		if message, ok := message.(map[string]any); ok && message["role"] == "user" {
			if content, ok := message["content"].(string); ok {
				prompt = &content
			}
			break
		}
	}

	if server.mode == AcmeServerModeStream {
		// Node joins repeated custom request headers with ", ".
		if key, present := request.Header["X-Acme-Key"]; !present || strings.Join(key, ", ") != "resolved-stream-key" {
			rejectRequest(response, http.StatusUnauthorized, "Invalid Acme Stream credential")
			return
		}
		if record["model"] != StreamModelID || prompt == nil || record["stream"] != true {
			rejectRequest(response, http.StatusUnprocessableEntity, "Invalid Acme Stream request")
			return
		}
		server.receivedValidRequest.Store(*prompt == StreamProbePrompt)
		response.Header().Set("content-type", "application/x-ndjson")
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, `{"type":"text_delta","text":"ACME_"}`+"\n")
		_, _ = io.WriteString(response, `{"type":"text_delta","text":"STREAM_OK"}`+"\n")
		_, _ = io.WriteString(response, `{"type":"usage","input_tokens":4,"output_tokens":3}`+"\n")
		_, _ = io.WriteString(response, `{"type":"done","reason":"stop"}`+"\n")
		return
	}

	if request.Header.Get("authorization") != "Bearer resolved-acme-key" {
		rejectRequest(response, http.StatusUnauthorized, "Invalid Acme credential")
		return
	}
	if record["model"] != OpenAIModelID || prompt == nil || record["stream"] != true {
		rejectRequest(response, http.StatusUnprocessableEntity, "Invalid OpenAI-compatible request")
		return
	}
	server.receivedValidRequest.Store(*prompt == OpenAIProbePrompt)
	response.Header().Set("content-type", "text/event-stream")
	response.Header().Set("cache-control", "no-cache")
	response.WriteHeader(http.StatusOK)
	stop := "stop"
	_, _ = io.WriteString(response, "data: "+jsonLine(openAIChunk{
		ID: "chatcmpl-acme", Object: "chat.completion.chunk", Model: OpenAIModelID,
		Choices: []openAIChunkChoice{{Delta: openAIChunkDelta{Role: "assistant", Content: OpenAIProbeResponse}}},
	})+"\n\n")
	_, _ = io.WriteString(response, "data: "+jsonLine(openAIChunk{
		ID: "chatcmpl-acme", Object: "chat.completion.chunk", Model: OpenAIModelID,
		Choices: []openAIChunkChoice{{FinishReason: &stop}},
		Usage:   &openAIChunkUsage{PromptTokens: 3, CompletionTokens: 2},
	})+"\n\n")
	_, _ = io.WriteString(response, "data: [DONE]\n\n")
}

// Start listens on an ephemeral loopback port.
func (server *AcmeServer) Start() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return errors.New("Acme fixture server did not bind a TCP port.")
	}
	httpServer := &http.Server{Handler: http.HandlerFunc(server.handle)}
	server.mu.Lock()
	server.server = httpServer
	server.serverOrigin = "http://127.0.0.1:" + strconv.Itoa(address.Port)
	server.mu.Unlock()
	go func() { _ = httpServer.Serve(listener) }()
	return nil
}

// Stop stops accepting connections and waits for in-flight requests, as Node's server.close does.
func (server *AcmeServer) Stop(ctx context.Context) error {
	server.mu.Lock()
	httpServer := server.server
	server.server = nil
	server.mu.Unlock()
	if httpServer == nil {
		return nil
	}
	return httpServer.Shutdown(ctx)
}

// Reset clears the probe flag.
func (server *AcmeServer) Reset() { server.receivedValidRequest.Store(false) }

// Origin is the server's http://127.0.0.1:<port> origin, or "" before Start.
func (server *AcmeServer) Origin() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.serverOrigin
}

// BaseURL is the OpenAI-compatible base URL, Origin()+"/v1".
func (server *AcmeServer) BaseURL() string { return server.Origin() + "/v1" }

// ValidRequestReceived reports whether the last accepted request was the probe.
func (server *AcmeServer) ValidRequestReceived() bool { return server.receivedValidRequest.Load() }
