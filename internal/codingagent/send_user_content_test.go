package codingagent

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Source: AgentSession.sendUserMessage joins text parts with newlines and
// forwards images into prompt(); streaming queues retain the input image bytes.
func TestSendUserMessageStructuredIdlePreservesTextAndImage(t *testing.T) {
	m, seen, _ := newSendUserMessageHarness(t)
	m.isIdle = true
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	content := []any{map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "image", "data": data, "mimeType": "image/png"}, map[string]any{"type": "text", "text": "second"}}
	if err := m.deliverUserMessage(content, extension.DeliverAsFollowUp); err != nil {
		t.Fatal(err)
	}
	drainOneUITask(t, m)
	select {
	case request := <-seen:
		for _, message := range request.Messages {
			if user, ok := message.(ai.UserMessage); ok {
				blocks, ok := user.Content.(ai.UserContentBlocks)
				if !ok || len(blocks) != 2 {
					t.Fatalf("user content %#v", user.Content)
				}
				if text, ok := blocks[0].(ai.TextContent); !ok || text.Text != "first\nsecond" {
					t.Fatalf("joined text %#v", blocks[0])
				}
				if img, ok := blocks[1].(ai.ImageContent); !ok || img.Data != data || img.MimeType != "image/png" {
					t.Fatalf("image %#v", blocks[1])
				}
				return
			}
		}
		t.Fatal("no user message")
	case <-time.After(3 * time.Second):
		t.Fatal("structured idle prompt did not reach provider")
	}
}

func TestSendUserMessageStructuredActiveQueuesRawImages(t *testing.T) {
	for _, mode := range []extension.DeliverAs{extension.DeliverAsSteer, extension.DeliverAsFollowUp} {
		t.Run(string(mode), func(t *testing.T) {
			m, seen, _ := newSendUserMessageHarness(t)
			m.turnActive.Store(true)
			// These bytes deliberately are not a valid image: active delivery must queue
			// the original content, not eagerly normalize it against the current model.
			content := ai.UserContentBlocks{ai.TextContent{Text: "first"}, ai.ImageContent{Data: "raw-image", MimeType: "image/png"}, ai.TextContent{Text: "second"}}
			if err := m.deliverUserMessage(content, mode); err != nil {
				t.Fatal(err)
			}
			drainOneUITask(t, m)
			queued := m.agent.ClearFollowUpQueue()
			if mode == extension.DeliverAsSteer {
				queued = m.agent.ClearSteeringQueue()
			}
			if len(queued) != 1 || queued[0].User == nil {
				t.Fatalf("queue %#v", queued)
			}
			blocks := queued[0].User.Content
			if len(blocks) != 2 {
				t.Fatalf("content %#v", blocks)
			}
			if text, ok := blocks[0].(ai.TextContent); !ok || text.Text != "first\nsecond" {
				t.Fatalf("text %#v", blocks[0])
			}
			if img, ok := blocks[1].(ai.ImageContent); !ok || img.Data != "raw-image" {
				t.Fatalf("raw image %#v", blocks[1])
			}
			select {
			case <-seen:
				t.Fatal("started concurrent request")
			default:
			}
		})
	}
}

func TestSendUserMessageRejectsMalformedStructuredContent(t *testing.T) {
	for _, content := range []any{nil, 42, []any{"not an object"}, []any{map[string]any{"type": "audio"}}, []any{map[string]any{"type": "image", "mimeType": "image/png"}}} {
		m, _, _ := newSendUserMessageHarness(t)
		if err := m.deliverUserMessage(content, extension.DeliverAsSteer); err == nil {
			t.Fatalf("accepted %#v", content)
		}
		select {
		case <-m.uiTaskCh:
			t.Fatal("malformed content posted a UI mutation")
		default:
		}
	}
}
