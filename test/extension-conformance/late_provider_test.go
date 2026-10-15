package extensionconformance

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// A provider registered after the factory finished behaves like one registered in it: Pi's pi.registerProvider takes the whole ProviderConfig, callbacks included, at any time (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803,1875-1903, loader.ts:449-457, runner.ts:517-523). Each SDK registers the provider from a tool through ctx.modelRegistry.registerProvider. The host must hold a ProviderConfig whose images, classifiers and streamSimple run in the extension. Every value asserted is one the extension's callbacks produce: the key the host resolved, the prompt, the reversed question order, and the model and key in the streamed text.

// lateRegistrations returns the configs the host applied for provider "late" after the tool ran.
func (r *modelTypesRig) lateRegistrations(t *testing.T) []extension.ProviderConfig {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var configs []extension.ProviderConfig
	for _, registration := range r.registrations {
		if registration.Name == "late" {
			configs = append(configs, registration.Config)
		}
	}
	return configs
}

func runLateOperations(t *testing.T, config extension.ProviderConfig) {
	t.Helper()
	images, classifier := config.Images["late-images"], config.Classifiers["late-classifier"]
	if images == nil || images.GenerateImages == nil || classifier == nil || classifier.Classify == nil || config.StreamSimple == nil {
		t.Fatalf("the late provider lost an implementation: images = %+v, classifiers = %+v, streamSimple set = %v", config.Images, config.Classifiers, config.StreamSimple != nil)
	}
	imageModel := &ai.ImageModel{ID: "flux", Name: "Flux", API: "late-images", Provider: "late", BaseURL: "https://late.test/v1", Input: []string{"text"}, Output: []string{"image"}}
	generated, err := images.GenerateImages(t.Context(), imageModel, modelTypesPrompt, ai.ImagesOptions{APIKey: "sk-late"})
	if err != nil {
		t.Fatal(err)
	}
	if generated.StopReason != ai.ImagesStopReasonStop || generated.Model != "flux" || generated.ResponseID != "sk-late" || len(generated.Output) != 1 || generated.Output[0].(ai.ImageContent).Data != "img:flux:a red circle" {
		t.Fatalf("image result = %+v", generated)
	}
	if _, err := images.GenerateImages(t.Context(), imageModel, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "fail"}}}, ai.ImagesOptions{}); err == nil || !strings.Contains(err.Error(), "image failed") {
		t.Fatalf("an extension error must be the call's error: %v", err)
	}
	classifierModel := &ai.ClassifierModel{ID: "cls", Name: "Cls", API: "late-classifier", Provider: "late", BaseURL: "https://late.test/v1", Input: []string{"text"}, ContextWindow: 1000}
	classified, err := classifier.Classify(t.Context(), classifierModel, modelTypesClassifierContext("four"), ai.ClassifierOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, entry := range classified.Answers {
		order = append(order, entry.ID)
	}
	if classified.StopReason != ai.ClassifierStopReasonStop || !reflect.DeepEqual(order, []string{"third", "second", "first"}) || classified.Model != "cls" {
		t.Fatalf("classifier result = %+v (answer order %v)", classified, order)
	}
	chat := &ai.Model{ID: "chat", ProviderMeta: ai.ProviderMetadata{ProviderID: "late", API: "late-chat-api", BaseURL: "https://late.test/v1"}}
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}})
	stream := config.StreamSimple(chat, transcript, ai.StreamOptions{APIKey: "sk-stream"})
	message := stream.Result()
	if message == nil || message.StopReason != ai.StopReasonStop || len(message.Content) != 1 || message.Content[0].(ai.TextContent).Text != "late:chat:sk-stream" {
		t.Fatalf("stream result = %+v", message)
	}
}

func TestLateProviderKeepsOperationsAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		r.toolWith("late_provider", map[string]any{"mode": "register"})
		configs := r.lateRegistrations(t)
		if len(configs) != 1 {
			t.Fatalf("the host applied %d registrations of the late provider", len(configs))
		}
		if len(configs[0].Models) != 3 || configs[0].BaseURL != "https://late.test/v1" {
			t.Fatalf("late config = %+v", configs[0])
		}
		runLateOperations(t, configs[0])
	})
}

// model-runtime.ts:753-766: a later registration merges its defined values over the earlier one, so one that defines no operation wires none, and the earlier registration's callbacks keep running in the extension.
func TestLateProviderReRegistrationKeepsOperationsAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		r.toolWith("late_provider", map[string]any{"mode": "again"})
		configs := r.lateRegistrations(t)
		if len(configs) != 2 {
			t.Fatalf("the host applied %d registrations of the late provider", len(configs))
		}
		if configs[1].BaseURL != "https://late.test/v2" {
			t.Fatalf("second config = %+v", configs[1])
		}
		if configs[1].Images != nil && configs[1].Images["late-images"] == nil {
			t.Fatalf("second config images = %+v", configs[1].Images)
		}
		runLateOperations(t, configs[0])
	})
}

// types.ts:1805-1819: unregisterProvider removes the registration; the extension drops the callbacks it held, so the host's calls through the removed configuration fail instead of running an implementation of a provider that no longer exists.
func TestLateProviderUnregisterDropsOperationsAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		r.toolWith("late_provider", map[string]any{"mode": "gone"})
		configs := r.lateRegistrations(t)
		if len(configs) != 1 {
			t.Fatalf("the host applied %d registrations of the late provider", len(configs))
		}
		images := configs[0].Images["late-images"]
		if images == nil || images.GenerateImages == nil {
			t.Fatalf("images = %+v", configs[0].Images)
		}
		imageModel := &ai.ImageModel{ID: "flux", API: "late-images", Provider: "late"}
		if result, err := images.GenerateImages(context.Background(), imageModel, modelTypesPrompt, ai.ImagesOptions{}); err == nil {
			t.Fatalf("an unregistered provider's implementation ran: %+v", result)
		}
	})
}

// model-runtime.ts:921-940: Pi keeps one effective registration per provider and merges a later registration's defined values over it, whichever extension makes it. A partial re-registration by another extension process therefore leaves the earlier registrant's streamSimple, images and classifiers in the effective registration, and the host keeps calling them in the earlier registrant's process after it told that process provider_superseded. The successor runs in its own process and defines no operation, so every value asserted comes from the superseded author's callbacks.
// The authors are the native SDKs, whose registration no successor merges. A Node author is not in this row: the Node runtime's provider_superseded handler drops those callbacks (runtime.mjs forgetProviderOwnership), so a Node author superseded by a successor that does not merge its root fails it.
func TestLateProviderSupersededByAnotherExtensionKeepsOperationsAcrossSDKs(t *testing.T) {
	eachModelTypesPlacementOf(t, []string{"go", "rust", "python"}, func(t *testing.T, r *modelTypesRig) {
		r.toolWith("late_provider", map[string]any{"mode": "register"})
		successor := filepath.Join(t.TempDir(), "late-successor.mjs")
		if err := os.WriteFile(successor, []byte(`export default function (pi) { pi.registerProvider("late", { baseUrl: "https://late.test/v2" }); }`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, _, err := subprocess.ResolveExtConfigWithIdentity(successor, "late-successor")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Isolation = "isolated"
		if _, err := r.host.Load(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		configs := r.lateRegistrations(t)
		if len(configs) != 2 || configs[1].BaseURL != "https://late.test/v2" || configs[1].StreamSimple != nil || configs[1].Images != nil || configs[1].Classifiers != nil {
			t.Fatalf("the successor's registration = %+v (of %d)", configs[len(configs)-1], len(configs))
		}
		runLateOperations(t, configs[0])
	})
}
