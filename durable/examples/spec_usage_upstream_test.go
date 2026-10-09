// Ports packages/durable/test/spec-usage.test.ts, the examples that use durable/harness and durable/tools: extensions
// and host setup (§7.1), hooks reading extension state (§7.2), subagents (§7.3), the chat extension (§7.4), a child
// conversation in a tool commit and live settings (§2.2), an environment per conversation, the table-read rules with
// LiveDoc (§4), and the revoked draft of LiveDoc (§3.4). The root-package examples (Payment, Follow, plan-mode and
// container documents) are ported in durable/spec_usage_upstream_test.go.
//
// Upstream compiles the examples and never calls them. Here the examples are one function over the names the spec
// leaves to the application, which the test passes as zero values and never invokes the sequences of; the Go compiler
// is the type check. The revoked-draft example also runs against a real Harness, which upstream does not.

package examples_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

type (
	specPlanMode  struct{ Enabled bool }
	specContainer struct{ Image string }
	specHold      struct {
		Phase string `json:"phase"`
	}
	specReport struct {
		Phase string `json:"phase"`
	}
	specFollow struct {
		Phase string `json:"phase"`
	}
	specTask[C any] = durable.Task[durable.JsonValue, C, durable.JsonValue, struct{}]
)

var specPlanModeDoc = durable.DefineDoc(durable.DocDefinition[specPlanMode]{
	CommonDocDefinition: durable.CommonDocDefinition[specPlanMode]{Kind: "app.plan-mode", Version: 1, Initial: func() specPlanMode { return specPlanMode{Enabled: false} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
})

// specNames are the names the spec leaves to the application (spec-usage.test.ts:49-80).
type specNames struct {
	storage        durable.Storage
	models         durable.Models
	session        durable.Session
	conversationId durable.ConversationId
	message        durable.EntryDraft
	haiku          durable.ModelRef
	sonnet         durable.ModelRef
	worktree       string
	name           string
	localEnv       func(cwd string) env.ExecutionEnv
	containers     interface {
		env(ctx context.Context, image, cwd string) (env.ExecutionEnv, error)
	}
	renderAgentsMd    func() *string
	renderSkills      func() *string
	isDangerous       func(call ai.ToolCall) bool
	writes            func(call ai.ToolCall) bool
	requestSecondPass func(ctx context.Context, answer ai.AssistantMessage, api harness.HookApi) (*harness.YieldContinue, error)
	metrics           interface{ record(name string, ms int64) }
	answerText        func(ctx context.Context, api durable.ToolExecutionApi, entry durable.EntryId) (string, error)
	manager           interface {
		timeoutMs() int
		autoCompact() bool
		setAutoCompact(value bool)
	}
	anchor       specTask[specHold]
	reporter     specTask[specReport]
	subagentTool *durable.ToolRegistration
	followTask   durable.Task[durable.JsonObject, specFollow, durable.JsonValue, struct{}]
	// host receives what the host sequence builds; the test allocates it.
	host *specHost
}

// specHost collects what the host sequence builds, so the test can read it back.
type specHost struct {
	harness harness.Harness
	root    harness.Conversation
}

// specUsageHarnessExamples is `examples()` of spec-usage.test.ts without the root-package examples.
func specUsageHarnessExamples(names specNames) map[string]func(context.Context) error {
	readTool := tools.CreateReadTool()
	editTool := tools.CreateEditTool()
	bashTool := tools.CreateBashTool(nil)
	text := func(render func() *string) func(context.Context, durable.PromptInput) (*string, error) {
		return func(context.Context, durable.PromptInput) (*string, error) { return render(), nil }
	}

	// ─── Section 7.1: extensions and host setup ──────────────────────────────────

	contextFiles := new(durable.Extension{Name: "context-files", Sections: []*durable.PromptSection{harness.Section("agents-md", text(names.renderAgentsMd))}})
	skills := new(durable.Extension{Name: "skills", Sections: []*durable.PromptSection{harness.Section("skills", text(names.renderSkills))}})
	skillsV2 := new(durable.Extension{Name: "skills", Sections: []*durable.PromptSection{harness.Section("skills", text(names.renderSkills))}})
	coding := new(durable.Extension{
		Name: "coding",
		Sections: []*durable.PromptSection{
			harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) {
				return new("You are an expert coding assistant."), nil
			}, harness.SectionOptions{Tag: new(false)}),
			// The environment the host built for this conversation, in the conversation's directory.
			harness.Section("cwd", func(_ context.Context, input durable.PromptInput) (*string, error) {
				if input.Env == nil {
					return nil, nil
				}
				return new("Working directory: " + input.Env.Cwd()), nil
			}),
		},
	})
	permissions := new(durable.Extension{
		Name: "permissions",
		Hooks: []durable.HookRegistration{
			harness.Hook(harness.ToolTask, &harness.ToolHooks{BeforeTool: func(_ context.Context, call ai.ToolCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
				if names.isDangerous(call) {
					return &harness.BeforeToolResult{Block: new("Needs approval")}, nil
				}
				return nil, nil
			}}),
		},
	})
	// A role and a review loop, for conversations that select it.
	reviewer := new(durable.Extension{
		Name: "reviewer",
		Sections: []*durable.PromptSection{harness.Section("role", func(context.Context, durable.PromptInput) (*string, error) {
			return new("You review diffs. Report problems as a list. Never edit files."), nil
		})},
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{OnYield: names.requestSecondPass})},
	})

	timing := new(durable.Extension{
		Name: "timing",
		Wraps: []durable.Wrap{
			harness.WrapTool(bashTool, func(tool *durable.ToolRegistration) *durable.ToolRegistration {
				wrapped := *tool
				wrapped.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					start := time.Now()
					defer func() { names.metrics.record("bash", time.Since(start).Milliseconds()) }()
					return tool.Execute(ctx, args, api)
				}
				return &wrapped
			}),
		},
	})
	// A bash inside a Python virtualenv for one conversation: it replaces CodingTools' bash in place,
	// and Timing, if selected, wraps it.
	venv := new(durable.Extension{
		Name:  "venv",
		Tools: []*durable.ToolRegistration{tools.CreateBashTool(&tools.BashToolOptions{CommandPrefix: "source .venv/bin/activate"})},
	})

	// ─── Section 7.2: hooks reading extension state ──────────────────────────────

	planMode := new(durable.Extension{
		Name: "plan-mode",
		Hooks: []durable.HookRegistration{
			harness.Hook(harness.ToolTask, &harness.ToolHooks{
				// An absent document means plan mode is off.
				BeforeTool: func(ctx context.Context, call ai.ToolCall, api harness.HookApi) (*harness.BeforeToolResult, error) {
					plan, err := durable.Snapshot[specPlanMode](ctx, api, specPlanModeDoc, api.ConversationId())
					if err != nil {
						return nil, err
					}
					if plan != nil && plan.Enabled && names.writes(call) {
						return &harness.BeforeToolResult{Block: new("Plan mode: read-only")}, nil
					}
					return nil, nil
				},
			}),
		},
	})

	// ─── Section 7.3: subagents ──────────────────────────────────────────────────

	var subagent *durable.Extension
	subagent = new(durable.Extension{
		Name: "subagent",
		Tools: []*durable.ToolRegistration{
			new(durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{
					Name:        "subagent",
					Description: "Delegate a self-contained task to a subagent and get its answer back.",
					Parameters: map[string]any{
						"type":       "object",
						"properties": map[string]any{"task": map[string]any{"type": "string"}},
						"required":   []any{"task"},
					},
				},
				Replay: durable.ReplaySafe,
				Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					task, _ := args.(map[string]any)["task"].(string)
					created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
						taskId := api.TaskId()
						existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &taskId}, 1, nil)
						if err != nil {
							return nil, err
						}
						if len(existing.Items) > 0 {
							return existing.Items[0].Id, nil
						}
						// Starts as a copy of this conversation's agent: model, thinking level, cwd, extensions, tools.
						child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
						if err != nil {
							return nil, err
						}
						// Without this extension, the child is not offered this tool.
						err = harness.Configure(tx, child.Id, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{Remove: []*durable.Extension{subagent}})})
						return child.Id, err
					})
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					child := created.(durable.ConversationId)
					if err := api.Details(ctx, delta.JsonObjectOf("conversationId", float64(child))); err != nil {
						return durable.ToolExecutionResult{}, err
					}
					handle, err := api.Conversation(ctx, child)
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					request := durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(task), RequestId: new(fmt.Sprintf("subagent:%d", api.TaskId()))}
					submission, err := handle.Submit(ctx, request)
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					settled, err := submission.Wait(ctx)
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
						return durable.ToolExecutionResult{}, fmt.Errorf("Subagent failed: %s", settled.Status)
					}
					answer, err := names.answerText(ctx, api, *settled.Answer)
					return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: answer}}}, err
				},
			}),
		},
	})

	subagentTools := new(durable.Extension{
		Name:  "subagent-tools",
		Tasks: []durable.AnyTask{names.anchor, names.reporter},
		Tools: []*durable.ToolRegistration{names.subagentTool},
	})

	// ─── Section 7.4 ─────────────────────────────────────────────────────────────

	chat := new(durable.Extension{
		Name: "chat",
		Sections: []*durable.PromptSection{harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) {
			return new("You are a helpful assistant."), nil
		}, harness.SectionOptions{Tag: new(false)})},
	})

	sequences := map[string]func(context.Context) error{
		// Section 2.2: a tool's commit creates a configured child.
		"childInToolCommit": func(ctx context.Context) error {
			_, err := names.session.Commit(ctx, func(tx durable.Tx) (any, error) {
				// In a tool's commit. The child starts as a copy of this conversation's agent: model, extensions, tools, cwd.
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: 1}})
				if err != nil {
					return nil, err
				}
				// A cheaper model, only the read tool, and its own worktree; everything else stays as copied.
				return nil, harness.Configure(tx, child.Id, harness.AgentChange{
					Model: harness.SetTo(names.haiku),
					Tools: harness.SetTo(harness.ToolChange{Exact: true, List: []*durable.ToolRegistration{readTool}}),
					Cwd:   harness.SetTo(names.worktree),
				})
			})
			return err
		},

		// Section 2.2: an environment per conversation.
		"containerEnv": func(ctx context.Context) error {
			registry := harness.CreateRegistry()
			// Absent: the conversation runs locally. Only conversations with this document run in a container.
			// Subagents do not copy it: their creator writes it too when they should run in the container.
			containerDoc := durable.DefineDoc(durable.DocDefinition[specContainer]{
				CommonDocDefinition: durable.CommonDocDefinition[specContainer]{Kind: "app.container", Version: 1, Initial: func() specContainer { return specContainer{Image: "node:22"} }},
				DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
			})
			_, err := harness.OpenHarness(ctx, names.storage, harness.HarnessOptions{
				Models:   names.models,
				Registry: registry,
				Env: func(ctx context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
					container, err := durable.Snapshot[specContainer](ctx, target.Read, containerDoc, target.ConversationId)
					if err != nil {
						return nil, err
					}
					cwd := "/work"
					if target.Cwd != nil {
						cwd = *target.Cwd
					}
					if container != nil {
						return names.containers.env(ctx, container.Image, cwd)
					}
					if target.Cwd == nil {
						process, err := os.Getwd()
						if err != nil {
							return nil, err
						}
						return names.localEnv(process), nil
					}
					return names.localEnv(cwd), nil // cached NodeExecutionEnv per directory
				},
			})
			return err
		},

		// Section 2.2: settings read live through getters.
		"liveSettings": func(context.Context) error {
			// The user's settings, read live through getters: Settings runs at every resolution.
			settings := func() *harness.HarnessSettings {
				return &harness.HarnessSettings{
					Stream:     &durable.ConversationStreamOptions{TimeoutMs: new(names.manager.timeoutMs())},
					Compaction: &harness.CompactionPolicyPatch{Enabled: new(names.manager.autoCompact())},
				}
			}
			names.manager.setAutoCompact(false) // no Session write; every conversation follows at its next threshold check
			_ = settings()
			return nil
		},

		// Sections 2.2 and 7.1: host setup, tool filters, extension selection, plan mode, and reload.
		"host": func(ctx context.Context) error {
			registry := harness.CreateRegistry()
			for _, extension := range []*durable.Extension{tools.CodingTools, coding, contextFiles, skills, permissions, reviewer} {
				if err := registry.Install(extension); err != nil {
					return err
				}
			}
			opened, err := harness.OpenHarness(ctx, names.storage, harness.HarnessOptions{
				Models:   names.models,
				Registry: registry,
				// Reviewer is installed but not selected by default: only conversations that select it get its role and hooks.
				Settings: func() *harness.HarnessSettings {
					return &harness.HarnessSettings{Extensions: []*durable.Extension{tools.CodingTools, coding, contextFiles, skills, permissions}}
				},
				Env: func(_ context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
					if target.Cwd == nil {
						process, err := os.Getwd()
						if err != nil {
							return nil, err
						}
						return names.localEnv(process), nil
					}
					return names.localEnv(*target.Cwd), nil // cached NodeExecutionEnv per directory
				},
			})
			if err != nil {
				return err
			}
			// The conversation remembers its model and directory; a restart elsewhere keeps both.
			process, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := opened.Root(ctx, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(names.sonnet), Cwd: harness.SetTo(process)}})
			if err != nil {
				return err
			}

			for _, change := range []harness.AgentChange{
				{Tools: harness.SetTo(harness.ToolChange{Remove: []*durable.ToolRegistration{editTool}})},
				{Tools: harness.SetTo(harness.ToolChange{Remove: []*durable.ToolRegistration{bashTool}})}, // edit is offered again
				{Tools: harness.Cleared[harness.ToolChange]()},                                            // every tool of the selected extensions again
			} {
				if err := root.Configure(ctx, change); err != nil {
					return err
				}
			}

			for _, extension := range []*durable.Extension{timing, venv} { // venv: installed, but not in the default selection
				if err := registry.Install(extension); err != nil {
					return err
				}
			}
			conversation := root
			if err := conversation.Configure(ctx, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{Add: []*durable.Extension{venv}})}); err != nil {
				return err
			}

			if err := registry.Install(planMode); err != nil {
				return err
			}
			// PlanMode is in the default selection; /plan toggles this conversation's state.
			if _, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
				draft, err := durable.TxDoc[specPlanMode](tx, specPlanModeDoc, root.Id())
				if err != nil {
					return nil, err
				}
				return nil, draft.Set("enabled", true)
			}); err != nil {
				return err
			}

			for _, extension := range []*durable.Extension{subagent, chat, skillsV2} { // skillsV2: same name, so conversations selecting skills render v2 at their next request
				if err := registry.Install(extension); err != nil {
					return err
				}
			}
			registry.Uninstall(skills) // selecting conversations get a system delta removing its section; nothing is rewritten
			// Restart: the host installs its extensions again; stored names resolve against them.
			if names.host != nil {
				*names.host = specHost{harness: opened, root: root}
			}
			return nil
		},

		// Section 7.3: a named subagent's spawn commit.
		"spawn": func(ctx context.Context) error {
			_, err := names.session.Commit(ctx, func(tx durable.Tx) (any, error) {
				// In subagentTool's spawn commit, after the name checks:
				anchor, err := durable.CreateTask(tx, names.anchor, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, Background: true})
				if err != nil {
					return nil, err
				}
				// Owned by a task of the parent: starts as a copy of the parent's agent.
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: anchor}})
				if err != nil {
					return nil, err
				}
				return nil, harness.Configure(tx, child.Id, harness.AgentChange{
					Extensions:   harness.SetTo(harness.ExtensionChange{Remove: []*durable.Extension{subagentTools}}),
					Instructions: harness.SetTo(fmt.Sprintf("You are the subagent %q. Answer the main agent's requests.", names.name)),
				})
			})
			return err
		},

		// Section 4: table reads before the first table write; documents stay usable.
		"tableRules": func(ctx context.Context) error {
			_, err := names.session.Commit(ctx, func(tx durable.Tx) (any, error) {
				if _, err := tx.Conversation(names.conversationId); err != nil { // table read
					return nil, err
				}
				live, err := durable.TxDoc[harness.LiveState](tx, harness.LiveDoc, names.conversationId)
				if err != nil {
					return nil, err
				}
				if _, err := tx.AppendEntry(names.conversationId, names.message); err != nil { // first table write
					return nil, err
				}
				live.Delete("generation") // document mutation remains valid
				// further table writes are fine
				_, err = durable.CreateTask(tx, names.followTask, delta.NewJsonObject(0), durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &names.conversationId})
				return nil, err
			})
			return err
		},

		// Section 3.4: drafts are revoked after their commit.
		"revokedDraft": func(ctx context.Context) error {
			var escaped durable.Draft[harness.LiveState]
			if _, err := names.session.Commit(ctx, func(tx durable.Tx) (any, error) {
				var err error
				escaped, err = durable.TxDoc[harness.LiveState](tx, harness.LiveDoc, names.conversationId)
				return nil, err
			}); err != nil {
				return err
			}
			return escaped.Set("generation", nil) // panics with delta.ErrRevoked: the draft was revoked
		},
	}
	return sequences
}

// spec-usage.test.ts:375
func TestSpecUsageCompilesTheHarnessExamples(t *testing.T) {
	sequences := specUsageHarnessExamples(specNames{})
	want := []string{"childInToolCommit", "containerEnv", "liveSettings", "host", "spawn", "tableRules", "revokedDraft"}
	got := make([]string, 0, len(sequences))
	for name := range sequences {
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("sequences = %v, want %v", got, want)
	}
	for _, name := range want {
		if sequences[name] == nil {
			t.Fatalf("sequence %q is missing from %v", name, got)
		}
	}
}

// The host sequence of spec-usage.test.ts, run: it installs the examples' extensions, edits the root conversation's
// tool and extension selection, writes the plan-mode document, and replaces and uninstalls an extension.
// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of Conversation.Agent, Conversation.Id fails it
// mutation-checked: dropping the reads and writes of Extension.Name fails it
func TestSpecUsageHostSequenceRuns(t *testing.T) {
	names := specNames{
		host:           &specHost{},
		storage:        storage.NewMemoryStorage(),
		models:         ai.CreateModels(),
		sonnet:         durable.ModelRef{Provider: "test", ModelId: "sonnet"},
		localEnv:       func(string) env.ExecutionEnv { return nil },
		renderAgentsMd: func() *string { return nil },
		renderSkills:   func() *string { return new("skills") },
	}
	if err := specUsageHarnessExamples(names)["host"](background); err != nil {
		t.Fatal(err)
	}
	host := names.host
	defer func() { _ = host.harness.Close(background) }()
	agent, err := host.root.Agent(background)
	if err != nil {
		t.Fatal(err)
	}
	toolNames := []string{}
	for _, tool := range agent.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	if want := []string{"read", "write", "edit", "bash"}; !reflect.DeepEqual(toolNames, want) {
		t.Fatalf("tools = %v, want %v", toolNames, want)
	}
	// venv's bash replaces CodingTools' bash in place.
	if bash := agent.Tools[3]; bash.Name != "bash" || bash != venvOf(agent) {
		t.Fatalf("bash is not the venv's: %p", bash)
	}
	extensionNames := []string{}
	for _, extension := range agent.Extensions {
		extensionNames = append(extensionNames, extension.Name)
	}
	if want := []string{"coding-tools", "coding", "context-files", "permissions", "venv"}; !reflect.DeepEqual(extensionNames, want) {
		t.Fatalf("extensions = %v, want %v", extensionNames, want)
	}
	plan, err := durable.Snapshot[specPlanMode](background, host.harness, specPlanModeDoc, host.root.Id())
	if err != nil || plan == nil || !plan.Enabled {
		t.Fatalf("plan-mode document = %+v, %v", plan, err)
	}
}

// §3.4 over the real LiveDoc: a draft used after its commit panics with delta.ErrRevoked (upstream's TypeError).
func TestSpecUsageRevokedLiveDraftPanics(t *testing.T) {
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: ai.CreateModels(), Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close(background) }()
	root, err := opened.Root(background, nil)
	if err != nil {
		t.Fatal(err)
	}
	sequence := specUsageHarnessExamples(specNames{session: opened, conversationId: root.Id()})["revokedDraft"]
	defer func() {
		recovered, _ := recover().(error)
		if !errors.Is(recovered, delta.ErrRevoked) {
			t.Fatalf("recovered %v, want delta.ErrRevoked", recovered)
		}
	}()
	_ = sequence(background)
	t.Fatal("using the draft after its commit did not panic")
}

func venvOf(agent durable.Agent) *durable.ToolRegistration {
	for _, extension := range agent.Extensions {
		if extension.Name == "venv" {
			return extension.Tools[0]
		}
	}
	return nil
}
