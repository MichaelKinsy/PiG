package contract

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the tool choices of apps/p2/plugins/subagents/contract.worker.ts: the choices subagents and one-shot agents resolve
// alike from their caller's tools.

// AgentChoice is a subagent's model ('provider/modelId'), thinking level and tools, as the tools take them. Nil Tools:
// unset.
type AgentChoice struct {
	Model         *string
	ThinkingLevel *string
	Tools         []string
	ToolsSet      bool
}

// ToolChoice are the tools an agent gets, as ResolveTools resolves them.
type ToolChoice struct {
	// Chosen are the tools it chose (or got by default), by name: what its registry entry records and `configure` keeps.
	Chosen []string
	// Tools is every tool it gets: the chosen ones, the script-only ones with code mode, a subagent's `scope: "subagent"`
	// ones.
	Tools []*durable.ToolRegistration
	// Loaded are the deferred tools it is offered from the start: the `scope: "subagent"` ones, and deferred ones chosen by
	// name.
	Loaded []*durable.ToolRegistration
}

// Choice is a resolved choice: the model, the clamped thinking level and the tools.
type Choice struct {
	Model         durable.ModelRef
	ThinkingLevel ai.ModelThinkingLevel
	ToolChoice
}

// AgentKind is which agent gets the tools: a persistent subagent also gets the `scope: "subagent"` ones.
type AgentKind string

const (
	KindSubagent AgentKind = "subagent"
	KindOneShot  AgentKind = "one-shot"
)

// CodemodeTool is the code mode tool (plugins/codemode): choosing it brings the tools only scripts call.
const CodemodeTool = "codemode"

// CallerTools is a caller's resolved agent: the tools offered to its model, its deferred ones and the ones scripts call.
type CallerTools struct {
	Tools, Deferred, Callable []*durable.ToolRegistration
}

// CallerToolsOf is the tools of agent.
func CallerToolsOf(agent durable.Agent) CallerTools {
	return CallerTools{Tools: agent.Tools, Deferred: agent.Deferred, Callable: agent.Callable}
}

// CalledByModel is whether the model may call tool (`callers`); the others are script-only: MCP tools in code mode, `agent`…
func CalledByModel(tool *durable.ToolRegistration) bool { return tool.CallableBy(durable.CallerModel) }

func everyTool(caller CallerTools) []*durable.ToolRegistration {
	seen := map[string]bool{}
	var all []*durable.ToolRegistration
	for _, group := range [][]*durable.ToolRegistration{caller.Tools, caller.Deferred, caller.Callable} {
		for _, tool := range group {
			if !seen[tool.Name] {
				seen[tool.Name] = true
				all = append(all, tool)
			}
		}
	}
	return all
}

// scopeOf is where a tool works, as its plugin declared it.
func scopeOf(tool *durable.ToolRegistration, access map[string]p2runtime.P2Tool) string {
	if declared, ok := access[tool.Name]; ok && declared.Scope != "" {
		return declared.Scope
	}
	return "any"
}

// ChoosableTools are the caller's tools an agent may be given: the ones its model calls, except `scope: "main"` and
// `"subagent"` ones.
func ChoosableTools(caller CallerTools, access map[string]p2runtime.P2Tool) []*durable.ToolRegistration {
	var choosable []*durable.ToolRegistration
	for _, tool := range everyTool(caller) {
		if CalledByModel(tool) && scopeOf(tool, access) == "any" {
			choosable = append(choosable, tool)
		}
	}
	return choosable
}

// StillGiven is current's recorded tools that the caller can still give; nil for a new agent (all of them).
func StillGiven(caller CallerTools, access map[string]p2runtime.P2Tool, current *Subagent) []string {
	if current == nil {
		return nil
	}
	choosable := map[string]bool{}
	for _, tool := range ChoosableTools(caller, access) {
		choosable[tool.Name] = true
	}
	kept := []string{}
	for _, name := range current.Tools {
		if choosable[name] {
			kept = append(kept, name)
		}
	}
	return kept
}

// ResolveTools are the tools an agent of kind gets from a caller with tools caller: names among its choosable tools
// (nil: all), with code mode the caller's script-only tools (MCP tools, the terminal, `agent`, code mode's own), and for a
// subagent the `scope: "subagent"` ones. Deferred tools stay deferred, except those in load. A non-empty problem names the
// tools names may not choose.
func ResolveTools(caller CallerTools, access map[string]p2runtime.P2Tool, names []string, namesSet bool, load []string, kind AgentKind) (ToolChoice, string) {
	all := everyTool(caller)
	choosable := ChoosableTools(caller, access)
	byName := func(name string) *durable.ToolRegistration {
		for _, tool := range choosable {
			if tool.Name == name {
				return tool
			}
		}
		return nil
	}
	if namesSet {
		var unknown []string
		for _, name := range names {
			if byName(name) == nil {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			own := make([]string, len(choosable))
			for i, tool := range choosable {
				own[i] = tool.Name
			}
			return ToolChoice{}, fmt.Sprintf("Unknown tools: %s. Choose from your own tools: %s.", strings.Join(unknown, ", "), strings.Join(own, ", "))
		}
	}
	// In the order given, each once.
	chosen := choosable
	if namesSet {
		chosen = nil
		seen := map[string]bool{}
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				chosen = append(chosen, byName(name))
			}
		}
	}
	var scripts []*durable.ToolRegistration
	if slices.ContainsFunc(chosen, func(tool *durable.ToolRegistration) bool { return tool.Name == CodemodeTool }) {
		for _, tool := range all {
			if !CalledByModel(tool) && scopeOf(tool, access) != "main" {
				scripts = append(scripts, tool)
			}
		}
	}
	var own []*durable.ToolRegistration
	if kind == KindSubagent {
		for _, tool := range all {
			if scopeOf(tool, access) == "subagent" {
				own = append(own, tool)
			}
		}
	}
	choice := ToolChoice{Chosen: make([]string, len(chosen))}
	for i, tool := range chosen {
		choice.Chosen[i] = tool.Name
	}
	choice.Tools = slices.Concat(chosen, scripts, own)
	choice.Loaded = slices.Clone(own)
	for _, tool := range chosen {
		if tool.Deferred && slices.Contains(load, tool.Name) {
			choice.Loaded = append(choice.Loaded, tool)
		}
	}
	return choice, ""
}

// ResolveChoice validates a choice before any commit, so a typo changes nothing; a problem is returned as text. Unset
// fields keep current's (its recorded tools as far as the caller can still give them), or for a new agent the caller's
// model and thinking level and every tool the caller may give (ResolveTools).
func ResolveChoice(ctx context.Context, models durable.Models, api interface {
	Agent(ctx context.Context) (durable.Agent, error)
}, access map[string]p2runtime.P2Tool, choice AgentChoice, current *Subagent, kind AgentKind) (Choice, string, error) {
	own, err := api.Agent(ctx)
	if err != nil {
		return Choice{}, "", err
	}
	var text *string
	if choice.Model != nil {
		text = choice.Model
	} else if current != nil {
		text = &current.Model
	}
	unknownModel := "Unknown model " + func() string {
		if choice.Model != nil {
			return *choice.Model
		}
		return "(none)"
	}() + ". Use the subagent tool's models action to list them."
	var ref *durable.ModelRef
	switch {
	case text == nil:
		ref = own.Model
	default:
		if slash := strings.Index(*text, "/"); slash > 0 {
			ref = &durable.ModelRef{Provider: (*text)[:slash], ModelId: (*text)[slash+1:]}
		}
	}
	var model *ai.Model
	if ref != nil {
		model = models.GetModel(ref.Provider, ref.ModelId)
	}
	if ref == nil || model == nil {
		return Choice{}, unknownModel, nil
	}
	if choice.ThinkingLevel != nil && !slices.Contains(ThinkingLevels, *choice.ThinkingLevel) {
		return Choice{}, "Invalid thinkingLevel " + *choice.ThinkingLevel + ".", nil
	}
	requested := own.ThinkingLevel
	switch {
	case choice.ThinkingLevel != nil:
		requested = ai.ModelThinkingLevel(*choice.ThinkingLevel)
	case current != nil:
		requested = ai.ModelThinkingLevel(current.ThinkingLevel)
	}
	caller := CallerToolsOf(own)
	names, namesSet := choice.Tools, choice.ToolsSet
	if !namesSet {
		if kept := StillGiven(caller, access, current); kept != nil {
			names, namesSet = kept, true
		}
	}
	load := choice.Tools
	tools, problem := ResolveTools(caller, access, names, namesSet, load, kind)
	if problem != "" {
		return Choice{}, problem, nil
	}
	return Choice{Model: *ref, ThinkingLevel: ai.ClampThinkingLevel(model, requested), ToolChoice: tools}, "", nil
}
