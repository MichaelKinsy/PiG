package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func BenchmarkSessionRegistryMetadataRefresh(b *testing.B) {
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(services.Close)
	model := services.ModelRuntime().GetModel("anthropic", "claude-sonnet-4-5")
	session, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := session.Close(); err != nil {
			b.Error(err)
		}
	})
	b.ReportAllocs()
	for b.Loop() {
		if err := services.Registry().RegisterProvider("anthropic", extension.ProviderConfig{BaseURL: "https://metadata.test"}); err != nil {
			b.Error(err)
		}
	}
}

func TestRegisteredModelSessionHeadersAssembleBeforeHook(t *testing.T) {
	services := newTestServices(t)
	var observed ai.ProviderHeaders
	runner := inproc.NewRunner([]extension.Extension{{Path: "headers", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(args ...any) (any, error) {
		observed = args[0].(extension.BeforeProviderHeadersEvent).Headers
		return nil, nil
	}}}}}, services.CWD())
	if err := services.Registry().RegisterProvider("header-refresh", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://headers.test", APIKey: "key", Headers: map[string]string{"provider": "provider"}, Models: []extension.ProviderModelConfig{{ID: "model", Name: "Model", Headers: map[string]string{"model": "model"}}}, StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		stream.End(&ai.AssistantMessage{StopReason: ai.StopReasonStop})
		return stream
	}}); err != nil {
		t.Error(err)
	}
	session, err := NewSession(services, SessionOptions{Model: services.ModelRuntime().GetModel("header-refresh", "model"), Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	drainSessionEvents(t, session)
	if _, err := session.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"provider", "model"} {
		if observed[name] == nil || *observed[name] != name {
			t.Errorf("header hook %q=%v", name, observed[name])
		}
	}
}

func TestSessionRegistryRefreshDoesNotResolveCredentials(t *testing.T) {
	// agent-session.ts:3006-3017 refreshes from getModel, which composes model data without preparing a request.
	dir := t.TempDir()
	counter := filepath.Join(dir, "resolved")
	script := filepath.Join(dir, "key.cjs")
	body := `require("node:fs").appendFileSync(` + strconv.Quote(counter) + `,"called\n"); console.log("configured-key");`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	providerID := "refresh-no-auth"
	config := extension.ProviderConfig{API: "openai-completions", BaseURL: "https://before.test", APIKey: "!node " + strconv.Quote(script), Models: []extension.ProviderModelConfig{{ID: "model", Name: "Model", ContextWindow: 128000, MaxTokens: 16384}}}
	if err := services.Registry().RegisterProvider(providerID, config); err != nil {
		t.Error(err)
	}
	model := services.ModelRuntime().GetModel(providerID, "model")
	if model == nil {
		t.Fatal("missing registered metadata")
	}
	session, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	before := len(session.Inner().Entries())
	if err := services.Registry().RegisterProvider(providerID, extension.ProviderConfig{BaseURL: "https://after.test"}); err != nil {
		t.Error(err)
	}
	if got := session.Model().ProviderMeta.BaseURL; got != "https://after.test" {
		t.Fatalf("metadata URL=%q", got)
	}
	if len(session.Inner().Entries()) != before {
		t.Fatal("metadata refresh appended a transcript entry")
	}
	if data, err := os.ReadFile(counter); !os.IsNotExist(err) {
		t.Fatalf("metadata refresh resolved credentials: %q, %v", data, err)
	}
	// Metadata defers work; it does not erase request auth or registry-local callbacks.
	if err := services.Registry().RegisterProvider(providerID, extension.ProviderConfig{API: ai.APIOpenAICompletions, StreamSimple: func(_ extension.Model, _ extension.AIContext, options extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		requestOptions := options.(ai.StreamOptions)
		if requestOptions.APIKey != "configured-key" {
			t.Errorf("request key=%q", requestOptions.APIKey)
		}
		stream := ai.NewAssistantMessageEventStream()
		stream.End(&ai.AssistantMessage{Model: "model", Provider: providerID, API: ai.APIOpenAICompletions, StopReason: ai.StopReasonStop})
		return stream
	}}); err != nil {
		t.Fatal(err)
	}
	result := session.ModelRuntime().StreamSimple(t.Context(), session.Model(), ai.Context{}, ai.StreamOptions{}).Result()
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("request failed: %+v", result)
	}
	if data, err := os.ReadFile(counter); err != nil || string(data) != "called\n" {
		t.Fatalf("request credential resolution=%q, %v", data, err)
	}
}

func TestNativeRefreshPreservesAuthAndBothCallbacks(t *testing.T) {
	services := newTestServices(t)
	const id = "native-refresh-auth"
	calls := 0
	streamCalls := []string{}
	model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: id, API: ai.APIOpenAICompletions, BaseURL: "https://native.test"}}
	stream := func(method string) ai.ModelsStreamFunction {
		return func(ctx context.Context, _ *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			if options.APIKey != "native-key" {
				t.Errorf("native key=%q", options.APIKey)
			}
			streamCalls = append(streamCalls, method)
			result := ai.NewAssistantMessageEventStream()
			result.End(&ai.AssistantMessage{Model: "model", Provider: id, API: ai.APIOpenAICompletions, StopReason: ai.StopReasonStop})
			return result, ctx.Err()
		}
	}
	// Registration schedules Pi's local availability refresh (model-runtime.ts:744-750). An explicit check keeps that refresh from falling back to request resolution, so calls counts request credentials only.
	native := &ai.ModelsProvider{ID: id, Name: "Native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
		return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "native"}, nil
	}, Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		calls++
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "native-key"}}, nil
	}}}, Stream: stream("stream"), StreamSimple: stream("streamSimple")}
	if err := services.ModelRuntime().RegisterNativeProvider(native); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: services.ModelRuntime().GetModel(id, "model"), NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := services.ModelRuntime().RegisterNativeProvider(native); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("native metadata resolved auth %d times", calls)
	}
	runtime := session.ModelRuntime()
	for _, result := range []*ai.AssistantMessage{runtime.Stream(t.Context(), session.Model(), ai.Context{}, ai.StreamOptions{}).Result(), runtime.StreamSimple(t.Context(), session.Model(), ai.Context{}, ai.StreamOptions{}).Result()} {
		if result.StopReason != ai.StopReasonStop {
			t.Fatalf("native result=%+v", result)
		}
	}
	data, _ := json.Marshal(streamCalls)
	if string(data) != `["stream","streamSimple"]` || calls != 2 {
		t.Fatalf("callbacks=%s auth calls=%d", data, calls)
	}
}
