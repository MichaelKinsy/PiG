// Ports packages/durable/test/examples/26-coding-agent.ts, 27-plan-mode.ts and 28-reviewer.ts. Upstream's scripts
// print; each Go example asserts what the script prints.

package examples_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// lastToolResultText is the text of the newest tool result of the conversation, trimmed.
func lastToolResultText(t *testing.T, conversation harness.Conversation) string {
	t.Helper()
	page := must(conversation.Entries(background, durable.EntryQuery{}, 10, nil))
	for _, entry := range page.Items {
		if durable.ToolResultEntry.Is(&entry) {
			var text strings.Builder
			for _, part := range entry.Model[0].(ai.ToolResultMessage).Content {
				if part, ok := part.(ai.TextContent); ok {
					text.WriteString(strings.TrimSpace(part.Text))
				}
			}
			return text.String()
		}
	}
	t.Fatal("no tool result")
	return ""
}

// 26-coding-agent.ts: the built-in coding tools, a prompt that knows the working directory, settings backed by the
// app's settings file, and an environment that follows the conversation's directory.
func TestExample26CodingAgent(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The app's own prompt, next to the coding tools.
	untagged := false
	coding := new(durable.Extension{Name: "coding", Sections: []*durable.PromptSection{
		harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) {
			return new("You are a coding agent. Use the tools to inspect the project."), nil
		}, harness.SectionOptions{Tag: &untagged}),
		harness.Section("cwd", func(_ context.Context, input durable.PromptInput) (*string, error) {
			if input.Env == nil {
				return nil, nil
			}
			return new(input.Env.Cwd()), nil
		}),
	}})
	registry := harness.CreateRegistry()
	installed(t, registry, tools.CodingTools)
	installed(t, registry, coding)

	// Settings the user edits while the agent runs: the getter makes every Harness read see the current values.
	var parallelTools atomic.Bool
	parallelTools.Store(true)
	var modes []string
	var modesMu sync.Mutex
	var maxRetries atomic.Int64
	maxRetries.Store(3)
	settings := func() *harness.HarnessSettings {
		mode := durable.ToolExecutionMode("sequential")
		if parallelTools.Load() {
			mode = "parallel"
		}
		modesMu.Lock()
		modes = append(modes, string(mode))
		modesMu.Unlock()
		return &harness.HarnessSettings{ToolExecution: mode, Retry: &harness.RetryPolicyPatch{MaxRetries: new(int(maxRetries.Load()))}}
	}
	// The Harness calls this for every tool call and request with the conversation's cwd, so changing a
	// conversation's directory needs no restart.
	environment := func(_ context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
		cwd := workspace
		if target.Cwd != nil {
			cwd = *target.Cwd
		}
		return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}), nil
	}
	pwd := func() ai.FauxResponseStep { return fauxToolTurn("bash", map[string]any{"command": "pwd"}, "") }
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models: fauxModels(pwd(), fauxAnswer("Done."), pwd(), fauxAnswer("Done.")), Registry: registry, Settings: settings, Env: environment,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel), Cwd: harness.SetTo(workspace)}}))

	say(t, root, "Where are we?")
	if got := lastToolResultText(t, root); got != workspace {
		t.Fatalf("bash ran in %q, want %q", got, workspace)
	}

	// The user switches the project directory and turns off parallel tools. Both apply from the next use.
	if err := root.Configure(background, harness.AgentChange{Cwd: harness.SetTo(filepath.Join(workspace, "app"))}); err != nil {
		t.Fatal(err)
	}
	parallelTools.Store(false)
	say(t, root, "And now?")
	if got := lastToolResultText(t, root); got != filepath.Join(workspace, "app") {
		t.Fatalf("bash ran in %q, want %q", got, filepath.Join(workspace, "app"))
	}
	// Every Harness read saw the current file: first parallel, and sequential once the user turned it off.
	modesMu.Lock()
	defer modesMu.Unlock()
	if len(modes) == 0 || modes[0] != "parallel" || modes[len(modes)-1] != "sequential" {
		t.Fatalf("tool execution modes read: %v", modes)
	}
	closeSession(t, opened)
}

type plan struct {
	Steps []string `json:"steps"`
}

// 27-plan-mode.ts: the user switches a conversation into a read-only planning mode, the agent writes a plan into the
// extension's own document, and switching back restores the full tool set.
// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of Conversation.Context fails it
// mutation-checked: dropping the reads and writes of Extension.Sections, Extension.Tools fails it
func TestExample27PlanMode(t *testing.T) {
	// The current plan of a conversation. A fork keeps the plan it had at the fork entry.
	planDoc := durable.DefineDoc(durable.DocDefinition[plan]{
		CommonDocDefinition: durable.CommonDocDefinition[plan]{Kind: "app.plan", Version: 1, Initial: func() plan { return plan{Steps: []string{}} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
	})
	submitPlan := new(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{
			Name:        "submit_plan",
			Description: "Submit the plan as a list of steps.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"steps": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
				"required":   []any{"steps"},
			},
		},
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			steps, _ := args.(map[string]any)["steps"].([]any)
			if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				draft, err := durable.TxDoc[plan](tx, planDoc, api.ConversationId())
				if err != nil {
					return nil, err
				}
				return nil, draft.Set("steps", steps)
			}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			result := textResult("Plan submitted.")
			result.Control = &durable.ToolControl{Terminate: true}
			return result, nil
		},
	})
	planExtension := new(durable.Extension{
		Name:  "plan",
		Tools: []*durable.ToolRegistration{submitPlan},
		Sections: []*durable.PromptSection{harness.Section("plan_mode", func(context.Context, durable.PromptInput) (*string, error) {
			return new("You are in plan mode. Read the code, then call submit_plan. Change nothing."), nil
		})},
	})
	// Plan mode is a change to the conversation's agent: select the plan extension and offer only reading and
	// submitting. Clearing both returns to the host's default selection and every tool.
	enterPlanMode := harness.AgentChange{
		Extensions: harness.SetTo(harness.ExtensionChange{Add: []*durable.Extension{planExtension}}),
		Tools:      harness.SetTo(harness.ToolChange{Exact: true, List: []*durable.ToolRegistration{tools.CreateReadTool(), submitPlan}}),
	}
	leavePlanMode := harness.AgentChange{Extensions: harness.Cleared[harness.ExtensionChange](), Tools: harness.Cleared[harness.ToolChange]()}

	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "server.ts"), []byte("app.listen(3000);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := harness.CreateRegistry()
	installed(t, registry, tools.CodingTools)
	installed(t, registry, planExtension)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models: fauxModels(
			fauxToolTurn("read", map[string]any{"path": "server.ts"}, ""),
			fauxToolTurn("submit_plan", map[string]any{"steps": []any{"Read PORT from the environment", "Default to 3000"}}, ""),
			fauxAnswer("Implementing step 1."),
		),
		Registry: registry,
		// Installed, but only CodingTools is selected by default: conversations opt into plan mode.
		Settings: func() *harness.HarnessSettings {
			return &harness.HarnessSettings{Extensions: []*durable.Extension{tools.CodingTools}}
		},
		Env: func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) {
			return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: directory}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	full := toolNames(t, root)
	expectEqual(t, "tools", full, []string{"read", "write", "edit", "bash"})

	if err := root.Configure(background, enterPlanMode); err != nil {
		t.Fatal(err)
	}
	expectEqual(t, "plan mode tools", toolNames(t, root), []string{"read", "submit_plan"})
	say(t, root, "Make the port configurable.")
	current := must(durable.Snapshot[plan](background, opened, planDoc, root.Id()))
	if current == nil {
		t.Fatal("no plan document")
	}
	expectEqual(t, "plan", current.Steps, []string{"Read PORT from the environment", "Default to 3000"})

	if err := root.Configure(background, leavePlanMode); err != nil {
		t.Fatal(err)
	}
	expectEqual(t, "tools again", toolNames(t, root), full)
	say(t, root, "Go ahead.")

	// The model saw each switch as a system prompt change in its transcript.
	type change struct {
		sections       map[string]*string
		added, removed []string
	}
	var changes []change
	for _, message := range must(root.Context(background, nil)).Messages {
		system, ok := message.(ai.SystemMessage)
		if !ok {
			continue
		}
		next := change{sections: map[string]*string{}}
		for _, section := range system.Sections {
			next.sections[section.Name] = section.Value
		}
		for _, tool := range system.ToolsAdded {
			next.added = append(next.added, tool.Name)
		}
		for _, tool := range system.ToolsRemoved {
			next.removed = append(next.removed, tool.Name)
		}
		changes = append(changes, next)
	}
	// The first prompt is sent in plan mode; leaving it is one more system message: a null section value removes
	// the plan section, and the tools outside the plan filter come back.
	if len(changes) != 2 {
		t.Fatalf("system messages = %d, want the plan-mode prompt and the switch back", len(changes))
	}
	if changes[0].sections["plan_mode"] == nil || !strings.Contains(*changes[0].sections["plan_mode"], "plan mode") {
		t.Fatalf("entering plan mode: %+v", changes[0])
	}
	expectEqual(t, "tools offered in plan mode", changes[0].added, []string{"read", "submit_plan"})
	if value, present := changes[1].sections["plan_mode"]; !present || value != nil {
		t.Fatalf("leaving plan mode must remove the plan_mode section: %+v", changes[1])
	}
	expectEqual(t, "tools removed on leaving", changes[1].removed, []string{"submit_plan"})
	expectEqual(t, "tools added on leaving", changes[1].added, []string{"write", "edit", "bash"})
	closeSession(t, opened)
}

// 28-reviewer.ts: a reviewer agent next to the main one: a cheaper model, a review role and loop, read-only tools,
// and its own checkout of the project.
// mutation-checked: zeroing the results of Conversation.Entries fails it
func TestExample28Reviewer(t *testing.T) {
	const done = "No further findings."
	// A role and a review loop: every answer that still has findings gets a second pass.
	reviewer := new(durable.Extension{
		Name: "reviewer",
		Sections: []*durable.PromptSection{harness.Section("role", func(context.Context, durable.PromptInput) (*string, error) {
			return new("You review diffs. Report problems as a list. Never edit files."), nil
		})},
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
			OnYield: func(_ context.Context, answer ai.AssistantMessage, _ harness.HookApi) (*harness.YieldContinue, error) {
				if strings.Contains(assistantText(answer), done) {
					return nil, nil
				}
				return &harness.YieldContinue{Continue: ai.UserText(`Look again for anything you missed. Say "` + done + `" when there is nothing left.`)}, nil
			},
		})},
	})
	// The reviewer works in its own checkout, in practice a `git worktree add`.
	worktree, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "user.ts"), []byte("export const name = (user) => user.name;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "big"}, {ID: "small"}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{
		fauxToolTurn("read", map[string]any{"path": "user.ts"}, ""),
		fauxAnswer("1. `name` does not handle a missing user."),
		fauxAnswer("2. `user` has no type. " + done),
	})
	registry := harness.CreateRegistry()
	installed(t, registry, tools.CodingTools)
	installed(t, registry, reviewer)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models:   models,
		Registry: registry,
		// The main agent selects only CodingTools; the reviewer opts in.
		Settings: func() *harness.HarnessSettings {
			return &harness.HarnessSettings{Extensions: []*durable.Extension{tools.CodingTools}}
		},
		Env: func(_ context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
			cwd, _ := os.Getwd()
			if target.Cwd != nil {
				cwd = *target.Cwd
			}
			return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "big"})}}))

	// Everything the reviewer is, stored on its conversation: the model, exactly these extensions in this order,
	// only the read tool, and its directory. A restart keeps all of it.
	review := must(opened.CreateConversation(background, harness.ConversationCreateOptions{
		Ownership: ownerless.Ownership,
		Agent: &harness.AgentChange{
			Model:      harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "small"}),
			Extensions: harness.SetTo(harness.ExtensionChange{Exact: true, List: []*durable.Extension{tools.CodingTools, reviewer}}),
			Tools:      harness.SetTo(harness.ToolChange{Exact: true, List: []*durable.ToolRegistration{tools.CreateReadTool()}}),
			Cwd:        harness.SetTo(worktree),
		},
	}))
	agent := must(review.Agent(background))
	var extensionNames []string
	for _, extension := range agent.Extensions {
		extensionNames = append(extensionNames, extension.Name)
	}
	if agent.Model == nil || agent.Model.ModelId != "small" || agent.Cwd == nil || *agent.Cwd != worktree {
		t.Fatalf("reviewer agent: %+v", agent)
	}
	expectEqual(t, "reviewer extensions", extensionNames, []string{"coding-tools", "reviewer"})
	expectEqual(t, "reviewer tools", toolNames(t, review), []string{"read"})

	say(t, review, "Review user.ts.")
	page := must(review.Entries(background, durable.EntryQuery{}, 20, nil))
	var transcript []string
	for _, entry := range slices.Backward(page.Items) {

		if len(entry.Model) == 0 {
			continue
		}
		switch message := entry.Model[0].(type) {
		case ai.UserMessage:
			if text, ok := message.Content.(ai.UserText); ok {
				transcript = append(transcript, "> "+string(text))
			}
		case ai.AssistantMessage:
			if text := assistantText(message); text != "" {
				transcript = append(transcript, "reviewer: "+text)
			}
		}
	}
	expectEqual(t, "review transcript", transcript, []string{
		"> Review user.ts.",
		"reviewer: 1. `name` does not handle a missing user.",
		`> Look again for anything you missed. Say "` + done + `" when there is nothing left.`,
		"reviewer: 2. `user` has no type. " + done,
	})
	closeSession(t, opened)
}
