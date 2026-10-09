package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func main() {
	dir, err := os.MkdirTemp("", "sdk-options-")
	must(err)
	defer func() { must(os.RemoveAll(dir)) }()
	must(os.WriteFile(dir+"/settings.json", []byte(`{"httpIdleTimeoutMs":1234,"websocketConnectTimeoutMs":4321,"retry":{"provider":{"maxRetries":2,"maxRetryDelayMs":3000}}}`), 0o600))
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	must(err)
	var options ai.StreamOptions
	must(services.Registry().RegisterExtensionProvider("capture-provider", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://capture.invalid/v1", APIKey: "test-api-key", Headers: map[string]string{"x-provider": "provider"}, Models: []extension.ProviderModelConfig{{ID: "capture-model", Name: "Capture Model", Headers: map[string]string{"x-model": "model"}, ContextWindow: 128000, MaxTokens: 4096}}, StreamSimple: func(_ extension.Model, _ extension.AIContext, raw extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		options = raw
		stream := ai.NewAssistantMessageEventStream()
		stream.End(&ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, StopReason: ai.StopReasonStop})
		return stream
	}}))
	runner := inproc.NewRunner([]extension.Extension{{Path: "headers", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(args ...any) (any, error) {
		headers := args[0].(extension.BeforeProviderHeadersEvent).Headers
		headers["x-hook"] = new(*headers["x-provider"] + ":" + *headers["x-model"])
		return nil, nil
	}}}}}, dir)
	model, err := coding.BuildModel("capture-provider/capture-model", services)
	must(err)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true, SkipBuiltinTools: true})
	must(err)
	drained := make(chan struct{})
	go func() {
		for range session.Events() {
		}
		close(drained)
	}()
	_, err = session.Send(context.Background(), "test")
	must(err)
	must(session.Close())
	<-drained
	output := []any{options.TimeoutMs, options.WebSocketConnectTimeoutMs, options.MaxRetries, options.MaxRetryDelayMs, options.Headers["x-provider"], options.Headers["x-model"], options.Headers["x-hook"], options.TransformHeaders == nil}
	must(json.NewEncoder(os.Stdout).Encode(output))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
