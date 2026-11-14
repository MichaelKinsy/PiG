package coding

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// connectionCounter is a local listener that counts the connections made to it and closes each one. As the HTTP proxy of a test process it sees every request the
// process sends to a provider host (a CONNECT or a plain request), whatever that host is: a request that reaches no listener is a request that was never sent.
type connectionCounter struct {
	addr  string
	count atomic.Int32
}

func newConnectionCounter(t *testing.T) *connectionCounter {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counter := &connectionCounter{addr: listener.Addr().String()}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			counter.count.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return counter
}

// routeAllRequestsTo makes every provider request of the test process go to counter: the environment proxy of the provider HTTP client.
func (counter *connectionCounter) routeAllRequestsTo(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(key, "http://"+counter.addr)
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
}

type unlistedBridge struct{ actions map[string]any }

func (bridge *unlistedBridge) SetHostAction(key string, action any) { bridge.actions[key] = action }
func (*unlistedBridge) PublishModelCatalog()                        {}

// unlistedModelEntries are the ways a program or an extension reaches a model by provider and id: the library's BuildModel, an extension's model stream and its complete (the host actions
// WireModelOperations installs, which resolve the named model through BuildModel).
func unlistedModelEntries(t *testing.T, services *AgentSessionServices, start *ai.Model) map[string]func(provider, id string) error {
	t.Helper()
	session, err := NewSession(services, SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: start})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	bridge := &unlistedBridge{actions: map[string]any{}}
	runtime := session.ModelRuntime()
	detach := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{
		CurrentModel: session.Model, ModelLookup: runtime.GetModel, ModelCatalog: runtime.GetModels, Registry: services.Registry().ModelRegistry,
		ModelBuilder: func(spec string) (*ai.Model, error) { return BuildModel(spec, services) }, SessionHandle: session,
	})
	t.Cleanup(detach)
	request := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello", "timestamp": 1}}}
	return map[string]func(provider, id string) error{
		"library BuildModel": func(provider, id string) error {
			model, err := BuildModel(provider+"/"+id, services)
			if model != nil {
				t.Errorf("BuildModel(%s/%s) returned a model %+v", provider, id, model)
			}
			return err
		},
		"extension model stream": func(provider, id string) error {
			stream, err := bridge.actions["streamModel"].(func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error))(t.Context(), map[string]any{"provider": provider, "modelId": id}, request)
			if stream != nil {
				t.Errorf("model stream of %s/%s returned a stream", provider, id)
			}
			return err
		},
		"extension model complete": func(provider, id string) error {
			result, err := bridge.actions["complete"].(func(context.Context, map[string]any, map[string]any, map[string]any) (map[string]any, error))(t.Context(), map[string]any{"provider": provider, "modelId": id}, request, nil)
			if result != nil {
				t.Errorf("model complete of %s/%s returned %v", provider, id, result)
			}
			return err
		},
	}
}

// completeOnce sends one request to model and returns whatever the provider answered; the context ends it as soon as the counting listener has seen its connection.
func completeOnce(t *testing.T, services *AgentSessionServices, model *ai.Model) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	services.ModelRuntime().CompleteSimple(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{MaxRetries: new(0)})
}

// A model nothing lists is no model: no entry that names one by provider and id makes a request, to the provider's host or to any other, with the provider's credential. The cell is the credential
// disclosure of v0.7.0: a bare object of an API-key provider went as OpenAI chat completions to api.openai.com with that provider's key. Each shape runs a control first (a listed model of the same provider
// does reach the counter), so zero connections after the unlisted ids is a measurement, not a proxy that was never consulted.
func TestUnlistedModelMakesNoConnection(t *testing.T) {
	for _, sh := range unlistedShapes() {
		t.Run(sh.name, func(t *testing.T) {
			counter := newConnectionCounter(t)
			counter.routeAllRequestsTo(t)
			sh.models = strings.ReplaceAll(sh.models, "https://acme.example/v1", "http://"+counter.addr+"/v1")
			services := unlistedServices(t, sh, "")
			listed := services.ModelRuntime().GetModel(sh.provider, sh.listedID)
			if listed == nil {
				t.Fatalf("the runtime does not list %s/%s", sh.provider, sh.listedID)
			}
			entries := unlistedModelEntries(t, services, listed)
			for name, call := range entries {
				for _, id := range []string{"not-listed-anywhere", "gpt-4o-mini", "gpt-5.4"} {
					if sh.listedID == id {
						continue
					}
					if err := call(sh.provider, id); err == nil || err.Error() != "Model not found: "+sh.provider+"/"+id {
						t.Errorf("%s of %s/%s: error %v, want 'Model not found: %s/%s'", name, sh.provider, id, err, sh.provider, id)
					}
				}
			}
			if got := counter.count.Load(); got != 0 {
				t.Fatalf("%d connections after the unlisted ids; want none", got)
			}
			model, err := BuildModel(sh.provider+"/"+sh.listedID, services)
			if err != nil {
				t.Fatal(err)
			}
			completeOnce(t, services, model)
			if counter.count.Load() == 0 {
				t.Fatalf("control: a request for the listed model %s/%s reached no counter, so the cell above measured nothing", sh.provider, sh.listedID)
			}
		})
	}
}

// A provider object that fills its API in itself (the Copilot shape) posts to its own host for whatever model it is handed; an id it does not list is never handed to it.
func TestUnlistedModelOfAProviderObjectMakesNoConnection(t *testing.T) {
	counter := newConnectionCounter(t)
	services := newRegistrationServices(t)
	post := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		events := ai.NewAssistantMessageEventStream()
		response, err := http.Post("http://"+counter.addr+"/chat/completions", "application/json", strings.NewReader("{}"))
		if err == nil {
			_ = response.Body.Close()
		}
		events.End(&ai.AssistantMessage{StopReason: ai.StopReasonStop})
		return events, nil
	}
	listed := &ai.Model{ID: "m1", ProviderMeta: ai.ProviderMetadata{ProviderID: "native", API: ai.APIOpenAICompletions}, Capabilities: ai.ModelCapabilities{ContextWindow: 128, MaxOutputTokens: 16}}
	if err := services.ModelRuntime().RegisterNativeProvider(&ai.ModelsProvider{ID: "native", Name: "native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{listed}, nil },
		Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "test"}}, nil
		}}},
		Stream: post, StreamSimple: post,
	}); err != nil {
		t.Fatal(err)
	}
	start := services.ModelRuntime().GetModel("native", "m1")
	for name, call := range unlistedModelEntries(t, services, start) {
		if err := call("native", "m2"); err == nil || err.Error() != "Model not found: native/m2" {
			t.Errorf("%s of native/m2: error %v, want 'Model not found: native/m2'", name, err)
		}
	}
	if got := counter.count.Load(); got != 0 {
		t.Fatalf("%d connections after the unlisted id; want none", got)
	}
	model, err := BuildModel("native/m1", services)
	if err != nil {
		t.Fatal(err)
	}
	completeOnce(t, services, model)
	if counter.count.Load() == 0 {
		t.Fatal("control: the listed model's request reached no counter")
	}
}
