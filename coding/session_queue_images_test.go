package coding

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func tallPNG(t *testing.T) ai.ImageContent {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 1200, 9000))); err != nil {
		t.Fatal(err)
	}
	return ai.ImageContent{Data: base64.StdEncoding.EncodeToString(encoded.Bytes()), MimeType: "image/png"}
}

func providerImages(messages []ai.Message) (images []ai.ImageContent) {
	for _, message := range messages {
		if user, ok := message.(ai.UserMessage); ok {
			blocks, _ := user.Content.(ai.UserContentBlocks)
			for _, block := range blocks {
				if image, ok := block.(ai.ImageContent); ok {
					images = append(images, image)
				}
			}
		}
	}
	return images
}

// pi: agent-session-queue.test.ts "removes an image-only steering message after delivery" (Pi main a63c4ec5c, #8612).
func TestSessionRemovesAnImageOnlySteeringMessageAfterDelivery(t *testing.T) {
	waiting := createQueueWaitingHarness(t, extension.Extension{})
	h := waiting.h
	sawImage := false
	waiting.setResponses(fauxToolCall("wait"), func(messages []ai.Message) *ai.AssistantMessage {
		sawImage = len(providerImages(messages)) > 0
		return fauxReply("done", ai.StopReasonStop, 0)(messages)
	})
	<-waiting.waitForToolStart
	if _, err := h.session.Steer(t.Context(), "", []ai.ImageContent{sessionImageFixture(t)}, nil); err != nil {
		t.Fatal(err)
	}
	if got := h.session.PendingMessageCount(); got != 1 {
		t.Fatalf("pending before delivery = %d, want 1", got)
	}
	waiting.releaseToolExecution()
	waiting.join()
	if !sawImage {
		t.Fatal("the image never reached the model")
	}
	if h.session.agent.HasQueuedMessages() || h.session.PendingMessageCount() != 0 || len(h.session.GetSteeringMessages()) != 0 {
		t.Fatalf("queue after delivery: agent queued %v, pending %d, steering %v", h.session.agent.HasQueuedMessages(), h.session.PendingMessageCount(), h.session.GetSteeringMessages())
	}
}

// pi: agent-session-queued-images.test.ts "resizes images in steering, follow-up, and streaming prompt messages" (Pi main 4cf69332c, #11782; D249).
func TestSessionResizesImagesInQueuedSteeringAndFollowUpMessages(t *testing.T) {
	waiting := createQueueWaitingHarness(t, extension.Extension{})
	h := waiting.h
	original := tallPNG(t)
	var sent []ai.ImageContent
	record := func(messages []ai.Message) *ai.AssistantMessage {
		sent = append(sent, providerImages(messages)...)
		return fauxReply("ok", ai.StopReasonStop, 0)(messages)
	}
	waiting.setResponses(fauxToolCall("wait"), record, record, record)
	<-waiting.waitForToolStart
	if _, err := h.session.Steer(t.Context(), "steer", []ai.ImageContent{original}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.FollowUp(t.Context(), "follow up", []ai.ImageContent{original}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.session.Prompt(t.Context(), "streaming prompt", &PromptOptions{Images: []ai.ImageContent{original}, StreamingBehavior: extension.DeliverAsFollowUp}); err != nil {
		t.Fatal(err)
	}
	waiting.releaseToolExecution()
	waiting.join()
	checked := 0
	for _, message := range h.session.Messages() {
		if message.User == nil {
			continue
		}
		text := extractUserMessageText(message.User.Content)
		if !strings.HasPrefix(text, "steer") && !strings.HasPrefix(text, "follow up") && !strings.HasPrefix(text, "streaming prompt") {
			continue
		}
		checked++
		blocks := message.User.Content.(ai.UserContentBlocks)
		var images []ai.ImageContent
		for _, block := range blocks {
			if image, ok := block.(ai.ImageContent); ok {
				images = append(images, image)
			}
		}
		if len(images) != 1 || images[0].Data == original.Data {
			t.Fatalf("%q kept its full-size image (%d images)", text, len(images))
		}
		if !strings.Contains(text, "original 1200x9000, displayed at 267x2000") {
			t.Fatalf("%q lacks the resize hint: %q", text, text)
		}
	}
	if checked != 3 {
		t.Fatalf("checked %d queued messages, want 3", checked)
	}
	if len(sent) == 0 {
		t.Fatal("no image reached the model")
	}
	for _, image := range sent {
		if image.Data == original.Data {
			t.Fatal("a request carried the original image")
		}
	}
	if h.session.PendingMessageCount() != 0 {
		t.Fatalf("pending = %d", h.session.PendingMessageCount())
	}
}

// pi main 4cf69332c (#11782; D249): the interactive mode queues on the Agent with a message built by QueuedUserMessage; delivery resizes it.
func TestSessionQueuedUserMessageBuiltForAgentQueueIsResizedAtDelivery(t *testing.T) {
	waiting := createQueueWaitingHarness(t, extension.Extension{})
	h := waiting.h
	original := tallPNG(t)
	var sent []ai.ImageContent
	waiting.setResponses(fauxToolCall("wait"), func(messages []ai.Message) *ai.AssistantMessage {
		sent = append(sent, providerImages(messages)...)
		return fauxReply("ok", ai.StopReasonStop, 0)(messages)
	})
	<-waiting.waitForToolStart
	h.session.agent.Steer(h.session.QueuedUserMessage("steer", []ai.ImageContent{original}))
	waiting.releaseToolExecution()
	waiting.join()
	if len(sent) == 0 {
		t.Fatal("no image reached the model")
	}
	for _, image := range sent {
		if image.Data == original.Data {
			t.Fatal("a request carried the original image")
		}
	}
	for _, message := range h.session.Messages() {
		if message.User != nil && strings.HasPrefix(extractUserMessageText(message.User.Content), "steer") && !strings.Contains(extractUserMessageText(message.User.Content), "displayed at 267x2000") {
			t.Fatalf("saved message lacks the resize hint: %q", extractUserMessageText(message.User.Content))
		}
	}
}
