// Ports packages/durable/test/examples/34-submit-in-commit.ts: a host lets people message an agent while it works: a
// message must join the current run as a steer and never start a run of its own. Conversation.SubmitIn admits it inside
// the host's own commit, together with the host's check and its own state; WhenIdleReject refuses it when no run is
// working. The script's printing is the transcript this test reads.
//
// Go mapping: upstream's `working` promise is a channel the build tool waits on; nothing else differs.

package examples_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type visitorMessage struct {
	From string `json:"from"`
	Text string `json:"text"`
}

type visitors struct {
	Muted []string         `json:"muted"`
	Sent  []visitorMessage `json:"sent"`
}

// Pi: packages/durable/src/harness/conversation.ts (submit(tx, draft)), whenIdle "reject".
func TestExample34SubmitInCommit(t *testing.T) {
	// The host's own state: who may message this conversation, and what they sent.
	visitorsDoc := durable.DefineDoc(durable.DocDefinition[visitors]{
		CommonDocDefinition: durable.CommonDocDefinition[visitors]{Kind: "app.visitors", Version: 1, Initial: func() visitors { return visitors{Muted: []string{}, Sent: []visitorMessage{}} }, CheckpointWhen: func(visitors, []durable.Op, durable.CheckpointInfo) bool { return false }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
	})

	// A tool that waits until released, so messages arrive while the run works.
	release := make(chan struct{})
	build := &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "build", Description: "Build the project", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			<-release
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "built"}}}, nil
		},
	}

	models := fauxModels(
		fauxToolTurn("build", map[string]any{}, "b1"),
		fauxAnswer("Built, and I added the test Ana asked for."),
	)
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "build", Tools: []*durable.ToolRegistration{build}})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	must(root.Commit(background, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc[visitors](tx, visitorsDoc, root.Id())
		if err != nil {
			return nil, err
		}
		_, err = draft.Array("muted").Push("bob")
		return nil, err
	}))

	// A visitor's message: checked, admitted as a steer of the current run, and recorded, in one commit or not at all.
	message := func(from, text, requestId string) string {
		result, err := opened.Commit(background, func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc[visitors](tx, visitorsDoc, root.Id())
			if err != nil {
				return nil, err
			}
			muted, err := durable.FromJsonValue[[]string](draft.Array("muted").Snapshot())
			if err != nil {
				return nil, err
			}
			if slices.Contains(muted, from) {
				return from + " is muted", nil
			}
			// Before the commit's own table writes; document writes may come before or after.
			id, err := root.SubmitIn(tx, durable.SubmissionDraft{
				Type:      durable.SubmissionTypeInput,
				Content:   ai.UserText(from + ": " + text),
				WhenBusy:  durable.WhenBusySteer,
				WhenIdle:  durable.WhenIdleReject,
				RequestId: &requestId,
			})
			if err != nil {
				return nil, err
			}
			if _, err := draft.Array("sent").Push(map[string]any{"from": from, "text": text}); err != nil {
				return nil, err
			}
			return fmt.Sprintf("%s's message is submission %d", from, id), nil
		})
		if _, ok := errors.AsType[*durable.ConversationIdle](err); ok {
			return from + ": the agent isn't working now"
		}
		if err != nil {
			t.Fatal(err)
		}
		return result.(string)
	}

	expectEqual(t, "idle message", message("ana", "Add a test for the build.", "ana-1"), "ana: the agent isn't working now") // idle: refused, nothing written
	run := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Build the project.")}))
	// busy: joins the run as a steer
	if got := message("ana", "Add a test for the build.", "ana-2"); !strings.HasPrefix(got, "ana's message is submission ") {
		t.Fatalf("busy message = %q, want ana's message admitted as a submission", got)
	}
	expectEqual(t, "muted message", message("bob", "Delete everything.", "bob-1"), "bob is muted") // the host's own check refuses it
	close(release)
	must(run.Wait(background))
	if err := root.WaitForIdle(background); err != nil {
		t.Fatal(err)
	}

	page := must(root.Entries(background, durable.EntryQuery{Order: new(durable.ScanAscending)}, 100, nil))
	var lines []string
	for _, entry := range page.Items {
		if len(entry.Model) == 0 {
			continue
		}
		switch message := entry.Model[0].(type) {
		case ai.UserMessage:
			text, _ := message.Content.(ai.UserText)
			lines = append(lines, "> "+string(text))
		case ai.AssistantMessage:
			if message.StopReason != "stop" {
				continue
			}
			var text strings.Builder
			for _, part := range message.Content {
				if content, ok := part.(ai.TextContent); ok {
					text.WriteString(content.Text)
				}
			}
			lines = append(lines, "agent: "+text.String())
		}
	}
	expectEqual(t, "transcript", lines, []string{
		"> Build the project.",
		"> ana: Add a test for the build.",
		"agent: Built, and I added the test Ana asked for.",
	})
	recorded := must(durable.Snapshot(background, opened, visitorsDoc, root.Id()))
	expectEqual(t, "recorded", recorded.Sent, []visitorMessage{{From: "ana", Text: "Add a test for the build."}})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
