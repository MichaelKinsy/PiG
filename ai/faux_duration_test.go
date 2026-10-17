package ai

import "testing"

// faux.ts cloneMessage (:298-307) spreads the scripted AssistantMessage, so a scripted durationMs sits on the partial of every event and on the
// final message, and AssistantMessageEventStream sets a measured durationMs only when the final message has none (event-stream.ts:127).
// A FauxResponseFactory returns the same message shape (faux.ts FauxResponseFactory), so its durationMs behaves the same.
// mutation-checked: dropping the copy in streamWithDeltas, or in fauxResponseFromMessage for an AssistantMessage step, fails the matching case.
func TestFauxScriptedDurationMsReachesEveryMessage(t *testing.T) {
	scripted := int64(4321)
	factory := FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (AssistantMessage, error) {
		return AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop, DurationMs: &scripted}, nil
	})
	message := FauxStaticStep(fauxResponseFromMessage(AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop, DurationMs: &scripted}))
	for name, step := range map[string]FauxResponseStep{
		"factory": factory,
		"message": message,
		"static":  FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("ok")}, StopReason: "stop", DurationMs: &scripted}),
	} {
		t.Run(name, func(t *testing.T) {
			core := CreateFauxCore(RegisterFauxProviderOptions{})
			core.SetResponses([]FauxResponseStep{step})
			request, options := fauxCoreRequest()
			stream, err := core.Stream(t.Context(), core.GetModel(), request, options)
			if err != nil {
				t.Fatal(err)
			}
			sawPartial := false
			for event := range stream.Events(t.Context()) {
				if start, ok := event.(StartEvent); ok {
					sawPartial = true
					if start.Partial.DurationMs == nil || *start.Partial.DurationMs != scripted {
						t.Fatalf("start partial durationMs = %v, want %d", start.Partial.DurationMs, scripted)
					}
				}
			}
			if !sawPartial {
				t.Fatal("no start event")
			}
			if result := stream.Result(); result.DurationMs == nil || *result.DurationMs != scripted {
				t.Fatalf("final durationMs = %v, want the scripted %d, not a measurement", result.DurationMs, scripted)
			}
		})
	}
	core := CreateFauxCore(RegisterFauxProviderOptions{})
	core.SetResponses([]FauxResponseStep{fauxCoreAnswer("plain")})
	request, options := fauxCoreRequest()
	stream, _ := core.Stream(t.Context(), core.GetModel(), request, options)
	if result := stream.Result(); result.DurationMs == nil || *result.DurationMs == scripted {
		t.Fatalf("an unscripted response must carry the stream's measurement, got %v", result.DurationMs)
	}
}
