package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// agent-session.ts::_preparePromptAndToolLoadout persists section patches with
// empty content; forced provider prompt projection does not flatten this state.
func TestSessionFirstPromptPersistsStructuredSystemSections(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SystemPrompt: "configured instructions"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	if len(sess.Agent().Messages()) != 0 {
		t.Fatal("fresh transcript must be empty")
	}
	if _, err := sess.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	messages := sess.Agent().Messages()
	if len(messages) == 0 || messages[0].System == nil {
		t.Fatalf("missing system: %#v", messages)
	}
	system := messages[0].System
	if text, ok := system.Content.(ai.SystemText); !ok || text != "" {
		t.Fatalf("system content %#v, want empty text with sections", system.Content)
	}
	if len(system.Sections) == 0 || system.Sections[0].Name != "preamble" || system.Sections[0].Value == nil || *system.Sections[0].Value != "configured instructions" {
		t.Fatalf("system sections %#v", system.Sections)
	}
}

func TestSessionDefaultSectionsReachEventsProviderAndPersistenceOnce(t *testing.T) {
	provider := &transcriptCaptureProvider{}
	services := newTestServices(t)
	options := SessionOptions{Model: fakeModelWithProvider(provider)}
	sess, err := NewSession(services, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	if len(sess.Messages()) != 0 {
		t.Fatal("new session has transcript")
	}
	for _, prompt := range []string{"one", "two"} {
		if _, err := sess.Send(context.Background(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	check := func(messages []agent.AgentMessage) {
		t.Helper()
		systems := 0
		for _, message := range messages {
			if message.System == nil {
				continue
			}
			systems++
			var names []string
			for _, section := range message.System.Sections {
				names = append(names, section.Name)
			}
			want := []string{"preamble", "tools", "rules", "docs", "cwd"}
			if !reflect.DeepEqual(names, want) || message.System.Content != ai.SystemText("") {
				t.Fatalf("system %#v", message.System)
			}
			if len(message.System.ToolsAdded) != len(sess.Agent().Tools()) {
				t.Fatal("tool declarations not merged with section baseline")
			}
		}
		if systems != 1 {
			t.Fatalf("system messages %d", systems)
		}
	}
	check(sess.Messages())
	check(sess.Inner().BuildSessionProjection().Messages)
	for _, request := range provider.requests {
		var systems []ai.SystemMessage
		for _, message := range request.Messages() {
			if system, ok := message.(ai.SystemMessage); ok {
				systems = append(systems, system)
			}
		}
		if len(systems) != 1 || systems[0].Content != ai.SystemText("") || len(systems[0].Sections) != 5 {
			t.Fatalf("provider system %#v", systems)
		}
	}
	starts, ends := 0, 0
	drain := true
	for drain {
		select {
		case event := <-sess.Events():
			switch event := event.(type) {
			case agent.MessageStartEvent:
				if event.Message.System != nil {
					starts++
				}
			case agent.MessageEndEvent:
				if event.Message.System != nil {
					ends++
				}
			}
		default:
			drain = false
		}
	}
	// Flush the event funnel with a writer acknowledgement, rather than assuming
	// Send's return means the independent event consumer has caught up.
	flushed := make(chan error, 1)
	go func() { flushed <- sess.FlushEvents(t.Context()) }()
	for {
		event := <-sess.Events()
		if AcknowledgeEvent(event) {
			break
		}
		switch event := event.(type) {
		case agent.MessageStartEvent:
			if event.Message.System != nil {
				starts++
			}
		case agent.MessageEndEvent:
			if event.Message.System != nil {
				ends++
			}
		}
	}
	if err := <-flushed; err != nil {
		t.Fatal(err)
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("system event starts=%d ends=%d", starts, ends)
	}
	options.ResumePath = sess.Path()
	resumed, err := NewSession(services, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resumed.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := resumed.Send(context.Background(), "resumed"); err != nil {
		t.Fatal(err)
	}
	check(resumed.Messages())
}

func TestSessionSectionsDiffReplacesAndRemovesOnlyChangedState(t *testing.T) {
	services := newTestServices(t)
	desired := ai.OrderedSections{{Name: "preamble", Value: new("base")}, {Name: "cwd", Value: new("<cwd>\n/work\n</cwd>")}}
	sess, err := NewSession(services, SessionOptions{Model: fakeModel(), SystemPromptSections: desired, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	*desired[0].Value = "caller mutation"
	if _, err := sess.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	// The replayed transcript may have a different section state after resume or
	// a context edit. The next request declares only differences from that state.
	old := &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "preamble", Value: new("other")}, {Name: "plan_mode", Value: new("<plan_mode>\nPlan only.\n</plan_mode>")}}}
	if _, err := sess.Inner().AppendMessage(agent.AgentMessage{System: old}); err != nil {
		t.Fatal(err)
	}
	sess.refreshContext()
	if _, err := sess.Send(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	var systems []*ai.SystemMessage
	for _, message := range sess.Messages() {
		if message.System != nil {
			systems = append(systems, message.System)
		}
	}
	if len(systems) != 3 {
		t.Fatalf("system count %d", len(systems))
	}
	want := ai.OrderedSections{{Name: "preamble", Value: new("base")}, {Name: "plan_mode"}}
	if !reflect.DeepEqual(systems[2].Sections, want) {
		t.Fatalf("patch %#v want %#v", systems[2].Sections, want)
	}
	if got := ai.GetCurrentSystemPrompt([]ai.Message{*systems[0], *systems[1], *systems[2]}); got != "base\n\n<cwd>\n/work\n</cwd>" {
		t.Fatalf("replay %q", got)
	}
}
