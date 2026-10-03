// Ports packages/durable/test/examples/14-chat.ts, 15-system-prompt.ts and 17-coding-tools.ts.

package examples_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	jsonlnode "github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

var fauxModel = durable.ModelRef{Provider: "faux", ModelId: "faux-1"}

func fauxAnswer(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}

func fauxToolTurn(name string, args map[string]any, id string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, args, id)}, StopReason: "toolUse"})
}

func fauxModels(responses ...ai.FauxResponseStep) *ai.Models {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses(responses)
	return models
}

func say(t *testing.T, conversation harness.Conversation, text string) durable.SubmissionRecord {
	t.Helper()
	submission, err := conversation.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(background)
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

// answerText is the text of the assistant entry a settled submission answered with.
func answerText(t *testing.T, conversation harness.Conversation, settled durable.SubmissionRecord) string {
	t.Helper()
	if settled.Status != durable.SubmissionDone || settled.Answer == nil {
		t.Fatalf("submission = %+v", settled)
	}
	entry := commit(t, conversation, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(*settled.Answer) })
	if entry == nil || len(entry.Model) == 0 {
		t.Fatalf("answer entry = %+v", entry)
	}
	assistant, ok := entry.Model[0].(ai.AssistantMessage)
	if !ok || len(assistant.Content) == 0 {
		t.Fatalf("answer = %+v", entry.Model[0])
	}
	return assistant.Content[0].(ai.TextContent).Text
}

// transcriptKinds is the transcript's entry kinds, oldest first.
func transcriptKinds(t *testing.T, conversation harness.Conversation) []string {
	t.Helper()
	page := must(conversation.Entries(background, durable.EntryQuery{}, 20, nil))
	kinds := []string{}
	for _, v := range slices.Backward(page.Items) {
		kinds = append(kinds, v.Kind)
	}
	return kinds
}

// 14-chat.ts: one question answered by the faux model, with a system preamble from an extension section.
func TestExample14Chat(t *testing.T) {
	registry := harness.CreateRegistry()
	untagged := false
	installed(t, registry, new(durable.Extension{
		Name: "terse",
		Sections: []*durable.PromptSection{{Key: "preamble", Tag: &untagged, Render: func(context.Context, durable.PromptInput) (*string, error) {
			text := "You answer in one word."
			return &text, nil
		}}},
	}))
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: fauxModels(fauxAnswer("Paris.")), Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))

	answered := say(t, root, "Capital of France?")
	if got := answerText(t, root, answered); got != "Paris." {
		t.Fatalf("answer: %q", got)
	}
	expectEqual(t, "transcript", transcriptKinds(t, root), []string{"pi.user", "pi.system", "pi.assistant"})
	closeSession(t, opened)
}

// 15-system-prompt.ts: extensions contribute prompt sections; wrappers change them; a subagent differs by extension
// set and instructions; the cwd is re-rendered after it changes.
func TestExample15SystemPrompt(t *testing.T) {
	untagged := false
	section := func(key string, render func(input durable.PromptInput) *string, tag *bool) *durable.PromptSection {
		return &durable.PromptSection{Key: key, Tag: tag, Render: func(_ context.Context, input durable.PromptInput) (*string, error) {
			return render(input), nil
		}}
	}
	text := func(value string) func(durable.PromptInput) *string {
		return func(durable.PromptInput) *string { return &value }
	}
	coding := new(durable.Extension{Name: "coding", Sections: []*durable.PromptSection{
		section("preamble", text("You are a coding agent."), &untagged),
		section("cwd", func(input durable.PromptInput) *string {
			if input.Env == nil {
				return nil
			}
			cwd := input.Env.Cwd()
			return &cwd
		}, nil),
	}})
	agentsMd := new(durable.Extension{Name: "agents-md", Sections: []*durable.PromptSection{section("agents_md", text("Run npm run check after changes."), nil)}})
	terse := new(durable.Extension{Name: "terse", Wraps: []durable.Wrap{{Section: "preamble", WrapSection: func(preamble *durable.PromptSection) *durable.PromptSection {
		wrapped := *preamble
		wrapped.Render = func(ctx context.Context, input durable.PromptInput) (*string, error) {
			rendered, err := preamble.Render(ctx, input)
			if err != nil || rendered == nil {
				return rendered, err
			}
			withSuffix := *rendered + " Be terse."
			return &withSuffix, nil
		}
		return &wrapped
	}}}})

	registry := harness.CreateRegistry()
	installed(t, registry, coding)
	installed(t, registry, agentsMd)
	installed(t, registry, terse)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models:   fauxModels(fauxAnswer("Done."), fauxAnswer("Done."), fauxAnswer("Done.")),
		Registry: registry,
		Env: func(_ context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
			cwd := "/"
			if target.Cwd != nil {
				cwd = *target.Cwd
			}
			return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := "/repo"
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel), Cwd: harness.SetTo(repo)}}))
	subagent := must(opened.CreateConversation(background, harness.ConversationCreateOptions{
		Ownership: ownerless.Ownership,
		Agent: &harness.AgentChange{
			Model:        harness.SetTo(fauxModel),
			Cwd:          harness.SetTo(repo),
			Extensions:   harness.SetTo(harness.ExtensionChange{Remove: []*durable.Extension{agentsMd}}),
			Instructions: harness.SetTo("Only read; never edit files."),
		},
	}))

	// systemSections is the sections of each pi.system entry, oldest first.
	systemSections := func(conversation harness.Conversation) []map[string]string {
		t.Helper()
		page := must(conversation.Entries(background, durable.EntryQuery{}, 20, nil))
		var all []map[string]string
		for i := len(page.Items) - 1; i >= 0; i-- {
			if !durable.SystemEntry.Is(&page.Items[i]) {
				continue
			}
			for _, message := range page.Items[i].Model {
				rendered := map[string]string{}
				for _, section := range message.(ai.SystemMessage).Sections {
					if section.Value != nil {
						rendered[section.Name] = *section.Value
					}
				}
				all = append(all, rendered)
			}
		}
		return all
	}

	say(t, root, "Fix the build.")
	say(t, subagent, "Read the logs.")
	rootPrompt := systemSections(root)
	expectEqual(t, "root system prompt", rootPrompt, []map[string]string{{
		"preamble":  "You are a coding agent. Be terse.",
		"cwd":       "<cwd>\n/repo\n</cwd>",
		"agents_md": "<agents_md>\nRun npm run check after changes.\n</agents_md>",
	}})
	subagentPrompt := systemSections(subagent)
	if len(subagentPrompt) != 1 {
		t.Fatalf("subagent system prompt: %v", subagentPrompt)
	}
	if _, hasAgentsMd := subagentPrompt[0]["agents_md"]; hasAgentsMd {
		t.Fatalf("the subagent kept agents_md: %v", subagentPrompt[0])
	}
	if !strings.Contains(subagentPrompt[0]["instructions"], "Only read; never edit files.") {
		t.Fatalf("subagent instructions: %v", subagentPrompt[0])
	}

	if err := root.Configure(background, harness.AgentChange{Cwd: harness.SetTo("/repo/packages")}); err != nil {
		t.Fatal(err)
	}
	say(t, root, "Now the package.")
	after := systemSections(root)
	if len(after) != 2 || !strings.Contains(after[1]["cwd"], "/repo/packages") {
		t.Fatalf("root system entries after cwd change: %v", after)
	}
	closeSession(t, opened)
}

// 17-coding-tools.ts: a coding-agent tool turn on JSONL storage: the model reads, edits and runs commands, then
// answers. The upstream timing hook around `cat /tmp/1gb.txt` only measures and prints; it is not ported.
func TestExample17CodingTools(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "missing.txt") // upstream cats /tmp/1gb.txt, which does not exist unless created
	models := fauxModels(
		fauxToolTurn("read", map[string]any{"path": "notes.txt"}, "r"),
		fauxToolTurn("edit", map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "world", "newText": "durable"}}}, "e"),
		fauxToolTurn("bash", map[string]any{"command": "cat notes.txt"}, "b"),
		fauxToolTurn("bash", map[string]any{"command": "cat " + missing}, "c"),
		fauxAnswer("The file now greets durable."),
	)
	registry := harness.CreateRegistry()
	installed(t, registry, tools.CodingTools)

	storageDirectory := filepath.Join(directory, "storage")
	store, err := jsonlnode.OpenNodeJsonlStorage(background, storageDirectory, jsonl.JsonlStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env: func(_ context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
			cwd := directory
			if target.Cwd != nil {
				cwd = *target.Cwd
			}
			return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel), Cwd: harness.SetTo(directory)}}))

	settled := say(t, root, "Greet durable instead.")
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("status: %+v", settled)
	}
	page := must(root.Entries(background, durable.EntryQuery{}, 20, nil))
	type toolOutput struct {
		name    string
		text    string
		isError bool
	}
	var outputs []toolOutput
	for i := len(page.Items) - 1; i >= 0; i-- {
		if !durable.ToolResultEntry.Is(&page.Items[i]) {
			continue
		}
		result := page.Items[i].Model[0].(ai.ToolResultMessage)
		var text strings.Builder
		for _, item := range result.Content {
			if content, ok := item.(ai.TextContent); ok {
				text.WriteString(content.Text)
			}
		}
		outputs = append(outputs, toolOutput{result.ToolName, text.String(), result.IsError})
	}
	if len(outputs) != 4 {
		t.Fatalf("tool results: %+v", outputs)
	}
	if outputs[0].name != "read" || outputs[0].text != "hello world\n" {
		t.Fatalf("read result: %+v", outputs[0])
	}
	if outputs[1].name != "edit" || !strings.Contains(outputs[1].text, "Successfully replaced 1 block(s) in notes.txt.") {
		t.Fatalf("edit result: %+v", outputs[1])
	}
	if outputs[2].name != "bash" || outputs[2].text != "hello durable\n" || outputs[2].isError {
		t.Fatalf("bash result: %+v", outputs[2])
	}
	if outputs[3].name != "bash" || !outputs[3].isError || !strings.Contains(outputs[3].text, "No such file") {
		t.Fatalf("failing bash result: %+v", outputs[3])
	}
	if got := answerText(t, root, settled); got != "The file now greets durable." {
		t.Fatalf("answer: %q", got)
	}
	content, err := os.ReadFile(filepath.Join(directory, "notes.txt"))
	if err != nil || string(content) != "hello durable\n" {
		t.Fatalf("file: %q %v", content, err)
	}
	closeSession(t, opened)
}
