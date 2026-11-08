package subagents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/subagents/tools.ts (the `subagent` and `team` tools).

var subagentDescription = strings.Join([]string{
	"Manage persistent subagents that work in the background while you keep talking to the user.",
	"Each subagent is its own conversation with its own memory, working in your cwd unless spawned with its own.",
	"Actions:",
	"- spawn (name, message; optional model, thinkingLevel, tools, env, cwd): start a subagent and send its first message.",
	"  Without env and cwd it works where you work and follows the session when it moves. With them it gets its own",
	"  location, which stays when the session moves: cwd is an existing directory its tools run in and the user's workspace",
	"  view shows, e.g. a git worktree you created first (relative to your cwd on your environment, to the home on another);",
	"  env is an environment name from location_envs, or null for this device (default: yours; another environment needs",
	"  the owner's request).",
	"  model is 'provider/modelId' (default: yours); thinkingLevel is off|minimal|low|medium|high|xhigh|max",
	"  (clamped to the model); tools names tools of yours it gets (default: all you can give; [] for none). Tools that",
	"  work only in your conversation (subagent, rename_session, …) cannot be given. With codemode its scripts call",
	"  its tools and the tools behind code mode (MCP tools, terminal, one-shot agents). With location_move it can",
	"  move itself.",
	"- send (name, message; followUp: true queues after the current answer instead of steering).",
	"- configure (name; optional model, thinkingLevel, tools, env, cwd, followSession): replace its model/thinking/tool",
	"  selection for later work; env/cwd give it its own location, followSession: true makes it follow the session",
	"  again. A location changes only while it is idle.",
	"- stop (name): abort its current work; it stays usable.",
	"- archive (name): stop it and hide it from the user's sidebar and from status; restore (name) or send brings it back.",
	"- answer (name): return its latest completed answer, e.g. when a report did not reach you or you need it again.",
	"- status (optional name): list subagents and whether they are working; archived ones only by name.",
	"- models: list models that have credentials configured.",
	"Answers are reported back to you as messages when they arrive; do not wait or poll for them.",
	"Subagents can also message you while they work: a <sender kind=\"subagent\"> message marked [asks; …] needs your",
	"answer (send to that subagent); a note needs none.",
}, "\n")

func literals(values ...string) schema.Schema {
	options := make([]schema.Schema, len(values))
	for i, value := range values {
		options[i] = p2runtime.Literal(value)
	}
	return schema.Union(options)
}

var subagentParameters = schema.Obj(schema.Properties{
	schema.Prop("action", literals("spawn", "send", "configure", "stop", "archive", "restore", "answer", "status", "models")),
	schema.Opt("name", schema.Str()),
	schema.Opt("message", schema.Str()),
	schema.Opt("followUp", schema.Bool()),
	schema.Opt("model", schema.Str()),
	schema.Opt("thinkingLevel", schema.Str()),
	schema.Opt("tools", schema.Arr(schema.Str())),
	schema.Opt("cwd", schema.Str()),
	schema.Opt("env", schema.Union([]schema.Schema{schema.Str(), schema.Nul()})),
	schema.Opt("followSession", schema.Bool()),
}, schema.Open())

// subagentEnvironment is what the subagent tool needs from the worker.
type subagentEnvironment struct {
	models func() durable.Models
	locate locator
	// access is the worker's tools by name, with what they declare: which ones an agent may give to its subagents.
	access func() map[string]p2runtime.P2Tool
}

func stringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key].(string)
	return value, ok
}

func stringList(args map[string]any, key string) ([]string, bool) {
	list, ok := args[key].([]any)
	if !ok {
		return nil, false
	}
	names := make([]string, len(list))
	for i, each := range list {
		names[i], _ = each.(string)
	}
	return names, true
}

// subagentDetails are the tool's details: the action, the subagent, and its choices and location.
func subagentDetails(action string, name *string, extra ...any) delta.JsonValue {
	details := delta.JsonObjectOf("action", action)
	if name != nil {
		details.Set("name", *name)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		details.Set(extra[i].(string), extra[i+1])
	}
	return details
}

// locationDetails is a subagent's own location as the tool's details carry it (its cwd and, on a remote environment, that
// environment's name).
func locationDetails(location *contract.LocationValue) []any {
	if location == nil {
		return nil
	}
	details := []any{"cwd", location.Cwd}
	if location.Env != nil {
		details = append(details, "env", *location.Env)
	}
	return details
}

func busy(ctx context.Context, api durable.ToolExecutionApi, conversation durable.ConversationId) (bool, error) {
	live, err := durable.Snapshot(ctx, api, harness.LiveDoc, conversation)
	if err != nil {
		return false, err
	}
	return live != nil && live.Run != nil, nil
}

func stringsAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func newSubagentTool(environment subagentEnvironment) p2runtime.P2Tool {
	registration := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: p2runtime.MustToolSchema("subagent", subagentDescription, subagentParameters),
		// A crash-interrupted stop must not be repeated against newer work.
		Replay: durable.ReplayUnsafe,
		Execute: func(ctx context.Context, raw any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			args, _ := raw.(map[string]any)
			return runSubagent(ctx, environment, args, api)
		},
	})
	return p2runtime.P2Tool{ToolRegistration: registration, ToolAccess: p2runtime.ToolAccess{Scope: "main"}}
}

func runSubagent(ctx context.Context, environment subagentEnvironment, args map[string]any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	action, _ := stringArg(args, "action")
	name, hasName := stringArg(args, "name")
	message, hasMessage := stringArg(args, "message")
	followUp, _ := args["followUp"].(bool)
	var namePtr *string
	if hasName {
		namePtr = &name
	}
	reply := func(text string, isError bool, extra ...any) (durable.ToolExecutionResult, error) {
		return p2runtime.ToolText(text, subagentDetails(action, namePtr, extra...), isError), nil
	}
	registry, err := contract.Registry(ctx, api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}

	if action == "models" {
		available, ok := environment.models().(interface {
			GetAvailable(ctx context.Context, providerID ...string) ([]*ai.Model, error)
		})
		if !ok {
			return reply("No models with configured credentials.", false)
		}
		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		models, err := available.GetAvailable(listCtx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		lines := make([]string, 0, min(len(models), 200))
		for _, model := range models[:min(len(models), 200)] {
			lines = append(lines, fmt.Sprintf("%s/%s (%s)", model.ProviderID(), model.ID, model.DisplayName))
		}
		if len(lines) == 0 {
			return reply("No models with configured credentials.", false)
		}
		return reply(strings.Join(lines, "\n"), false)
	}
	if action == "status" {
		var listed []string
		for _, each := range registry.Names() {
			if hasName && each == name || !hasName && !registry.Agents[each].Archived {
				listed = append(listed, each)
			}
		}
		lines := []string{}
		for _, each := range listed {
			agent := registry.Agents[each]
			working, err := busy(ctx, api, agent.ConversationId)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			state := "idle"
			if working {
				state = "working"
			}
			archived, tools, where := "", "none", ""
			if agent.Archived {
				archived = " · archived"
			}
			if len(agent.Tools) > 0 {
				tools = strings.Join(agent.Tools, ", ")
			}
			if agent.Location != nil {
				where = " · in " + p2runtime.LocationText(*contract.LocationOf(agent.Location))
			}
			lines = append(lines, fmt.Sprintf("%s: %s%s · %s · thinking %s · tools %s%s", each, state, archived, agent.Model, agent.ThinkingLevel, tools, where))
		}
		if !hasName {
			if hidden := len(registry.Agents) - len(listed); hidden > 0 {
				lines = append(lines, fmt.Sprintf("%d archived (status with a name shows one).", hidden))
			}
		}
		if len(lines) == 0 {
			return reply("No subagents.", false)
		}
		return reply(strings.Join(lines, "\n"), false)
	}
	if !hasName || strings.TrimSpace(name) == "" {
		return reply(action+" needs a name.", true)
	}
	agent, exists := registry.Agents[name]
	var agentPtr *contract.Subagent
	if exists {
		agentPtr = &agent
	}
	if action != "spawn" && !exists {
		return reply("No subagent named "+name+".", true)
	}
	conversationID := float64(agent.ConversationId)
	switch {
	case action == "stop" && exists:
		handle, err := api.Conversation(ctx, agent.ConversationId)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if handle != nil {
			if err := handle.Abort(ctx, nil); err != nil {
				return durable.ToolExecutionResult{}, err
			}
		}
		return reply("Stopped "+name+".", false, "conversationId", conversationID)
	case (action == "archive" || action == "restore") && exists:
		if err := setArchived(ctx, archiverOfAPI(api), durable.ROOT_CONVERSATION_ID, agent.ConversationId, action == "archive"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if action == "archive" {
			return reply("Stopped and archived "+name+".", false, "conversationId", conversationID)
		}
		return reply("Restored "+name+".", false, "conversationId", conversationID)
	case action == "answer" && exists:
		answer, found, err := latestAnswer(ctx, api, agent.ConversationId)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		working, err := busy(ctx, api, agent.ConversationId)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		state := ""
		if working {
			state = name + " is still working."
		}
		var text string
		switch {
		case !found && state != "":
			text = name + " has not answered yet. " + state
		case !found:
			text = name + " has not answered yet."
		case state != "":
			text = state + " Its latest answer:\n\n" + answer
		default:
			text = answer
		}
		return reply(text, false, "conversationId", conversationID)
	}
	if action != "configure" && (!hasMessage || strings.TrimSpace(message) == "") {
		return reply(action+" needs a message.", true)
	}
	_, hasCwd := stringArg(args, "cwd")
	_, hasEnv := args["env"]
	placing := hasCwd || hasEnv
	followSession, _ := args["followSession"].(bool)
	_, hasModel := args["model"]
	_, hasThinking := args["thinkingLevel"]
	_, hasTools := args["tools"]
	if action == "configure" && !hasModel && !hasThinking && !hasTools && !placing && !followSession {
		return reply("configure needs model, thinkingLevel, tools, env, cwd or followSession.", true)
	}

	// Validate spawn/configure choices before a commit, so a typo changes nothing.
	var change contract.Choice
	var tools contract.ToolChoice
	// location is its own location; followSessionAgain: it follows the session (again).
	var location *p2runtime.Location
	followAgain := false
	// narrowed is, on a send, the tools it keeps when its caller can no longer give all it was given.
	var narrowed *contract.ToolChoice
	if placing && action != "spawn" && action != "configure" {
		return reply("Only spawn and configure take a location.", true)
	}
	if followSession && (action != "configure" || placing) {
		return reply("followSession is for configure, without env or cwd.", true)
	}
	// Who started this turn: the owner may choose another environment, and the subagent's turn inherits it.
	var origin p2runtime.ConversationOrigin
	if action == "spawn" || action == "send" || placing {
		if origin, err = p2runtime.OriginOf(ctx, api); err != nil {
			return durable.ToolExecutionResult{}, err
		}
	}
	access := environment.access()
	if action == "spawn" || action == "configure" {
		if action == "spawn" && exists {
			archived := ""
			if agent.Archived {
				archived = " (archived)"
			}
			return reply(name+" already exists"+archived+"; use send.", true)
		}
		existing := agentPtr
		if action == "spawn" {
			existing = nil
		}
		choice := contract.AgentChoice{}
		if model, ok := stringArg(args, "model"); ok {
			choice.Model = &model
		}
		if level, ok := stringArg(args, "thinkingLevel"); ok {
			choice.ThinkingLevel = &level
		}
		choice.Tools, choice.ToolsSet = stringList(args, "tools")
		resolved, problem, err := contract.ResolveChoice(ctx, environment.models(), api, access, choice, existing, contract.KindSubagent)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if problem != "" {
			return reply(problem, true)
		}
		change, tools = contract.Choice{Model: resolved.Model, ThinkingLevel: resolved.ThinkingLevel}, resolved.ToolChoice
		// A location changes between runs, never under one: its tools would switch machines mid-task.
		if exists && (placing || followSession) {
			working, err := busy(ctx, api, agent.ConversationId)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if working {
				return reply(name+" is working: stop it or wait before changing its location.", true)
			}
		}
		if followSession {
			followAgain = true
		} else if placing {
			// From the caller's location (the session's: subagents spawn none).
			caller, err := environment.locate.locate(ctx, api.ConversationId(), api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			target := placement{envSet: hasEnv}
			if hasEnv {
				if env, ok := args["env"].(string); ok {
					target.env = &env
				}
			}
			if cwd, ok := stringArg(args, "cwd"); ok {
				target.cwd = &cwd
			}
			if hasEnv && !sameEnvironment(target.env, caller.Env) && origin != p2runtime.OriginOwner {
				return reply("Another environment needs a turn the owner started.", true)
			}
			// A subagent in a sandbox: a cwd alone means a path in that sandbox (it cannot be placed elsewhere).
			if exists && agent.Location != nil && !hasEnv {
				kept, err := environment.locate.sandbox(ctx, *contract.LocationOf(agent.Location))
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				if kept != "" {
					target.envSet, target.env = true, &kept
				}
			}
			placed, problem, err := placeTarget(ctx, environment.locate, caller, target)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if problem != "" {
				return reply(problem, true)
			}
			location = &placed
		}
		// A subagent working in a sandbox stays there, whoever configures it (only the owner's Relocate leaves one).
		if exists && agent.Location != nil && (location != nil || followAgain) {
			sandbox, err := environment.locate.sandbox(ctx, *contract.LocationOf(agent.Location))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if sandbox != "" {
				next := location
				if next == nil {
					session, err := environment.locate.locate(ctx, durable.ROOT_CONVERSATION_ID, api)
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					next = &session
				}
				if next.Env == nil || *next.Env != sandbox {
					return reply(name+" works in sandbox "+sandbox+" and cannot leave it.", true)
				}
			}
		}
	} else if exists {
		// A send: what it was given that its caller can no longer give is taken away (the caller's tools changed).
		own, err := api.Agent(ctx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		caller := contract.CallerToolsOf(own)
		if kept := contract.StillGiven(caller, access, &agent); kept != nil && len(kept) != len(agent.Tools) {
			if resolved, problem := contract.ResolveTools(caller, access, kept, true, nil, contract.KindSubagent); problem == "" {
				narrowed = &resolved
			}
		}
	}
	// A subagent following the session works at its cwd.
	var session *p2runtime.Location
	if followAgain {
		located, err := environment.locate.locate(ctx, durable.ROOT_CONVERSATION_ID, api)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		session = &located
	}

	committed, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		state, err := durable.TxDoc[contract.SubagentState](tx, contract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			return nil, err
		}
		background := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, Background: true}
		modelText := change.Model.Provider + "/" + change.Model.ModelId
		agents := state.Object("agents")
		if action == "configure" {
			if !agents.Has(name) {
				return "No subagent named " + name + ".", nil
			}
			target := agents.Object(name)
			targetID, _ := target.Get("conversationId").(float64)
			cwd := harness.Change[string]{}
			switch {
			case location != nil:
				cwd = harness.SetTo(location.Cwd)
			case session != nil:
				cwd = harness.SetTo(session.Cwd)
			}
			toolList, modelTools := agentTools(tools)
			if err := harness.Configure(tx, durable.ConversationId(targetID), harness.AgentChange{
				Model: harness.SetTo(change.Model), ThinkingLevel: harness.SetTo(change.ThinkingLevel),
				Tools: toolList, ModelTools: modelTools, Cwd: cwd,
			}); err != nil {
				return nil, err
			}
			if location != nil {
				if err := target.Set("location", locationJSON(*location)); err != nil {
					return nil, err
				}
			} else if followAgain {
				target.Delete("location")
			}
			if err := target.Set("model", modelText); err != nil {
				return nil, err
			}
			if err := target.Set("thinkingLevel", string(change.ThinkingLevel)); err != nil {
				return nil, err
			}
			if err := target.Set("tools", stringsAny(tools.Chosen)); err != nil {
				return nil, err
			}
			return "Configured " + name + ".", nil
		}
		if action == "spawn" {
			if agents.Has(name) {
				return name + " already exists; use send.", nil
			}
			anchorID, err := durable.CreateTask(tx, anchor, nil, background)
			if err != nil {
				return nil, err
			}
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: anchorID}})
			if err != nil {
				return nil, err
			}
			cwd := harness.Change[string]{}
			if location != nil {
				cwd = harness.SetTo(location.Cwd)
			}
			toolList, modelTools := agentTools(tools)
			if err := harness.Configure(tx, child.Id, harness.AgentChange{
				Model: harness.SetTo(change.Model), ThinkingLevel: harness.SetTo(change.ThinkingLevel),
				Tools: toolList, ModelTools: modelTools, Cwd: cwd,
				Instructions: harness.SetTo(fmt.Sprintf("You are the subagent %q, working for a main agent. Do the requested work and answer concisely; your final answer is reported back to it. With the team tool you can ask the main agent a question or tell it something while you keep working, and message the other subagents.", name)),
			}); err != nil {
				return nil, err
			}
			entry := delta.JsonObjectOf(
				"conversationId", float64(child.Id), "model", modelText, "thinkingLevel", string(change.ThinkingLevel),
				"tools", stringsAny(tools.Chosen),
			)
			if location != nil {
				entry.Set("location", locationJSON(*location))
			}
			entry.Set("reported", []any{})
			if err := agents.Set(name, entry); err != nil {
				return nil, err
			}
		}
		if !agents.Has(name) {
			return "No subagent named " + name + ".", nil
		}
		target := agents.Object(name)
		// Working with an archived subagent again brings it back.
		target.Delete("archived")
		if err := target.Set("parent", float64(api.ConversationId())); err != nil {
			return nil, err
		}
		targetID, _ := target.Get("conversationId").(float64)
		if narrowed != nil {
			toolList, modelTools := agentTools(*narrowed)
			if err := harness.Configure(tx, durable.ConversationId(targetID), harness.AgentChange{Tools: toolList, ModelTools: modelTools}); err != nil {
				return nil, err
			}
			if err := target.Set("tools", stringsAny(narrowed.Chosen)); err != nil {
				return nil, err
			}
		}
		input := reporterInput{
			Name: name, ConversationId: durable.ConversationId(targetID), Parent: api.ConversationId(),
			Message: message, FollowUp: action == "send" && followUp,
		}
		if origin == p2runtime.OriginOwner {
			input.Origin = string(origin)
		}
		reporterID, err := durable.CreateTask(tx, reporter, input, background)
		if err != nil {
			return nil, err
		}
		if err := state.Object("reporters").Set(fmt.Sprint(int64(api.TaskId())), float64(reporterID)); err != nil {
			return nil, err
		}
		if action == "send" {
			return "Sent to " + name + ".", nil
		}
		return "Started " + name + ".", nil
	})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	current, err := contract.Registry(ctx, api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	var extra []any
	if now, ok := current.Agents[name]; ok {
		extra = append([]any{
			"conversationId", float64(now.ConversationId), "model", now.Model, "thinkingLevel", now.ThinkingLevel,
			"tools", stringsAny(now.Tools),
		}, locationDetails(now.Location)...)
	}
	return reply(committed.(string), false, extra...)
}
