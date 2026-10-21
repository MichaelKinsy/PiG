package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// routedImageSession resumes a session that selected the virtual model router/auto and whose latest response came from the physical model faux/large. Only the physical model declares an image resize profile (MaxWidth 20); the virtual model declares none.
func routedImageSession(t *testing.T, tools ...agent.AgentTool) (*Session, virtualFaux) {
	t.Helper()
	t.Setenv(icodingagent.ENV_AGENT_DIR, t.TempDir())
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{Credentials: ai.NewInMemoryCredentialStore(), ModelsStore: ai.NewInMemoryModelsStore(), ModelsPath: new((*string)(nil)), AllowModelNetwork: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Models: []ai.FauxModelDefinition{{
		ID: "large", ContextWindow: 50_000, MaxTokens: 5000, Input: []string{"text", "image"},
		InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}},
	}}})
	if err := runtime.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	err = runtime.RegisterVirtualModel(VirtualModelDefinition{
		Provider: "router", ID: "auto", Name: "Auto",
		Route: func(context.Context, ModelRouteRequest) (ModelRoute, error) {
			return ModelRoute{Model: runtime.GetModel("faux", "large")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("refresh = %+v", result)
	}
	manager := icodingagent.NewSession("routed-images", t.TempDir())
	if _, err := manager.AppendModelChange("router", "auto"); err != nil {
		t.Fatal(err)
	}
	appendVirtualTranscript(t, manager, userText("hi", 1), assistantFromModel(runtime.GetModel("faux", "large"), "hello"))
	session, err := NewSession(runtime.services, SessionOptions{SessionManager: manager, SkipBuiltinTools: true, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	go func() {
		for range session.Events() {
		}
	}()
	if routed := session.RoutedModel(); routed == nil || routed.Model.InputLimits == nil {
		t.Fatalf("routed model = %+v, want the physical model with its image profile", routed)
	}
	return session, faux
}

// Pi sizes prompt images with `_limitsModel()`, the physical model that answered last under a virtual selection (agent-session.ts:641-643, 2037).
func TestPromptImagesUseTheRoutedModelsProfile(t *testing.T) {
	session, faux := routedImageSession(t)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}, StopReason: "stop"})})
	messages, err := session.SendContent(context.Background(), BuildUserContent("look", []ai.ImageContent{sessionImageFixture(t)}))
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if blocks, ok := userBlocks(message); ok {
			text := blocks[0].(ai.TextContent).Text
			if !strings.Contains(text, "displayed at 20x10") {
				t.Fatalf("prompt text = %q, want the physical model's 20px resize hint", text)
			}
			return
		}
	}
	t.Fatal("user message missing")
}

// Pi sizes tool-result images with `_limitsModel()` as well (agent-session.ts:707).
func TestToolResultImagesUseTheRoutedModelsProfile(t *testing.T) {
	session, faux := routedImageSession(t, screenshotTool{image: sessionImageFixture(t)})
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("env_probe", map[string]any{}, nil)}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	messages, err := session.Send(context.Background(), "capture")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ToolResult != nil {
			if !strings.Contains(message.ToolResult.Text(), "displayed at 20x10") {
				t.Fatalf("tool result = %#v, want the physical model's 20px resize hint", message.ToolResult)
			}
			return
		}
	}
	t.Fatal("tool result missing")
}

// userBlocks returns the content blocks of a user message that carries blocks, as the prompt with images does; the resumed transcript's plain-text message does not.
func userBlocks(message agent.AgentMessage) (ai.UserContentBlocks, bool) {
	if message.User == nil {
		return nil, false
	}
	blocks, ok := message.User.Content.(ai.UserContentBlocks)
	return blocks, ok
}
