package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/streamattachhook/attachstall"
)

// generation.ts:740-741 calls streamSimple and iterates it in the same synchronous prefix, so its `for await` reads pi-ai's opening start event with the partial the provider built (no content), skips it, and observes the first partial that shows content. A generation that attached after the provider had finished would read a start event whose partial already holds the whole response and observe that.
func TestObserveResponseSkipsTheStartEventWhoseProviderPartialHasNoContentYet(t *testing.T) {
	models, model := attachstall.ModelsOverOpenAI(t)
	attachstall.Install(t)

	var observed []ai.AssistantMessageEvent
	stream, err := observeResponse(context.Background(), models, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, ai.StreamOptions{APIKey: "test"}, func(event ai.AssistantMessageEvent, _ *ai.AssistantMessage) {
		observed = append(observed, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result == nil || len(result.Content) == 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(observed) == 0 {
		t.Fatal("no partial showed content")
	}
	if first, isStart := observed[0].(ai.StartEvent); isStart {
		t.Fatalf("the first observed partial is the start event %#v: it was read after the provider had filled its partial", first)
	}
}
