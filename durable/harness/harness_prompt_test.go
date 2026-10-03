// Ports packages/durable/test/harness-prompt.test.ts.

package harness

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

func promptSection(key string, render func(ctx context.Context, input durable.PromptInput) (*string, error), tag ...bool) *durable.PromptSection {
	section := &durable.PromptSection{Key: key, Render: render}
	if len(tag) > 0 {
		section.Tag = new(tag[0])
	}
	return section
}

var promptInput = durable.PromptInput{
	ConversationId: 1,
	Agent:          durable.Agent{ThinkingLevel: "off", Extensions: []*durable.Extension{}, Tools: []*durable.ToolRegistration{}, Sections: []*durable.PromptSection{}},
	Shown:          map[string]string{},
	Read:           emptyReader{},
}

func entriesOf(values *OrderedMap[string]) [][2]string {
	out := [][2]string{}
	for _, key := range values.Keys() {
		value, _ := values.Get(key)
		out = append(out, [2]string{key, value})
	}
	return out
}

func orderedOf(pairs ...[2]string) *OrderedMap[string] {
	values := NewOrderedMap[string]()
	for _, pair := range pairs {
		values.Set(pair[0], pair[1])
	}
	return values
}

func failing(message string) func(context.Context, durable.PromptInput) (*string, error) {
	return func(context.Context, durable.PromptInput) (*string, error) { return nil, errors.New(message) }
}

func TestSystemPromptPreparation(t *testing.T) {
	t.Run("renders sections in order with tags, omissions, wrappers, and failures", func(t *testing.T) {
		registry := CreateRegistry()
		addSection(t, registry, "preamble", text("You are helpful."), SectionOptions{Tag: new(false)})
		addSection(t, registry, "cwd", text("/repo"))
		addSection(t, registry, "skipped", func(context.Context, durable.PromptInput) (*string, error) { return nil, nil })
		addSection(t, registry, "failing", failing("render failed"))
		addSection(t, registry, "new-failing", failing("also failed"))
		mustInstall(t, registry, new(durable.Extension{
			Name: "git",
			Wraps: []durable.Wrap{WrapSection("cwd", func(inner *durable.PromptSection) *durable.PromptSection {
				wrapped := *inner
				wrapped.Render = func(ctx context.Context, value durable.PromptInput) (*string, error) {
					rendered, err := inner.Render(ctx, value)
					if err != nil {
						return nil, err
					}
					return new(*rendered + " (git)"), nil
				}
				return &wrapped
			})},
		}))
		var reports []error
		shown := orderedOf([2]string{"failing", "<failing>\nold\n</failing>"}, [2]string{"cwd", "stale"})
		agent := ResolveAgent(nil, registry.Snapshot(), ResolveSettings(nil), func(err error) { panic(err) })
		desired, err := RenderSections(context.Background(), agent.Sections, promptInput, shown, func(err error) { reports = append(reports, err) })
		if err != nil {
			t.Fatal(err)
		}
		want := [][2]string{{"preamble", "You are helpful."}, {"cwd", "<cwd>\n/repo (git)\n</cwd>"}, {"failing", "<failing>\nold\n</failing>"}}
		if got := entriesOf(desired); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
		var messages []string
		for _, report := range reports {
			messages = append(messages, report.Error())
		}
		expectStrings(t, messages, []string{"render failed", "also failed"})
	})

	t.Run("propagates section errors after cancellation", func(t *testing.T) {
		cancelled, cancel := context.WithCancelCause(context.Background())
		cancel(errors.New("cancelled"))
		_, err := RenderSections(cancelled, []*durable.PromptSection{promptSection("a", failing("cancelled"))}, promptInput, NewOrderedMap[string](), func(error) {})
		if err == nil || err.Error() != "cancelled" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("replays sections in place, deletes on null, and appends re-additions", func(t *testing.T) {
		system := func(sections ...ai.PromptSection) ai.Message {
			return ai.SystemMessage{Content: ai.SystemText(""), Sections: sections, Timestamp: 1}
		}
		set := func(name, value string) ai.PromptSection { return ai.PromptSection{Name: name, Value: new(value)} }
		shown := ReplaySections([]ai.Message{
			system(set("a", "1"), set("b", "2"), set("c", "3")),
			user("x"),
			system(set("b", "20"), ai.PromptSection{Name: "a"}),
			system(set("a", "10")),
		})
		want := [][2]string{{"b", "20"}, {"c", "3"}, {"a", "10"}}
		if got := entriesOf(shown); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}
