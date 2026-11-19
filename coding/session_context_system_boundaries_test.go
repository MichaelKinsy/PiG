package coding

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func contextBoundaryTranscript() []agent.AgentMessage {
	user := func(text string) agent.AgentMessage {
		return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
	}
	system := func(text string) agent.AgentMessage {
		return agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(text)}}
	}
	return []agent.AgentMessage{system("first"), user("one"), system("second"), user("two")}
}

func contextBoundaryShape(t *testing.T, messages []agent.AgentMessage) []string {
	t.Helper()
	shape := make([]string, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.System != nil:
			shape = append(shape, "system:"+ai.GetCurrentSystemPrompt([]ai.Message{*message.System}))
		case message.User != nil:
			shape = append(shape, "user:"+extractUserMessageText(message.User.Content))
		default:
			t.Fatalf("unexpected message %+v", message)
		}
	}
	return shape
}

// Source: runner.ts emitContext/sameMessages/restoreSystemMessages. A context
// handler that edits a message in place (returning nothing, or the same list)
// leaves the conversation unchanged, so every system message stays where it
// was; only a replacement list collapses them into one leading system message.
// REFNL-003.
func TestContextPhaseInPlaceEditKeepsSystemBoundaries(t *testing.T) {
	edit := func(event extension.ContextEvent) {
		event.Messages[0].User.Content = ai.UserContentBlocks{ai.TextContent{Text: "edited"}}
	}
	for _, tc := range []struct {
		name    string
		handler extension.HandlerFn
		want    []string
	}{
		{
			name: "no result",
			handler: func(args ...any) (any, error) {
				edit(args[0].(extension.ContextEvent))
				return nil, nil
			},
			want: []string{"system:first", "user:edited", "system:second", "user:two"},
		},
		{
			name: "same list returned",
			handler: func(args ...any) (any, error) {
				event := args[0].(extension.ContextEvent)
				edit(event)
				return &extension.ContextEventResult{Messages: append([]extension.AgentMessage(nil), event.Messages...)}, nil
			},
			want: []string{"system:first", "user:edited", "system:second", "user:two"},
		},
		{
			name: "replacement list",
			handler: func(args ...any) (any, error) {
				event := args[0].(extension.ContextEvent)
				edit(event)
				replacement := event.Messages[0]
				replacement.User = &agent.UserMessage{Role: agent.RoleUser, Content: replacement.User.Content}
				return &extension.ContextEventResult{Messages: []extension.AgentMessage{replacement, event.Messages[1]}}, nil
			},
			want: []string{"system:first\n\nsecond", "user:edited", "user:two"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := inproc.NewRunner([]extension.Extension{{Path: "edit", Handlers: map[string][]extension.HandlerFn{"context": {tc.handler}}}}, t.TempDir())
			history := contextBoundaryTranscript()
			got, err := (&Session{}).contextPhase(t.Context(), runner, history)
			if err != nil {
				t.Fatal(err)
			}
			if shape := contextBoundaryShape(t, got); !equalStrings(shape, tc.want) {
				t.Fatalf("context = %v, want %v", shape, tc.want)
			}
			if shape := contextBoundaryShape(t, history); !equalStrings(shape, []string{"system:first", "user:one", "system:second", "user:two"}) {
				t.Fatalf("context transform mutated history: %v", shape)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contextBoundaryThroughRunner(t *testing.T, ext extension.Extension, want []string) {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	defer runner.Invalidate("test complete")
	history := contextBoundaryTranscript()
	got, err := (&Session{}).contextPhase(t.Context(), runner, history)
	if err != nil {
		t.Fatal(err)
	}
	if shape := contextBoundaryShape(t, got); !equalStrings(shape, want) {
		t.Fatalf("context = %q, want %q", shape, want)
	}
	if shape := contextBoundaryShape(t, history); !equalStrings(shape, []string{"system:first", "user:one", "system:second", "user:two"}) {
		t.Fatalf("context transform mutated history: %q", shape)
	}
}

// Source: runner.ts emitContext compares the visible snapshot even when a
// handler returns nothing, so reordering event.messages in place is a
// replacement that collapses the system messages. EOM-001.
func TestContextPhaseInPlaceReorderCollapsesSystemBoundaries(t *testing.T) {
	contextBoundaryThroughRunner(t, extension.Extension{Path: "reorder", Handlers: map[string][]extension.HandlerFn{"context": {func(args ...any) (any, error) {
		event := args[0].(extension.ContextEvent)
		event.Messages[0], event.Messages[1] = event.Messages[1], event.Messages[0]
		return nil, nil
	}}}}, []string{"system:first\n\nsecond", "user:two", "user:one"})
}

// Source: runner.ts sameMessages compares identity, not values: a fresh
// message with identical fields is still a replacement. EOM-002.
func TestContextPhaseEqualValueReplacementCollapsesSystemBoundaries(t *testing.T) {
	contextBoundaryThroughRunner(t, extension.Extension{Path: "equal", Handlers: map[string][]extension.HandlerFn{"context": {func(args ...any) (any, error) {
		event := args[0].(extension.ContextEvent)
		first := event.Messages[0]
		copyUser := *first.User
		first.User = &copyUser
		return &extension.ContextEventResult{Messages: []extension.AgentMessage{first, event.Messages[1]}}, nil
	}}}}, []string{"system:first\n\nsecond", "user:one", "user:two"})
}

// A handler error drops its list changes (upstream's catch skips
// restoreSystemMessages), so an in-place reorder before a throw keeps the
// original order and system boundaries.
func TestContextPhaseErroredReorderKeepsSystemBoundaries(t *testing.T) {
	contextBoundaryThroughRunner(t, extension.Extension{Path: "reorder-error", Handlers: map[string][]extension.HandlerFn{"context": {func(args ...any) (any, error) {
		event := args[0].(extension.ContextEvent)
		event.Messages[0], event.Messages[1] = event.Messages[1], event.Messages[0]
		return nil, errors.New("boom")
	}}}}, []string{"system:first", "user:one", "system:second", "user:two"})
}
