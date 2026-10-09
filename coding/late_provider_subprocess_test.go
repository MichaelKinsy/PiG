package coding

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A provider registered by a command after the factory finished keeps its images, classifiers and streamSimple, through the real Session, Model Runtime and Extension Host. Pi: `pi.registerProvider` takes the whole ProviderConfig at any time and, once the runner is bound, applies it at once (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803,1875-1903, loader.ts:449-457, runner.ts:517-523); the registry then runs the callbacks (core/model-runtime.ts:753-787, core/provider-composer.ts:632-663).
func TestLateProviderRegistrationKeepsOperationsThroughRuntime(t *testing.T) {
	services := newTestServices(t)
	session, err := NewSession(services, SessionOptions{Model: &ai.Model{ID: "primary", Provider: &scriptedProvider{}}, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	bridge := subprocess.NewUIBridge(func() {})
	detach := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{CurrentModel: session.Model, ModelLookup: services.ModelRuntime().GetModel, ModelCatalog: func(...string) []*ai.Model { return services.ModelRuntime().GetModels() }, Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return BuildModel(spec, services) }, SessionHandle: session})
	defer detach()
	host.SetUIBridge(bridge)
	ext, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "late-provider", Source: filepath.Join("testdata", "late-provider.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runtime := services.ModelRuntime()
	register := func(t *testing.T, command string) {
		t.Helper()
		if err := ext.Commands[command].Handler(t.Context(), ""); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	question := ai.ClassifierContext{State: ai.JsonObject{"text": "four"}, Questions: ai.ClassifierQuestions{
		{ID: "first", Question: ai.ClassifierBoolQuestion{Instructions: "One?"}},
		{ID: "second", Question: ai.ClassifierBoolQuestion{Instructions: "Two?"}},
	}}
	run := func(t *testing.T, provider, key string) {
		t.Helper()
		image, _ := runtime.GetModelOfType(ai.ModelTypeImage, provider, "flux").(*ai.ImageModel)
		classifier, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, provider, "cls").(*ai.ClassifierModel)
		chat := runtime.GetModel(provider, "chat")
		if image == nil || classifier == nil || chat == nil {
			t.Fatalf("models of %s: image %v, classifier %v, chat %v", provider, image, classifier, chat)
		}
		images := runtime.GenerateImages(t.Context(), image, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "a red circle"}}})
		if images.StopReason != ai.ImagesStopReasonStop || images.ResponseID != key || len(images.Output) != 1 || images.Output[0].(ai.ImageContent).Data != "img:flux:a red circle" {
			t.Fatalf("%s images = %+v", provider, images)
		}
		classified := runtime.Classify(t.Context(), classifier, question)
		if classified.StopReason != ai.ClassifierStopReasonStop || len(classified.Answers) != 2 || classified.Answers[0].ID != "second" || classified.Answers[0].Answer != (ai.ClassifierBoolAnswer{Probability: 0.04}) {
			t.Fatalf("%s classify = %+v", provider, classified)
		}
		streamed := runtime.Complete(t.Context(), chat, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
		if streamed.StopReason != ai.StopReasonStop || lateProviderText(streamed) != "late:chat:"+key {
			t.Fatalf("%s stream = %+v", provider, streamed)
		}
	}

	t.Run("pi.registerProvider", func(t *testing.T) {
		register(t, "late-pi")
		run(t, "late-pi", "late-key-pi")
	})
	t.Run("ctx.modelRegistry.registerProvider", func(t *testing.T) {
		register(t, "late-registry")
		run(t, "late-registry", "late-key-registry")
	})
	// model-runtime.ts:753-766: a registration merges its defined values over the previous one, so a second registration that defines no operation keeps the first's.
	t.Run("a later registration without operations keeps them", func(t *testing.T) {
		register(t, "late-twice")
		image, _ := runtime.GetModelOfType(ai.ModelTypeImage, "late-twice", "flux").(*ai.ImageModel)
		if image == nil {
			t.Fatal("the first registration's image model is gone")
		}
		if image.BaseURL != "https://late-second.test/v1" {
			t.Fatalf("the second registration's baseUrl = %q", image.BaseURL)
		}
		if images := runtime.GenerateImages(t.Context(), image, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "x"}}}); images.StopReason != ai.ImagesStopReasonStop || images.ResponseID != "late-key-first" {
			t.Fatalf("images after the second registration = %+v", images)
		}
	})
	// types.ts:1805-1819: unregisterProvider removes the provider and its models.
	t.Run("unregistering removes the provider", func(t *testing.T) {
		register(t, "late-gone")
		if got := runtime.GetModelOfType(ai.ModelTypeImage, "late-gone", "flux"); got != nil {
			t.Fatalf("an unregistered provider still lists %v", got)
		}
	})
}

func lateProviderText(message *ai.AssistantMessage) string {
	var text string
	for _, block := range message.Content {
		if part, ok := block.(ai.TextContent); ok {
			text += part.Text
		}
	}
	return text
}
