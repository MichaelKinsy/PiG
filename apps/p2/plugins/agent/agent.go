// Package agent is p2's agent plugin (apps/p2/plugins/agent): code mode's one-shot `agent` tool, contributed
// unconditionally. Only scripts can call it (`callers: ["tools"]`), so without code mode nothing reaches it, and its prompt
// section renders only while a conversation can call both `codemode` and `agent`. Code mode stays independently removable:
// no import of its plugin. Its conversations are listed with the subagents (one-shot entries of their document), which is
// where their sidebar metadata and read-only rule live.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

// Ports apps/p2/plugins/agent/{worker,tool}.ts. One-shot subagents for code mode scripts: `await tools.agent({ prompt,
// schema })` runs a fresh conversation and resolves to its final answer, parsed as JSON and validated against `schema`.
// Nothing is reported to the main conversation: only the script's return value reaches it.

// Tool is the name of the tool.
const Tool = "agent"

const attempts = 3

// agentRun is the call's conversation, created once, so a rerun after a restart reattaches to it and its submissions.
type agentRun struct {
	ConversationId *durable.ConversationId `json:"conversationId,omitempty"`
}

var agentRunDoc = durable.DefineDoc(durable.DocDefinition[agentRun]{
	CommonDocDefinition: durable.CommonDocDefinition[agentRun]{
		Kind: "p2.agent-run", Version: 1,
		Initial:        func() agentRun { return agentRun{} },
		CheckpointWhen: func(agentRun, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeTask},
})

const description = "Run a one-shot subagent in your cwd and resolve to its final answer, parsed as JSON and validated against `schema` (a JSON Schema object). Rejects when the subagent fails, or when its answer still does not validate after 3 attempts. Its answer is not reported to you, only what the script returns. Run many at once with `Promise.all`."

var parameters = schema.Obj(schema.Properties{
	schema.Prop("prompt", schema.Str(schema.Description("The task."))),
	schema.Opt("label", schema.Str(schema.Description("Short name shown where the user inspects it; default: the prompt's start."))),
	schema.Prop("schema", described(p2runtime.Record(schema.Unknown()), "JSON Schema of the answer.")),
	schema.Opt("model", schema.Str(schema.Description("'provider/modelId'; default: the caller's."))),
	schema.Opt("thinkingLevel", schema.Str(schema.Description("off|minimal|low|medium|high|xhigh|max; default: the caller's."))),
	schema.Opt("tools", schema.Arr(schema.Str(), schema.Description("Names of tools of yours it gets (default: all you can give); not those that work only in your conversation."))),
}, schema.Open())

// described is schema with a description.
func described(value schema.Schema, text string) schema.Schema {
	value.Set("description", text)
	return value
}

var fence = regexp.MustCompile("^\\s*```(?:json)?\\s*\\n([\\s\\S]*?)\\n?```\\s*$")

func failure(text string) durable.ToolExecutionResult { return p2runtime.ToolText(text, nil, true) }

func textOf(message *ai.AssistantMessage) string {
	if message == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range message.Content {
		if part, ok := part.(ai.TextContent); ok {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

// newTool is the `agent` tool. access gives the worker's tools by name, with what they declare: which ones the agent may
// get.
func newTool(models func() durable.Models, access func() map[string]p2runtime.P2Tool) p2runtime.P2Tool {
	registration := harness.DefineTool(durable.ToolRegistration{
		ToolSchema:             p2runtime.MustToolSchema(Tool, description, parameters),
		StructuredOutputSchema: map[string]any{},
		// Only scripts call it: a model would block its turn on it.
		Callers: []durable.ToolCaller{durable.CallerTools},
		// A rerun reattaches to the call's conversation; submissions are deduplicated by request ID.
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, raw any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			args, _ := raw.(map[string]any)
			return run(ctx, models, access, args, api)
		},
	})
	return p2runtime.P2Tool{ToolRegistration: registration}
}

func run(ctx context.Context, models func() durable.Models, access func() map[string]p2runtime.P2Tool, args map[string]any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	validator, err := schema.Compile(args["schema"], schema.CompileOptions{Formats: formats.Formats()})
	if err != nil {
		return failure("Invalid schema: " + err.Error()), nil
	}
	// A rerun reattaches to the conversation it created, even if its model has since left the configuration.
	existing, err := durable.Snapshot(ctx, api, agentRunDoc, api.TaskId())
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	var choice *contract.Choice
	if existing == nil || existing.ConversationId == nil {
		request := contract.AgentChoice{}
		if model, ok := args["model"].(string); ok {
			request.Model = &model
		}
		if level, ok := args["thinkingLevel"].(string); ok {
			request.ThinkingLevel = &level
		}
		if list, ok := args["tools"].([]any); ok {
			request.ToolsSet = true
			for _, each := range list {
				name, _ := each.(string)
				request.Tools = append(request.Tools, name)
			}
		}
		resolved, problem, err := contract.ResolveChoice(ctx, models(), api, access(), request, nil, contract.KindOneShot)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if problem != "" {
			return failure(problem), nil
		}
		choice = &resolved
	}
	// Owned by this call: aborting the call aborts the agent's work. Listed in the same commit, by the call ID the script's
	// tool card shows it under, so the user can open its transcript. The main conversation's and its `/tree` branches'
	// one-shot agents are listed in the session's registry.
	top, err := p2runtime.IsTopLevel(ctx, api, api.ConversationId())
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	listedOn := api.ConversationId()
	if top {
		listedOn = durable.ROOT_CONVERSATION_ID
	}
	// The agent works where its caller does: a caller with its own location (a subagent's) passes it on.
	inherited, err := contract.OwnLocation(ctx, api, api.ConversationId())
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	prompt, _ := args["prompt"].(string)
	label := prompt
	if each, ok := args["label"].(string); ok {
		label = each
	}
	committed, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		run, err := durable.TxDoc[agentRun](tx, agentRunDoc, api.TaskId())
		if err != nil {
			return nil, err
		}
		if id, ok := run.Get("conversationId").(float64); ok {
			return durable.ConversationId(id), nil
		}
		if choice == nil {
			return nil, fmt.Errorf("The agent's conversation record disappeared")
		}
		child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(args["schema"])
		if err != nil {
			return nil, err
		}
		tools := harness.SetTo(harness.ToolChange{List: nonNil(choice.Tools)})
		change := harness.AgentChange{
			Model: harness.SetTo(choice.Model), ThinkingLevel: harness.SetTo(choice.ThinkingLevel),
			Tools: tools, ModelTools: harness.SetTo(harness.ToolChange{Edit: &harness.ToolEdit{Add: nonNil(choice.Loaded)}}),
			Instructions: harness.SetTo("You are a one-shot subagent working for a script. Do the requested work. Your final answer is parsed by the script: it must be only a JSON value matching this JSON Schema, without prose or code fences:\n" + string(encoded)),
		}
		if err := harness.Configure(tx, child.Id, change); err != nil {
			return nil, err
		}
		if inherited != nil {
			state, err := durable.TxDoc[contract.InheritedLocationState](tx, contract.InheritedLocationDoc, child.Id)
			if err != nil {
				return nil, err
			}
			var env any
			if inherited.Env != nil {
				env = *inherited.Env
			}
			if err := state.Set("location", delta.JsonObjectOf("cwd", inherited.Cwd, "env", env)); err != nil {
				return nil, err
			}
			if err := harness.Configure(tx, child.Id, harness.AgentChange{Cwd: harness.SetTo(inherited.Cwd)}); err != nil {
				return nil, err
			}
		}
		if err := run.Set("conversationId", float64(child.Id)); err != nil {
			return nil, err
		}
		registry, err := durable.TxDoc[contract.SubagentState](tx, contract.SubagentsDoc, listedOn)
		if err != nil {
			return nil, err
		}
		if !registry.Has("oneShots") {
			if err := registry.Set("oneShots", delta.NewJsonObject(0)); err != nil {
				return nil, err
			}
		}
		chosen := make([]any, len(choice.Chosen))
		for i, name := range choice.Chosen {
			chosen[i] = name
		}
		if err := registry.Object("oneShots").Set(api.CallId(), delta.JsonObjectOf(
			"conversationId", float64(child.Id), "label", oneLine(label), "model", choice.Model.Provider+"/"+choice.Model.ModelId,
			"thinkingLevel", string(choice.ThinkingLevel), "tools", chosen)); err != nil {
			return nil, err
		}
		return child.Id, nil
	})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	conversationID := committed.(durable.ConversationId)
	child, err := api.Conversation(ctx, conversationID)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if child == nil {
		return failure("The agent's conversation is gone."), nil
	}
	// The agent's turn may act for the owner if the script's may.
	origin, err := p2runtime.OriginOf(ctx, api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	data := delta.NewJsonObject(1)
	if origin != "" {
		data.Set("origin", string(origin))
	}
	message, problem := prompt, ""
	for attempt := 1; attempt <= attempts; attempt++ {
		submission, err := child.Submit(ctx, durable.InputSubmissionDraft{
			Type: durable.SubmissionTypeInput, Content: ai.UserText(message),
			InputEntry: &durable.InputEntry{Kind: p2runtime.SubagentTaskEntry, Data: data},
			WhenBusy:   durable.WhenBusyFollowUp, RequestId: new(fmt.Sprintf("agent:%d", attempt)),
		})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		settled, err := submission.Wait(ctx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if settled.Type != durable.SubmissionTypeInput || settled.Status != durable.SubmissionDone || settled.Answer == nil {
			reason := string(settled.Status)
			if settled.Reason != nil {
				reason = *settled.Reason
			}
			return failure("The agent did not answer: " + reason), nil
		}
		answered, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
			entry, err := tx.Entry(*settled.Answer)
			if err != nil || entry == nil || len(entry.Model) == 0 {
				return nil, err
			}
			if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
				return message, nil
			}
			return nil, nil
		})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		var answer *ai.AssistantMessage
		if each, ok := answered.(ai.AssistantMessage); ok {
			answer = &each
		}
		text := textOf(answer)
		body := text
		if match := fence.FindStringSubmatch(text); match != nil {
			body = match[1]
		}
		value, err := parseJSON(body)
		if err != nil {
			problem = "it is not JSON (" + err.Error() + ")"
			message = "Your answer was not usable: " + problem + ". Reply with only the JSON value."
			continue
		}
		if ok, _ := validator.Check(value); ok {
			encoded, err := encodeJSON(value)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			result := p2runtime.ToolText(encoded, delta.JsonObjectOf("conversationId", float64(conversationID), "attempts", float64(attempt)), false)
			result.StructuredOutput, result.HasStructuredOutput = value, true
			return result, nil
		}
		errs, _ := validator.Errors(value)
		var shown []string
		for _, each := range errs[:min(len(errs), 5)] {
			path := each.Path
			if path == "" {
				path = "/"
			}
			shown = append(shown, path+": "+each.Message)
		}
		problem = "it does not match the schema (" + strings.Join(shown, "; ") + ")"
		message = "Your answer was not usable: " + problem + ". Reply with only the corrected JSON value."
	}
	return failure(fmt.Sprintf("The agent's answer was not usable after %d attempts: %s.", attempts, problem)), nil
}

func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

var spaces = regexp.MustCompile(`\s+`)

// oneLine is a label on one line, at most 80 UTF-16 units.
func oneLine(text string) string {
	collapsed := strings.TrimSpace(spaces.ReplaceAllString(text, " "))
	units := 0
	for index, each := range collapsed {
		width := 1
		if each > 0xFFFF {
			width = 2
		}
		if units+width > 80 {
			return collapsed[:index]
		}
		units += width
	}
	return collapsed
}

func init() {
	workerFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return p2runtime.Contribute(facet, "p2.agent-tool", func(self host.StoreSelf) error {
			config := self.Registry(p2runtime.HarnessConfig.Registry)
			extensionsSource := self.Registry(p2runtime.Extensions.Registry)
			// The live model observer, as the subagent tool uses it: a phase captures the tool, not the model catalog.
			models := func() durable.Models {
				table, _ := config.Value().(*p2runtime.KeyedTable[p2runtime.HarnessConfigEntry])
				if table != nil {
					if entry, ok := table.Get("default"); ok && entry.Models != nil {
						return entry.Models()
					}
				}
				panic("No model configuration")
			}
			access := func() map[string]p2runtime.P2Tool {
				table, _ := p2runtime.Extensions.Table(extensionsSource)
				return p2runtime.ToolsByName(table)
			}
			extension := &p2runtime.P2Extension{Extension: &durable.Extension{Name: "p2-agent"}, Tools: []p2runtime.P2Tool{newTool(models, access)}}
			p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extension.Extension.Name, extension))
			return nil
		})
	})
	hosts.RegisterPlugin(hosts.Plugin{Name: "agent", Worker: &workerFacet})
}

func parseJSON(text string) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(text))
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("unexpected data after the JSON value")
	}
	return value, nil
}

func encodeJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

// workerFacet is the plugin's worker facet, set at init.
var workerFacet host.Facet

// Facet is the plugin's worker facet.
func Facet() host.Facet { return workerFacet }
