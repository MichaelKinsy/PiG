package extensionconformance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:169-205 "uses the model selected by before_agent_start for image
// normalization" (https://github.com/earendil-works/pi/issues/9631), through the extension wire of every SDK. An extension's before_agent_start handler
// passes pi.setModel a registry model on which it set inputLimits; the prompt's image is normalized for that object: the registry's model of the same
// provider and id has no limits, so only the object the extension passed makes the 64x64 image arrive at 16x16.
func TestSetModelObjectFromBeforeAgentStartAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	picture := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			picture.Set(x, y, color.RGBA{R: uint8(4 * x), G: uint8(4 * y), B: 128, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	prompt := &coding.PromptOptions{Images: []ai.ImageContent{{Data: base64.StdEncoding.EncodeToString(encoded.Bytes()), MimeType: "image/png"}}}
	for _, sdk := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(sdk.name, func(t *testing.T) {
			h := sdk.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + sdk.name)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			configureFauxAuth(t, services)
			faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Models: []ai.FauxModelDefinition{
				{ID: "wide", Input: []string{"text", "image"}}, {ID: "strict", Input: []string{"text", "image"}},
			}})
			if err := services.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
				t.Fatal(err)
			}
			if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted {
				t.Fatalf("refresh=%v", refreshed)
			}
			if !services.ModelRuntime().HasConfiguredAuth("faux") {
				t.Fatal("the faux provider has no configured auth")
			}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: services.ModelRuntime().GetModel("faux", "wide"), Runner: h.runner, NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			// The fixture's other probes subscribe to events of their own tests; their complaints about this session are not this row's.
			h.runner.AddErrorListener(func(err *extension.ExtensionError) { t.Logf("extension error: %s: %s", err.Event, err.Error) })
			events := make(chan struct{})
			eventsCh := session.Events()
			go func() {
				defer close(events)
				for event := range eventsCh {
					coding.AcknowledgeEvent(event)
				}
			}()
			t.Cleanup(func() { _ = session.Close(); <-events })
			// The production binding of a Session's extension actions, as every mode makes it.
			coding.BindSessionExtensionActions(h.runner, h.bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)
			if err := session.BindExtensions(t.Context(), coding.ExtensionBindings{}); err != nil {
				t.Fatal(err)
			}

			// What the provider received: the model, and the size of the image of the user message.
			var mu sync.Mutex
			var models, sizes []string
			record := ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, model *ai.Model) (ai.AssistantMessage, error) {
				mu.Lock()
				defer mu.Unlock()
				models = append(models, model.ID)
				for _, message := range request.Messages() {
					user, ok := message.(ai.UserMessage)
					if !ok {
						continue
					}
					blocks, _ := user.Content.(ai.UserContentBlocks)
					for _, block := range blocks {
						content, ok := block.(ai.ImageContent)
						if !ok {
							continue
						}
						raw, err := base64.StdEncoding.DecodeString(content.Data)
						if err != nil {
							return ai.AssistantMessage{}, err
						}
						config, _, err := image.DecodeConfig(bytes.NewReader(raw))
						if err != nil {
							return ai.AssistantMessage{}, err
						}
						sizes = append(sizes, itoa(config.Width)+"x"+itoa(config.Height))
					}
				}
				if len(sizes) == 0 {
					return ai.AssistantMessage{}, errors.New("the request carries no image")
				}
				return ai.FauxAssistantMessage(ai.FauxContentString("done"), ai.FauxAssistantMessageOptions{}), nil
			})

			// Without the extension's handler the wide model has no limit that touches a 64x64 image.
			faux.SetResponses([]ai.FauxResponseStep{record})
			if err := session.Prompt(t.Context(), "inspect", prompt); err != nil {
				t.Fatal(err)
			}
			if got := slices.Clone(models); len(got) != 1 || got[0] != "wide" || sizes[len(sizes)-1] != "64x64" {
				t.Fatalf("before the handler the provider received model %v and image %v, want wide and 64x64", got, sizes)
			}

			listed := services.ModelRuntime().GetModel("faux", "strict")
			if listed == nil || listed.InputLimits != nil {
				t.Fatalf("the registry's faux/strict is %+v, want a model without input limits", listed)
			}
			registryObject, err := extension.ModelObject(listed)
			if err != nil {
				t.Fatal(err)
			}
			var strict map[string]any
			if err := json.Unmarshal(registryObject, &strict); err != nil {
				t.Fatal(err)
			}
			strict["inputLimits"] = map[string]any{"images": map[string]any{"resize": map[string]any{"maxWidth": 16, "maxHeight": 16, "maxBytes": 500000, "jpegQuality": 70}}}
			strictObject, err := json.Marshal(strict)
			if err != nil {
				t.Fatal(err)
			}
			command, ok := findCommand(h.runner, "set_model_object_before_agent_start")
			if !ok {
				t.Fatal("the fixture has no set_model_object_before_agent_start command")
			}
			if err := command.Handler(context.Background(), string(strictObject)); err != nil {
				t.Fatal(err)
			}
			h.ui.ClearRecorded()
			models, sizes = nil, nil
			faux.SetResponses([]ai.FauxResponseStep{record})
			if err := session.Prompt(t.Context(), "inspect", prompt); err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(h.ui.Recorded(), func(s string) bool { return strings.HasPrefix(s, "beforeAgentSetModel=true") }) {
				t.Errorf("the before_agent_start handler's setModel answered %q, want true", h.ui.Recorded())
			}
			if got := session.Model(); got.ID != "strict" || got.InputLimits == nil || got.InputLimits.Images == nil || got.InputLimits.Images.Resize == nil || got.InputLimits.Images.Resize.MaxWidth != 16 {
				t.Errorf("the Session's model is %s with input limits %+v, want strict with the object's resize limits", got.ID, got.InputLimits)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(models) != 1 || models[0] != "strict" {
				t.Errorf("the provider received model %v, want strict", models)
			}
			if len(sizes) == 0 || sizes[len(sizes)-1] != "16x16" {
				t.Errorf("the provider received the image at %v, want 16x16: the image is normalized for the object the extension passed", sizes)
			}
		})
	}
}

// The string form of an SDK's set-model asks the host for the model object of the name (resolveModel) and then sets it. An error of the first call is the
// error of the set-model, and no setModel is sent. The Node runtime has no string form: Pi's pi.setModel takes the object.
func TestSetModelNamedReportsTheResolveErrorAndSendsNoSetAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, sdk := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if strings.Contains(sdk.name, "node") {
			continue
		}
		t.Run(sdk.name, func(t *testing.T) {
			h := sdk.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + sdk.name)
			}
			var mu sync.Mutex
			var resolved, set []string
			h.bridge.SetHostAction("resolveModel", func(_ context.Context, spec string) (*ai.Model, error) {
				mu.Lock()
				defer mu.Unlock()
				resolved = append(resolved, spec)
				if spec == "acme/known" {
					return &ai.Model{ID: "known", ProviderMeta: ai.ProviderMetadata{ProviderID: "acme", API: ai.APIOpenAICompletions}, Capabilities: ai.ModelCapabilities{ContextWindow: 4321}}, nil
				}
				return nil, errors.New("resolve-refused-7391: " + spec)
			})
			h.bridge.SetHostAction("setModel", func(_ context.Context, model *ai.Model) (bool, error) {
				mu.Lock()
				defer mu.Unlock()
				set = append(set, modelSpec(model)+" window="+itoa(model.Capabilities.ContextWindow))
				return true, nil
			})
			command, ok := findCommand(h.runner, "set_model")
			if !ok {
				t.Fatal("the fixture has no set_model command")
			}
			err := command.Handler(context.Background(), "acme/unknown")
			if err == nil || !strings.Contains(err.Error(), "resolve-refused-7391: acme/unknown") {
				t.Errorf("set_model of a name the host cannot resolve: %v, want the resolve error", err)
			}
			mu.Lock()
			if len(resolved) != 1 || resolved[0] != "acme/unknown" || len(set) != 0 {
				t.Errorf("the host received resolveModel %v and setModel %v, want one resolve and no set", resolved, set)
			}
			mu.Unlock()
			// The object the resolve returned is the one set.
			h.ui.ClearRecorded()
			if err := command.Handler(context.Background(), "acme/known"); err != nil {
				t.Fatalf("set_model acme/known: %v", err)
			}
			waitFor(t, func() bool {
				return slices.ContainsFunc(h.ui.Recorded(), func(s string) bool { return strings.HasPrefix(s, "setModel=true") })
			})
			mu.Lock()
			defer mu.Unlock()
			if len(set) != 1 || set[0] != "acme/known window=4321" {
				t.Errorf("the host received setModel %v, want the resolved object acme/known window=4321", set)
			}
		})
	}
}
