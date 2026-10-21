package ai

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// referenceResolveTranscriptTools is ResolveTranscriptTools as first written, over a copy of the system messages. The in-place
// scan must return the same tools and folded count for every transcript.
func referenceResolveTranscriptTools(messages []Message, support TranscriptToolSupport) TranscriptTools {
	var systemMessages []SystemMessage
	for _, item := range messages {
		if message, ok := systemMessageOf(item); ok {
			systemMessages = append(systemMessages, message)
		}
	}
	switch support {
	case TranscriptToolSupportNone:
		return TranscriptTools{RequestTools: GetCurrentTools(systemMessages), FoldedSystemMessages: len(systemMessages)}
	case TranscriptToolSupportChanges:
		return TranscriptTools{}
	}
	folded := max(referenceLastNonAdditiveToolChange(systemMessages)+1, 0)
	if len(messages) > 0 {
		if _, ok := systemMessageOf(messages[0]); ok {
			folded = max(folded, 1)
		}
	}
	return TranscriptTools{RequestTools: GetCurrentTools(systemMessages[:folded]), FoldedSystemMessages: folded}
}

func referenceLastNonAdditiveToolChange(systemMessages []SystemMessage) int {
	declared := map[string]struct{}{}
	last := -1
	for index, message := range systemMessages {
		if len(message.ToolsRemoved) > 0 {
			last = index
		}
		for _, tool := range message.ToolsAdded {
			if _, exists := declared[tool.Name]; exists {
				last = index
			}
			declared[tool.Name] = struct{}{}
		}
	}
	return last
}

func TestResolveTranscriptToolsMatchesTheCopyingReference(t *testing.T) {
	random := rand.New(rand.NewSource(20261020))
	tool := func(name string, variant int) ToolSchema {
		return ToolSchema{Name: name, Description: fmt.Sprintf("%s v%d", name, variant), Parameters: JsonObject{"type": "object"}}
	}
	for trial := range 4000 {
		var messages []Message
		length := random.Intn(12)
		for position := range length {
			switch {
			case random.Intn(3) == 0 || (position == 0 && random.Intn(2) == 0):
				message := SystemMessage{Content: SystemText("update"), Timestamp: int64(position)}
				for range random.Intn(3) {
					message.ToolsAdded = append(message.ToolsAdded, tool(fmt.Sprintf("t%d", random.Intn(5)), random.Intn(3)))
				}
				for range random.Intn(2) {
					message.ToolsRemoved = append(message.ToolsRemoved, ToolReference{Name: fmt.Sprintf("t%d", random.Intn(5))})
				}
				messages = append(messages, message)
			case random.Intn(2) == 0:
				messages = append(messages, UserMessage{Content: UserText("q"), Timestamp: int64(position)})
			default:
				messages = append(messages, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "a"}}, StopReason: StopReasonStop, Timestamp: int64(position)})
			}
		}
		for _, support := range []TranscriptToolSupport{TranscriptToolSupportNone, TranscriptToolSupportAdditions, TranscriptToolSupportChanges} {
			got, want := ResolveTranscriptTools(messages, support), referenceResolveTranscriptTools(messages, support)
			if got.FoldedSystemMessages != want.FoldedSystemMessages || !reflect.DeepEqual(got.RequestTools, want.RequestTools) {
				t.Fatalf("trial %d, support %s: got folded %d tools %+v, want folded %d tools %+v\nmessages %+v", trial, support, got.FoldedSystemMessages, got.RequestTools, want.FoldedSystemMessages, want.RequestTools, messages)
			}
		}
		var systemMessages []SystemMessage
		for _, item := range messages {
			if message, ok := systemMessageOf(item); ok {
				systemMessages = append(systemMessages, message)
			}
		}
		if got, want := HasNonAdditiveToolChanges(messages), referenceLastNonAdditiveToolChange(systemMessages) >= 0; got != want {
			t.Fatalf("trial %d: HasNonAdditiveToolChanges = %v, want %v", trial, got, want)
		}
	}
}
