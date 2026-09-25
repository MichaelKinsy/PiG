package codingagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Mirrors agent-session-prompt.test.ts: input transforms feed before_agent_start,
// then that hook's model selection determines the profile used in history.
func TestInteractivePromptImagesUsePostHookModelLimits(t *testing.T) {
	seen := make(chan capturedStreamRequest, 1)
	model := &ai.Model{ID: "capture", Provider: captureStreamOptionsProvider{seen: seen}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}, InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 100}}}}
	strict := *model
	strict.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}}
	input := ai.ImageContent{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(makePNGImage(t, 80, 40, color.RGBA{255, 0, 0, 255}))}
	replacement := ai.ImageContent{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(makePNGImage(t, 40, 20, color.RGBA{0, 0, 255, 255}))}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.editor = tui.NewEditor()
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.runCtx = ctx
	m.abortCtx = ctx
	m.abortFn = cancel
	before := make(chan extension.BeforeAgentStartEvent, 1)
	ext := extension.Extension{Path: "input-limits", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			event := args[0].(extension.InputEvent)
			if len(event.Images) != 1 {
				return nil, fmt.Errorf("input hook received %d images", len(event.Images))
			}
			return extension.InputEventResultTransform{Text: "transformed", Images: []extension.ImageContent{replacement}}, nil
		}},
		EventBeforeAgentStart: {func(args ...any) (any, error) {
			before <- args[0].(extension.BeforeAgentStartEvent)
			m.agent.SetModel(&strict)
			return nil, nil
		}},
	}}
	m.newRunner = inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	m.handleSubmitWithImages(ctx, "original", []ai.ImageContent{input})
	select {
	case event := <-before:
		if event.Prompt != "transformed" || len(event.Images) != 1 {
			t.Fatalf("before event = %#v", event)
		}
		raw, err := json.Marshal(event.Images[0])
		if err != nil {
			t.Fatal(err)
		}
		var got ai.ImageContent
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Data != replacement.Data {
			t.Fatal("before_agent_start did not receive unprocessed input replacement")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("before_agent_start missing")
	}
	select {
	case request := <-seen:
		user, ok := request.Messages[len(request.Messages)-1].(ai.UserMessage)
		if !ok {
			t.Fatalf("last message=%T", request.Messages[len(request.Messages)-1])
		}
		blocks := user.Content.(ai.UserContentBlocks)
		if len(blocks) != 2 {
			t.Fatalf("content=%#v", blocks)
		}
		if text := blocks[0].(ai.TextContent).Text; !strings.Contains(text, "transformed\n\n[Image: original 40x20, displayed at 20x10.") {
			t.Fatalf("prompt=%q", text)
		}
		data, err := base64.StdEncoding.DecodeString(blocks[1].(ai.ImageContent).Data)
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Bounds().Dx() != 20 || decoded.Bounds().Dy() != 10 {
			t.Fatalf("image=%v", decoded.Bounds())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider request missing")
	}
}

func TestCompactionQueuePreservesUnprocessedImages(t *testing.T) {
	for _, mode := range []compactionQueueMode{compactionQueueSteer, compactionQueueFollowUp} {
		t.Run(string(mode), func(t *testing.T) {
			m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir()})
			m.agent = agent.NewAgent(agent.AgentOptions{})
			m.keybindings = NewKeybindingsManager(t.TempDir())
			m.pendingMessagesContainer = tui.NewContainer()
			m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
			attachment := ai.ImageContent{Data: "unprocessed", MimeType: "image/png"}
			m.queueCompactionMessageForActiveTurn(compactionQueuedMessage{text: "queued", images: []ai.ImageContent{attachment}, mode: mode})
			steer, follow := m.agent.PendingMessages()
			messages := steer
			if mode == compactionQueueFollowUp {
				messages = follow
			}
			if len(messages) != 1 || len(messages[0].User.Content) != 2 || messages[0].User.Content[1] != attachment {
				t.Fatalf("queued messages = %#v", messages)
			}
		})
	}
}

func TestNormalizePromptContentOmissionAndDisabledResize(t *testing.T) {
	attachment := ai.ImageContent{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(makePNGImage(t, 40, 20, color.RGBA{255, 0, 0, 255}))}
	model := &ai.Model{InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 10}}}}
	content := promptContent("keep", []ai.ImageContent{attachment})
	got := NormalizePromptContent(content, false, model)
	if len(got) != 2 || got[0].(ai.TextContent).Text != "keep" || got[1] != attachment {
		t.Fatalf("autoResize=false changed valid image: %#v", got)
	}
	broken := ai.ImageContent{Data: "broken", MimeType: "image/png"}
	got = NormalizePromptContent(promptContent("keep", []ai.ImageContent{broken}), true, model)
	if len(got) != 1 || !strings.Contains(got[0].(ai.TextContent).Text, "keep\n\n") || !strings.Contains(got[0].(ai.TextContent).Text, "omitted") {
		t.Fatalf("bad image was not omitted with hint: %#v", got)
	}
	if content[0].(ai.TextContent).Text != "keep" || content[1] != attachment {
		t.Fatal("normalization mutated caller content")
	}
}

func TestPromptImageInputLimitsAcceptNodeBase64(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(makePNGImage(t, 40, 20, color.RGBA{255, 0, 0, 255}))
	wrapped := " \t" + data[:10] + "#" + data[10:]
	model := &ai.Model{InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 10}}}}
	got := NormalizePromptContent(promptContent("keep", []ai.ImageContent{{Data: wrapped, MimeType: "image/png"}}), true, model)
	if len(got) != 2 || !strings.Contains(got[0].(ai.TextContent).Text, "displayed at 10x5") {
		t.Fatalf("Node-compatible attachment omitted or not resized: %#v", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(got[1].(ai.ImageContent).Data)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width != 10 || config.Height != 5 {
		t.Fatalf("resized dimensions=%+v err=%v", config, err)
	}
}
