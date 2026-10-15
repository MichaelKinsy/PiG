package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func main() {
	dir, err := os.MkdirTemp("", "metadata-refresh-")
	must(err)
	defer func() { must(os.RemoveAll(dir)) }()
	counter := filepath.Join(dir, "called")
	script := filepath.Join(dir, "key.cjs")
	must(os.WriteFile(script, []byte(`require("node:fs").appendFileSync(`+strconv.Quote(counter)+`,"called\n"); console.log("configured-key");`), 0o600))
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	must(err)
	must(services.Registry().RegisterExtensionProvider("metadata-refresh", extension.ProviderConfig{API: "openai-completions", BaseURL: "https://before.test", APIKey: "!node " + strconv.Quote(script), Models: []extension.ProviderModelConfig{{ID: "model", Name: "Model"}}}))
	runtime := services.ModelRuntime()
	ext := extension.Extension{Path: "metadata-refresh", Commands: map[string]extension.RegisteredCommand{"refresh-metadata": {Name: "refresh-metadata", Description: "Refresh metadata", Handler: func(context.Context, string) error {
		return services.Registry().RegisterExtensionProvider("metadata-refresh", extension.ProviderConfig{BaseURL: "https://after.test"})
	}}}}
	session, err := coding.NewSession(services, coding.SessionOptions{Runner: inproc.NewRunner([]extension.Extension{ext}, dir), Model: runtime.GetModel("metadata-refresh", "model"), NoSession: true, SkipBuiltinTools: true})
	must(err)
	_, err = session.Send(context.Background(), "/refresh-metadata")
	must(err)
	_, err = os.Stat(counter)
	ran := !os.IsNotExist(err)
	metadata := []any{session.Model().ProviderMeta.BaseURL, ran}
	must(session.Close())
	calls := 0
	var dispatch []string
	model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "native-refresh", API: ai.APIOpenAICompletions, BaseURL: "https://native.test"}}
	callback := func(method string) ai.ModelsStreamFunction {
		return func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			dispatch = append(dispatch, method+":"+options.APIKey)
			stream := ai.NewAssistantMessageEventStream()
			stream.End(&ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "end-only"}}, StopReason: ai.StopReasonStop})
			return stream, nil
		}
	}
	provider := &ai.ModelsProvider{ID: "native-refresh", Name: "Native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		calls++
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "native-key"}}, nil
	}}}, Stream: callback("stream"), StreamSimple: callback("streamSimple")}
	must(runtime.RegisterNativeProvider(provider))
	selected := runtime.GetModel(provider.ID, model.ID)
	before := calls
	var results []string
	for _, method := range []string{"stream", "streamSimple"} {
		var stream *ai.AssistantMessageEventStream
		if method == "stream" {
			stream = runtime.Stream(context.Background(), selected, ai.Context{}, ai.StreamOptions{})
		} else {
			stream = runtime.StreamSimple(context.Background(), selected, ai.Context{}, ai.StreamOptions{})
		}
		result := stream.Result()
		results = append(results, string(result.StopReason)+":"+ai.ContentText(result.Content))
	}
	must(json.NewEncoder(os.Stdout).Encode([]any{metadata, before, calls, dispatch, results}))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
