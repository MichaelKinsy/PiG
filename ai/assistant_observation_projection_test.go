package ai

import (
	"reflect"
	"testing"
)

// observedProjection is what ObserveProjection returns, gathered for comparison.
type observedProjection struct {
	Content      []AssistantContentProjection
	StopReason   StopReason
	ErrorMessage string
}

// project is ObserveProjection into a new slice.
func project(message *AssistantMessage) observedProjection {
	content, stopReason, errorMessage := message.ObserveProjection(nil)
	return observedProjection{Content: content, StopReason: stopReason, ErrorMessage: errorMessage}
}

// projectObserved is the projection of an owned observation: what ObserveProjection must return without copying it.
func projectObserved(message *AssistantMessage) observedProjection {
	projection := observedProjection{StopReason: message.StopReason, ErrorMessage: message.ErrorMessage}
	for _, block := range message.Content {
		switch block := block.(type) {
		case TextContent:
			projection.Content = append(projection.Content, AssistantContentProjection{Kind: "text", Text: block.Text})
		case ThinkingContent:
			projection.Content = append(projection.Content, AssistantContentProjection{Kind: "thinking", Thinking: block.Thinking})
		case ToolCall:
			projection.Content = append(projection.Content, AssistantContentProjection{Kind: "toolCall", ToolCallID: block.ID, ToolCallName: block.Name, Arguments: block.Arguments})
		}
	}
	return projection
}

// A projection reads what Observe copies: for a plain message, a full view and a shallow view, at every publication, including a replaced content reference that a shallow view does not follow and an assigned stop reason it keeps from its copy boundary. A view's Publication advances with every publication of its cell and stays put between them.
func TestObserveProjectionEqualsObserve(t *testing.T) {
	message := &AssistantMessage{StopReason: StopReasonPending, Content: []AssistantContentBlock{ThinkingContent{Thinking: "plan"}}}
	cell := newAssistantMessageCell(message)
	full := cell.view()
	shallow := full.ShallowCopy()
	steps := []func(){
		func() {
			message.Content = append(message.Content, TextContent{Text: "hello"})
			cell.publish(message, assistantMessageReplacements{})
		},
		func() {
			message.Content = append(message.Content, ToolCall{ID: "call_0", Name: "write", Arguments: JsonObject{"path": "a.txt", "content": "x"}})
			cell.publish(message, assistantMessageReplacements{})
		},
		func() {
			message.Content = []AssistantContentBlock{TextContent{Text: "replaced"}}
			cell.publish(message, assistantMessageReplacements{Content: true})
		},
		func() {
			message.StopReason, message.ErrorMessage = StopReasonError, "boom"
			cell.publish(message, assistantMessageReplacements{})
		},
	}
	check := func(step int) {
		t.Helper()
		plain := *message
		plain.observation = nil
		for name, view := range map[string]*AssistantMessage{"plain": &plain, "full": full, "shallow": shallow} {
			got := project(view)
			if want := projectObserved(view.Observe()); !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d %s: projection\n%#v\nwant\n%#v", step, name, got, want)
			}
		}
	}
	check(0)
	if cell, seq := (&AssistantMessage{}).Publication(); cell != 0 || seq != 0 {
		t.Fatalf("plain message publication = (%d, %d), want (0, 0)", cell, seq)
	}
	fullCell, fullSeq := full.Publication()
	for index, step := range steps {
		if againCell, againSeq := full.Publication(); againCell != fullCell || againSeq != fullSeq {
			t.Fatalf("step %d: publication moved without a publish", index)
		}
		step()
		check(index + 1)
		nextCell, nextSeq := full.Publication()
		shallowCell, shallowSeq := shallow.Publication()
		if nextCell != fullCell || nextSeq != fullSeq+1 || shallowCell != nextCell || shallowSeq != nextSeq {
			t.Fatalf("step %d: publication (%d, %d) shallow (%d, %d), want (%d, %d)", index, nextCell, nextSeq, shallowCell, shallowSeq, fullCell, fullSeq+1)
		}
		fullSeq = nextSeq
	}
}
