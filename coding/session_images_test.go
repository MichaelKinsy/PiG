package coding

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func sessionImageFixture(t *testing.T) ai.ImageContent {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 80, 40))); err != nil {
		t.Fatal(err)
	}
	return ai.ImageContent{Data: base64.StdEncoding.EncodeToString(encoded.Bytes()), MimeType: "image/png"}
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:917 (BeforeAgentStartEvent.images).
func TestSessionPromptImagesUseModelSelectedByBeforeHook(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func(s *Session) { _ = s.Close() }(sess)
	original := sessionImageFixture(t)
	strict := *sess.agent.Model()
	strict.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}}
	seen := false
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "images", Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		event := args[0].(extension.BeforeAgentStartEvent)
		if len(event.Images) != 1 {
			t.Errorf("before hook images=%#v", event.Images)
		}
		seen = true
		sess.agent.SetModel(&strict)
		return nil, nil
	}}}}}, t.TempDir()))
	messages, err := sess.SendContent(context.Background(), BuildUserContent("prompt", []ai.ImageContent{original}))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		if msg.User != nil {
			if !seen || len(msg.User.Content.(ai.UserContentBlocks)) != 2 || !strings.Contains(msg.User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text, "displayed at 20x10") {
				t.Fatalf("prompt=%#v", msg.User.Content)
			}
			return
		}
	}
	t.Fatal("user message missing")
}

func TestSessionToolImagesUseModelAfterLateHook(t *testing.T) {
	for _, clone := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "clone"}[clone], func(t *testing.T) {
			svcs := newTestServices(t)
			provider := &toolCallProvider{}
			sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider), Tools: []agent.AgentTool{&fakeTool{name: "env_probe"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer func(s *Session) { _ = s.Close() }(sess)
			if clone {
				appendAsst(t, sess, "saved reply")
				sess, err = sess.Clone()
				if err != nil {
					t.Fatal(err)
				}
				defer func(s *Session) { _ = s.Close() }(sess)
			}
			original := sessionImageFixture(t)
			strict := *sess.agent.Model()
			strict.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}}
			sess.agent.AddAfterToolCallHook(func(context.Context, string, string, json.RawMessage, agent.AgentToolResult) agent.AfterToolCallResult {
				sess.agent.SetModel(&strict)
				images := []ai.ImageContent{original}
				return agent.AfterToolCallResult{Content: []ai.ToolResultMessageContent{images[0]}}
			})
			messages, err := sess.Send(context.Background(), "read")
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range messages {
				if msg.ToolResult != nil {
					if !strings.Contains(msg.ToolResult.Text(), "displayed at 20x10") {
						t.Fatalf("result=%#v", msg.ToolResult)
					}
					return
				}
			}
			t.Fatal("tool result missing")
		})
	}
}

func TestSessionModelSwitchPreservesHistoricalImageBytes(t *testing.T) {
	model := fakeModel()
	model.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}}
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer func(s *Session) { _ = s.Close() }(sess)
	original := sessionImageFixture(t)
	messages, err := sess.SendContent(context.Background(), BuildUserContent("first", []ai.ImageContent{original}))
	if err != nil {
		t.Fatal(err)
	}
	var stored ai.ImageContent
	for _, message := range messages {
		if message.User != nil && len(message.User.Content.(ai.UserContentBlocks)) == 2 {
			stored = message.User.Content.(ai.UserContentBlocks)[1].(ai.ImageContent)
		}
	}
	if stored.Data == "" || stored.Data == original.Data {
		t.Fatal("first image was not resized before persistence")
	}
	next := *model
	next.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 10}}}
	sess.agent.SetModel(&next)
	if _, err := sess.Send(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	for _, message := range sess.agent.Messages() {
		if message.User != nil && len(message.User.Content.(ai.UserContentBlocks)) == 2 {
			if image := message.User.Content.(ai.UserContentBlocks)[1].(ai.ImageContent); image != stored {
				t.Fatal("model switch rewrote earlier image")
			}
			return
		}
	}
	t.Fatal("historical image missing")
}

type screenshotTool struct{ image ai.ImageContent }

func (t screenshotTool) Name() string          { return "env_probe" }
func (t screenshotTool) Label() string         { return "Screenshot" }
func (t screenshotTool) Schema() ai.ToolSchema { return ai.ToolSchema{Name: "env_probe"} }
func (t screenshotTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeParallel
}
func (t screenshotTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "captured"}, t.image}}, nil
}

// packages/coding-agent/test/suite/agent-session-tool-result-images.test.ts:37 "passes image settings and the current model profile to tool result normalization". Upstream mocks normalizeToolResultImages and asserts {autoResizeImages, resizeOptions}. Go has no module mock, so the same two inputs are observed through the real normalization: the active model profile's MaxWidth resizes the image only while the images.autoResize setting is on, and the setting off leaves the bytes untouched.
func TestSessionToolResultImagesUseSettingsAndModelProfile(t *testing.T) {
	for _, tc := range []struct {
		name       string
		autoResize bool
		resized    bool
	}{
		{"auto resize disabled passes the setting through", false, false},
		{"auto resize enabled applies the model profile", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svcs := newTestServices(t)
			if err := svcs.SettingsManager().SetImageAutoResize(tc.autoResize); err != nil {
				t.Fatal(err)
			}
			original := sessionImageFixture(t)
			model := fakeModelWithProvider(&toolCallProvider{})
			model.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}}
			sess, err := NewSession(svcs, SessionOptions{Model: model, Tools: []agent.AgentTool{screenshotTool{image: original}}})
			if err != nil {
				t.Fatal(err)
			}
			defer func(s *Session) { _ = s.Close() }(sess)
			messages, err := sess.Send(context.Background(), "take a screenshot")
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range messages {
				if msg.ToolResult == nil {
					continue
				}
				var image *ai.ImageContent
				for _, block := range msg.ToolResult.Content {
					if img, ok := block.(ai.ImageContent); ok {
						image = &img
					}
				}
				if image == nil {
					t.Fatalf("tool result lost its image: %#v", msg.ToolResult)
				}
				if got := image.Data != original.Data; got != tc.resized {
					t.Fatalf("image changed=%v, want %v", got, tc.resized)
				}
				if hint := strings.Contains(msg.ToolResult.Text(), "displayed at 20x10"); hint != tc.resized {
					t.Fatalf("resize hint=%v, want %v: %q", hint, tc.resized, msg.ToolResult.Text())
				}
				return
			}
			t.Fatal("tool result missing")
		})
	}
}
