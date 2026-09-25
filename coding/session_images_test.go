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
			if !seen || len(msg.User.Content) != 2 || !strings.Contains(msg.User.Content[0].(ai.TextContent).Text, "displayed at 20x10") {
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
				return agent.AfterToolCallResult{Images: &images}
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
		if message.User != nil && len(message.User.Content) == 2 {
			stored = message.User.Content[1].(ai.ImageContent)
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
		if message.User != nil && len(message.User.Content) == 2 {
			if image := message.User.Content[1].(ai.ImageContent); image != stored {
				t.Fatal("model switch rewrote earlier image")
			}
			return
		}
	}
	t.Fatal("historical image missing")
}
