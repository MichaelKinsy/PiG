// Ports packages/durable/test/examples/20-inbox.ts and 30-tool-override.ts.

package examples_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// 20-inbox.ts: submissions to a busy conversation queue in pi.inbox; a queued input can be withdrawn; whenBusy reject
// fails without writing.
func TestExample20Inbox(t *testing.T) {
	held := make(chan struct{})
	slow := ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		select {
		case <-held:
		case <-options.Signal.Done():
			return ai.FauxResponse{}.AssistantMessage(), context.Cause(options.Signal)
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Answer to the first question.")}}.AssistantMessage(), nil
	})
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	faux.SetResponses([]ai.FauxResponseStep{slow, fauxAnswer("Answer to the follow-up and the steer.")})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models:   models,
		Registry: harness.CreateRegistry(),
		Settings: func() *harness.HarnessSettings { return &harness.HarnessSettings{FollowUpMode: durable.QueueAll} },
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))

	submit := func(draft durable.SubmissionDraft) durable.Submission {
		t.Helper()
		submission, err := root.Submit(background, draft)
		if err != nil {
			t.Fatal(err)
		}
		return submission
	}
	input := func(text string, whenBusy durable.WhenBusy) durable.SubmissionDraft {
		return durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text), WhenBusy: whenBusy}
	}
	first := submit(input("First question", ""))
	// The conversation is busy from here on.
	followUp := submit(input("A follow-up", ""))
	steer := submit(input("A steer", durable.WhenBusySteer))
	note := submit(durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "app.note", Data: "noted while busy"}})
	withdrawn := submit(input("Never mind", ""))
	_, err = root.Submit(background, input("Now or never", durable.WhenBusyReject))
	if err == nil {
		t.Fatal("a whenBusy reject submission to a busy conversation was admitted")
	}
	t.Logf("rejected: %v", err)
	if result, err := withdrawn.Abort(background); err != nil || result != durable.SubmissionAborted {
		t.Fatalf("withdraw: %v %v", result, err)
	}

	inbox, err := durable.Snapshot[harness.InboxState](background, opened, harness.InboxDoc, root.Id())
	if err != nil || inbox == nil {
		t.Fatalf("inbox: %v %v", inbox, err)
	}
	var modes []harness.InboxMode
	for _, item := range inbox.Items {
		modes = append(modes, item.Mode)
	}
	if len(inbox.Items) != 3 {
		t.Fatalf("inbox holds %d items (%v), want the follow-up, the steer and the note", len(inbox.Items), modes)
	}

	close(held)
	status := func(name string, submission durable.Submission, want durable.SubmissionStatus) {
		t.Helper()
		record, err := submission.Wait(background)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != want {
			t.Fatalf("%s: status %q (%v), want %q", name, record.Status, record.Reason, want)
		}
	}
	status("first", first, durable.SubmissionDone)
	status("follow-up", followUp, durable.SubmissionDone)
	status("steer", steer, durable.SubmissionDone)
	status("note", note, durable.SubmissionDone)
	status("withdrawn", withdrawn, durable.SubmissionUnanswered)

	kinds := transcriptKinds(t, root)
	if len(kinds) == 0 || kinds[0] != "pi.user" {
		t.Fatalf("transcript: %v", kinds)
	}
	hasNote := false
	for _, kind := range kinds {
		hasNote = hasNote || kind == "app.note"
	}
	if !hasNote {
		t.Fatalf("transcript lacks the note written while busy: %v", kinds)
	}
	// The withdrawn input never reached the transcript.
	page := must(root.Entries(background, durable.EntryQuery{}, 50, nil))
	for _, entry := range page.Items {
		for _, message := range entry.Model {
			if user, ok := message.(ai.UserMessage); ok && user.Content == ai.UserContent(ai.UserText("Never mind")) {
				t.Fatal("the withdrawn input was placed in the transcript")
			}
		}
	}
	closeSession(t, opened)
}

// 30-tool-override.ts: a conversation overrides one built-in tool by extension (a bash with a venv prefix), and a
// wrapper times every bash call without replacing it.
// Pi source: packages/durable/src/tools/bash.ts
// mutation-checked: dropping the reads and writes of BashToolOptions.CommandPrefix fails it
func TestExample30ToolOverride(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".venv", "bin", "activate"), []byte("export VIRTUAL_ENV=\""+project+"/.venv\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	venv := new(durable.Extension{
		Name:  "venv",
		Tools: []*durable.ToolRegistration{tools.CreateBashTool(&tools.BashToolOptions{CommandPrefix: "source .venv/bin/activate"})},
	})
	timings := 0
	timing := new(durable.Extension{
		Name: "timing",
		Wraps: []durable.Wrap{durable.ToolWrap{Tool: "bash", Wrap: func(bash *durable.ToolRegistration) *durable.ToolRegistration {
			wrapped := *bash
			wrapped.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				defer func() { timings++ }()
				return bash.Execute(ctx, args, api)
			}
			return &wrapped
		}}},
	})
	probe := func() ai.FauxResponseStep {
		return fauxToolTurn("bash", map[string]any{"command": "echo venv: $VIRTUAL_ENV"}, "probe")
	}
	models := fauxModels(probe(), fauxAnswer("Done."), probe(), fauxAnswer("Done."))
	registry := harness.CreateRegistry()
	installed(t, registry, tools.CodingTools)
	installed(t, registry, timing)
	installed(t, registry, venv)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models:   models,
		Registry: registry,
		Settings: func() *harness.HarnessSettings {
			return &harness.HarnessSettings{Extensions: []*durable.Extension{tools.CodingTools, timing}}
		},
		Env: func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) {
			return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: project}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	python := must(opened.CreateConversation(background, harness.ConversationCreateOptions{
		Ownership: ownerless.Ownership,
		Agent:     &harness.AgentChange{Model: harness.SetTo(fauxModel), Extensions: harness.SetTo(harness.ExtensionChange{Add: []*durable.Extension{venv}})},
	}))

	probeBash := func(conversation harness.Conversation) string {
		t.Helper()
		say(t, conversation, "Which venv?")
		page := must(conversation.Entries(background, durable.EntryQuery{}, 10, nil))
		for _, entry := range page.Items {
			if durable.ToolResultEntry.Is(&entry) {
				var text strings.Builder
				for _, part := range entry.Model[0].(ai.ToolResultMessage).Content {
					if content, ok := part.(ai.TextContent); ok {
						text.WriteString(strings.TrimSpace(content.Text))
					}
				}
				return text.String()
			}
		}
		t.Fatal("no tool result")
		return ""
	}
	if got := probeBash(plain); got != "venv:" {
		t.Fatalf("plain conversation: %q", got)
	}
	canonicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(strings.ReplaceAll(probeBash(python), project, "<project>"), canonicalProject, "<project>")
	if got != "venv: <project>/.venv" {
		t.Fatalf("python conversation: %q", got)
	}
	if timings != 2 {
		t.Fatalf("timed bash calls: %d, want 2", timings)
	}
	closeSession(t, opened)
}
