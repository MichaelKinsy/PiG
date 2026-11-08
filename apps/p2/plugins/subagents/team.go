package subagents

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/subagents/tools.ts (teamTool): subagents' messaging, to the main agent and to the other subagents
// of the same main agent (siblings), as structural `p2.agent-message` inputs that start a turn like any input. Within one
// session, so no cross-session rule applies. No automatic replies: loops are left to the prompt.

const teamExtensionName = "p2-subagent-team"

var teamDescription = strings.Join([]string{
	"Message the main agent or the other subagents while you work, without ending your turn.",
	"- send (to: \"parent\" or a subagent name, message; optional intent \"question\" (default) or \"note\", steer):",
	"  delivers it as a message from you; it starts the receiver's turn if it is idle, else waits for its next step",
	"  (steer: true interrupts its current step). A question asks for a reply, which arrives as a message to you; a note needs none.",
	"- answer (name): the latest completed answer of another subagent.",
	"- list: the other subagents and whether they are working.",
	"Only message when it helps the work. Never send acknowledgements or thanks, and do not reply to notes.",
}, "\n")

var teamParameters = schema.Obj(schema.Properties{
	schema.Prop("action", literals("send", "answer", "list")),
	schema.Opt("to", schema.Str(schema.MinLength(1), schema.MaxLength(200))),
	schema.Opt("name", schema.Str(schema.MinLength(1), schema.MaxLength(200))),
	schema.Opt("message", schema.Str(schema.MinLength(1), schema.MaxLength(32768))),
	schema.Opt("intent", literals("question", "note")),
	schema.Opt("steer", schema.Bool()),
}, schema.Open())

// teamTool is the `team` tool.
func teamTool() p2runtime.P2Tool {
	registration := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: p2runtime.ToolSchemaOf(contract.TeamTool, teamDescription, teamParameters),
		// A send is admitted once per call (its request ID); list and answer only read.
		Replay:   durable.ReplaySafe,
		Deferred: true,
		Execute: func(ctx context.Context, raw any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			args, _ := raw.(map[string]any)
			return runTeam(ctx, args, api)
		},
	})
	return p2runtime.P2Tool{ToolRegistration: registration, ToolAccess: p2runtime.ToolAccess{Scope: "subagent"}}
}

func runTeam(ctx context.Context, args map[string]any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	action, _ := stringArg(args, "action")
	to, hasTo := stringArg(args, "to")
	reply := func(text string, isError bool) (durable.ToolExecutionResult, error) {
		details := subagentDetailsTeam(action, hasTo, to)
		return p2runtime.ToolText(text, details, isError), nil
	}
	registry, err := registryOf(ctx, api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	self, found := "", false
	for name, agent := range registry.Agents {
		if agent.ConversationId == api.ConversationId() {
			self, found = name, true
		}
	}
	if !found {
		return reply("Only subagents can use the team tool.", true)
	}
	siblings := orderedSiblings(registry, self)
	switch action {
	case "list":
		lines := []string{}
		for _, name := range siblings {
			agent := registry.Agents[name]
			working, err := busy(ctx, api, agent.ConversationId)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			state := "idle"
			if working {
				state = "working"
			}
			archived := ""
			if agent.Archived {
				archived = " · archived"
			}
			lines = append(lines, name+": "+state+archived)
		}
		if len(lines) == 0 {
			return reply("No other subagents.", false)
		}
		return reply(strings.Join(lines, "\n"), false)
	case "answer":
		name, _ := stringArg(args, "name")
		sibling, ok := registry.Agents[name]
		if !ok || name == self {
			shown := name
			if shown == "" {
				shown = "(none)"
			}
			return reply("No other subagent named "+shown+".", true)
		}
		answer, has, err := latestAnswer(ctx, api, sibling.ConversationId)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if !has {
			return reply(name+" has not answered yet.", false)
		}
		return reply(answer, false)
	}
	if !hasTo || to == "" {
		return reply("send needs to (\"parent\" or a subagent name).", true)
	}
	message, _ := stringArg(args, "message")
	if strings.TrimSpace(message) == "" {
		return reply("send needs a message.", true)
	}
	var sibling *contract.Subagent
	if to != "parent" {
		if agent, ok := registry.Agents[to]; ok && to != self {
			sibling = &agent
		}
	}
	if to != "parent" && sibling == nil {
		return reply("No other subagent named "+to+".", true)
	}
	var targetID durable.ConversationId
	switch {
	case sibling != nil:
		targetID = sibling.ConversationId
	case registry.Agents[self].Parent != 0:
		targetID = registry.Agents[self].Parent
	default:
		if targetID, err = p2runtime.MainConversation(ctx, api); err != nil {
			return durable.ToolExecutionResult{}, err
		}
	}
	target, err := api.Conversation(ctx, targetID)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if target == nil {
		return reply(to+" has no conversation.", true)
	}
	// The receiver's turn may act for the owner if this one may.
	origin, err := p2runtime.OriginOf(ctx, api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	intent := p2runtime.IntentQuestion
	if value, _ := stringArg(args, "intent"); value != "" {
		intent = p2runtime.AgentIntent(value)
	}
	agentMessage := p2runtime.AgentMessage{FromName: self, FromConversationID: float64(api.ConversationId()), Intent: intent, Text: message}
	if origin != "" {
		agentMessage.Origin = &origin
	}
	replyWith := "subagent send to " + self
	if sibling != nil {
		replyWith = "team send to " + self
	}
	content, entry := agentMessageInput(agentMessage, replyWith)
	whenBusy := durable.WhenBusyFollowUp
	if steer, _ := args["steer"].(bool); steer {
		whenBusy = durable.WhenBusySteer
	}
	if _, err := target.Submit(ctx, durable.InputSubmissionDraft{
		Type: durable.SubmissionTypeInput, Content: content, InputEntry: entry, WhenBusy: whenBusy,
		RequestId: new(fmt.Sprintf("team:%d:%s", api.TaskId(), api.CallId())),
	}); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return reply("Sent to "+to+".", false)
}

// orderedSiblings are the other subagents' names in the registry's order.
func orderedSiblings(registry contract.SubagentState, self string) []string {
	var siblings []string
	for _, name := range registry.Names() {
		if name != self {
			siblings = append(siblings, name)
		}
	}
	return siblings
}

func subagentDetailsTeam(action string, hasTo bool, to string) *delta.JsonObject {
	details := delta.JsonObjectOf("action", action)
	if hasTo {
		details.Set("to", to)
	}
	return details
}
