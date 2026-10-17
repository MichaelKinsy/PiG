package coding

import (
	"encoding/base64"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

const promptTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

type promptImageProcessCall struct {
	bytes      []byte
	mime       string
	autoResize bool
	options    *ai.ModelImageResizeOptions
}

func mockPromptImageProcessor(h *recoveryHarness) *[]promptImageProcessCall {
	calls := []promptImageProcessCall{}
	h.session.processImage = func(bytes []byte, mime string, autoResize bool, options *ai.ModelImageResizeOptions) ([]byte, string, string, error) {
		calls = append(calls, promptImageProcessCall{bytes: slices.Clone(bytes), mime: mime, autoResize: autoResize, options: options})
		return []byte("normalized"), mime, "", nil
	}
	return &calls
}

func recordOriginalImagePrompt(t *testing.T, site int, name string, calls []promptImageProcessCall) {
	t.Helper()
	rows := []any{}
	for _, call := range calls {
		rows = append(rows, []any{call.mime, call.autoResize, call.options})
	}
	t.Log("PROMPT_IMAGE_ORIGINAL", []any{site, name, rows})
}

// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:133.
func TestUpstreamSessionPromptPreservesImageAttachmentsInProviderContext(t *testing.T) {
	h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
	calls := mockPromptImageProcessor(h)
	sawImage := false
	h.provider.responses = []scriptedResponse{func(messages []ai.Message) *ai.AssistantMessage {
		for _, message := range messages {
			if user, ok := message.(ai.UserMessage); ok {
				if blocks, ok := user.Content.(ai.UserContentBlocks); ok {
					for _, part := range blocks {
						if _, ok := part.(ai.ImageContent); ok {
							sawImage = true
						}
					}
				}
				break
			}
		}
		return fauxReply("ok", ai.StopReasonStop, 0)(messages)
	}}
	if err := h.session.Prompt(t.Context(), "describe", &PromptOptions{Images: []ai.ImageContent{{MimeType: "image/png", Data: "ZmFrZQ=="}}}); err != nil {
		t.Fatal(err)
	}
	if !sawImage {
		t.Fatal("provider did not receive an image attachment")
	}
	recordOriginalImagePrompt(t, 133, "preserves image attachments in the provider context", *calls)
}

// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:163.
func TestUpstreamSessionPromptUsesBeforeAgentModelForMockedImageNormalization(t *testing.T) {
	var strict *ai.Model
	var h *recoveryHarness
	h = newQueueCharacterizationHarness(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(...any) (any, error) {
		if strict == nil {
			t.Fatal("Expected strict model")
		}
		return nil, h.session.SetModel(strict)
	}}}}, nil)
	wide := *h.session.Model()
	wide.ID, wide.DisplayName = "wide", "wide"
	h.session.Agent().SetModel(&wide)
	strictCopy := wide
	strictCopy.ID, strictCopy.DisplayName = "strict", "strict"
	strict = &strictCopy
	resizeOptions := &ai.ModelImageResizeOptions{MaxWidth: 1000, MaxHeight: 1000, MaxBytes: 500000, JPEGQuality: 70}
	strict.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: resizeOptions}}
	calls := mockPromptImageProcessor(h)
	h.provider.responses = []scriptedResponse{fauxReply("done", ai.StopReasonStop, 0)}
	if err := h.session.Prompt(t.Context(), "inspect", &PromptOptions{Images: []ai.ImageContent{{Data: promptTinyPNG, MimeType: "image/png"}}}); err != nil {
		t.Fatal(err)
	}
	if h.session.Model().ID != "strict" {
		t.Fatalf("model=%q", h.session.Model().ID)
	}
	matched := false
	for _, call := range *calls {
		if call.bytes != nil && call.mime == "image/png" && call.autoResize && reflect.DeepEqual(call.options, resizeOptions) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("processImage calls=%+v", *calls)
	}
	want := ai.ImageContent{Data: base64.StdEncoding.EncodeToString([]byte("normalized")), MimeType: "image/png"}
	found := false
	for _, message := range h.session.Messages() {
		if message.User == nil {
			continue
		}
		blocks := message.ContentBlocks()
		if blocks != nil {
			for _, block := range blocks {
				if image, ok := block.(ai.ImageContent); ok && image == want {
					found = true
				}
			}
		}
		break
	}
	if !found {
		t.Fatalf("user content does not contain %+v", want)
	}
	recordOriginalImagePrompt(t, 163, "uses the model selected by before_agent_start for image normalization", *calls)
}
