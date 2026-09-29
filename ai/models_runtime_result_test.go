package ai

import (
	"context"
	"testing"
	"time"
)

func TestModelsLazyStreamForwardsResultWithoutEvents(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/src/api/lazy.ts:31-38 forwards source.result() after iteration ends, even if no terminal event was emitted.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	models := CreateModels()
	result := &AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "end-only"}}, StopReason: StopReasonStop}
	inner := NewAssistantMessageEventStream()
	inner.End(result)
	outer := models.lazyStream(ctx, &Model{ID: "test"}, func() (*AssistantMessageEventStream, error) { return inner, nil })
	for event := range outer.Events(ctx) {
		t.Fatalf("invented event: %T", event)
	}
	got, err := outer.ResultContext(ctx)
	if err != nil || got != result {
		t.Fatalf("result=%p want=%p err=%v", got, result, err)
	}
	models.operations.Wait()
}
