package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

type fetchTransport func(*http.Request) (*http.Response, error)

func (f fetchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func provider(api ai.API, url string) ai.Provider {
	switch api {
	case ai.APIAnthropicMessages:
		return ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIOpenAICompletions:
		return ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIOpenAIResponses:
		return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIAzureOpenAIResponses:
		return ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIMistralConversations:
		return ai.NewMistralProvider(ai.MistralConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIPiMessages:
		return ai.NewPiMessagesProvider(ai.PiMessagesConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIGoogleGenerativeAI:
		return ai.NewGoogleProvider(ai.GoogleConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIGoogleVertex:
		return ai.NewGoogleVertexProvider(ai.GoogleVertexConfig{APIKey: "test", Model: "test-model", BaseURL: url})
	case ai.APIOpenAICodexResponses:
		return ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{APIKey: "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`)) + ".signature", Model: "test-model", BaseURL: url})
	default:
		panic("unexpected API")
	}
}
func run() error {
	result := map[string]any{}
	for _, api := range []ai.API{ai.APIAnthropicMessages, ai.APIOpenAICompletions, ai.APIOpenAIResponses, ai.APIAzureOpenAIResponses, ai.APIMistralConversations, ai.APIOpenAICodexResponses, ai.APIPiMessages, ai.APIGoogleGenerativeAI, ai.APIGoogleVertex, "openrouter-images"} {
		var customCalls, fallbackCalls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		fetch := &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
			customCalls.Add(1)
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream rejected request"}}`)), Request: r}, nil
		})}
		var message string
		if api == "openrouter-images" {
			out := ai.GenerateImagesOpenRouter(context.Background(), ai.ImagesModel{ID: "test-model", API: ai.APIImagesOpenRouter, Provider: ai.ProviderImagesOpenRouter, BaseURL: server.URL, Output: []string{"image"}}, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "draw"}}}, ai.ProviderImagesOptions{APIKey: "test", Fetch: fetch})
			message = out.ErrorMessage
		} else {
			stream, err := provider(api, server.URL).Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}), ai.StreamOptions{Fetch: fetch, Transport: ai.TransportSSE})
			if err != nil {
				message = err.Error()
			} else {
				message = stream.Result().ErrorMessage
			}
		}
		server.Close()
		rejected := ""
		if strings.Contains(message, "Custom fetch is not supported") {
			rejected = message
		}
		result[string(api)] = map[string]any{"custom": customCalls.Load(), "fallback": fallbackCalls.Load(), "rejected": rejected}
	}
	cause := errors.New("Request aborted")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	sameSignal := false
	fetch := &http.Client{Transport: fetchTransport(func(request *http.Request) (*http.Response, error) {
		sameSignal = request.Context() == ctx
		return nil, cause
	})}
	aborted := ai.GenerateImagesOpenRouter(ctx, ai.ImagesModel{ID: "black-forest-labs/flux.2-pro", API: ai.APIImagesOpenRouter, Provider: ai.ProviderImagesOpenRouter, BaseURL: "https://openrouter.ai/api/v1", Output: []string{"image"}}, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "Generate a dog"}}}, ai.ProviderImagesOptions{APIKey: "test", Fetch: fetch})
	result["openrouter-images-aborted"] = map[string]any{"error": aborted.ErrorMessage, "reason": aborted.StopReason, "signal": sameSignal}
	var redirectCalls atomic.Int32
	manual := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: fetchTransport(func(request *http.Request) (*http.Response, error) {
		redirectCalls.Add(1)
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://redirect.test/landing"}}, Body: io.NopCloser(strings.NewReader("manual redirect")), Request: request}, nil
	})}
	// pi.mjs reads the rejection from the stream's result, as stream() returns before the request settles.
	redirectStream, err := provider(ai.APIOpenAICompletions, "https://request.test/v1").Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{Fetch: manual})
	if err != nil {
		return err
	}
	if redirectStream.Result().StopReason != ai.StopReasonError {
		return errors.New("manual redirect must remain an HTTP rejection")
	}
	result["openai-completions-manual-redirect"] = map[string]any{"custom": redirectCalls.Load()}
	return json.NewEncoder(os.Stdout).Encode(result)
}
