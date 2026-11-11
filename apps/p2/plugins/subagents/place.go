package subagents

import (
	"context"
	"maps"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/subagents/tools.ts (placement and archiving).

func locationValue(location p2runtime.Location) *contract.LocationValue {
	return &contract.LocationValue{Cwd: location.Cwd, Env: location.Env}
}

// locationJSON is a location as a document stores it.
func locationJSON(location p2runtime.Location) *delta.JsonObject {
	var env any
	if location.Env != nil {
		env = *location.Env
	}
	return delta.JsonObjectOf("cwd", location.Cwd, "env", env)
}

// placeSubagents places what version 1 left unplaced (an own cwd without its environment: spawnedOn tells the environment
// it was spawned on), then lists where the session's subagents work: the conversations following the session (subagents
// spawned without their own location, one-shot agents) and the own locations.
func placeSubagents(ctx context.Context, session harness.Harness, spawnedOn func(conversation durable.ConversationId, cwd string) *string) (followers []durable.ConversationId, own []p2runtime.Location, err error) {
	registry, err := contract.Registry(ctx, session)
	if err != nil {
		return nil, nil, err
	}
	if slices.ContainsFunc(slices.Collect(maps.Values(registry.Agents)), func(agent contract.Subagent) bool { return agent.UnplacedCwd != "" }) {
		if _, err := session.Commit(ctx, func(tx durable.Tx) (any, error) {
			state, err := durable.TxDoc[contract.SubagentState](tx, contract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
			if err != nil {
				return nil, err
			}
			agents := state.Object("agents")
			for _, name := range agents.Keys() {
				agent := agents.Object(name)
				cwd, _ := agent.Get("unplacedCwd").(string)
				if cwd == "" {
					continue
				}
				conversation, _ := agent.Get("conversationId").(float64)
				var env any
				if on := spawnedOn(durable.ConversationId(conversation), cwd); on != nil {
					env = *on
				}
				if err := agent.Set("location", delta.JsonObjectOf("env", env, "cwd", cwd)); err != nil {
					return nil, err
				}
				agent.Delete("unplacedCwd")
			}
			return nil, nil
		}); err != nil {
			return nil, nil, err
		}
		if registry, err = contract.Registry(ctx, session); err != nil {
			return nil, nil, err
		}
	}
	for _, name := range registry.Names() {
		agent := registry.Agents[name]
		if agent.Location == nil {
			followers = append(followers, agent.ConversationId)
		} else {
			own = append(own, *contract.LocationOf(agent.Location))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(registry.OneShots)) {
		followers = append(followers, registry.OneShots[key].ConversationId)
	}
	return followers, own, nil
}

// spawnEnvironments tells where version 1 own cwds were spawned: on the environment the server recorded for them
// (recorded, the worker init value `subagentLocations`), else on the session's spawn environment (a session could not
// change machines while it had them).
func spawnEnvironments(recorded delta.JsonValue, sessionEnv *string) func(durable.ConversationId, string) *string {
	known, _ := recorded.(*delta.JsonObject)
	return func(conversation durable.ConversationId, cwd string) *string {
		if known != nil {
			if location, ok := known.Value(strconv.FormatInt(int64(conversation), 10)).(*delta.JsonObject); ok {
				if recordedCwd, _ := location.Value("cwd").(string); recordedCwd == cwd {
					if env, ok := location.Value("env").(string); ok {
						return &env
					}
					return nil
				}
			}
		}
		return sessionEnv
	}
}

// locator is what the subagent tool needs to place subagents: locating conversations and resolving directories.
type locator struct {
	// locate is where the conversation calling a tool works (the session's, or its own).
	locate func(ctx context.Context, conversation durable.ConversationId, read durable.DocumentReader) (p2runtime.Location, error)
	// sandbox is the sandbox a conversation working at location is confined to (its environment's configured type), "" if none.
	sandbox func(ctx context.Context, location p2runtime.Location) (string, error)
	// directory is target as an existing directory's location, relative to from, or why not (a non-empty problem).
	directory func(ctx context.Context, target p2runtime.LocationInput, from p2runtime.Location) (p2runtime.Location, string)
}

// placement is where a spawn or configure puts a subagent: env set (Env nil: this device) and cwd, or neither.
type placement struct {
	envSet bool
	env    *string
	cwd    *string
}

// placeTarget is where a subagent of a caller at caller should work: env (default the caller's; nil this device) and cwd
// (default the caller's cwd on its own environment, the home elsewhere). A non-empty problem when it is not an existing
// directory.
func placeTarget(ctx context.Context, where locator, caller p2runtime.Location, args placement) (p2runtime.Location, string, error) {
	env := caller.Env
	if args.envSet {
		env = args.env
	}
	// A caller in a sandbox places its subagents in the same sandbox only.
	sandbox, err := where.sandbox(ctx, caller)
	if err != nil {
		return p2runtime.Location{}, "", err
	}
	if sandbox != "" && (env == nil || *env != sandbox) {
		return p2runtime.Location{}, "You work in sandbox " + sandbox + "; your subagents can only work inside it.", nil
	}
	path := ""
	if args.cwd != nil {
		path = *args.cwd
	}
	resolved, problem := where.directory(ctx, p2runtime.LocationInput{Env: env, Path: path}, caller)
	if problem == "" {
		return resolved, "", nil
	}
	prefix := ""
	if !sameEnvironment(env, caller.Env) {
		if env == nil {
			prefix = "local:"
		} else {
			prefix = *env + ":"
		}
	}
	shown := "~"
	if args.cwd != nil {
		shown = *args.cwd
	}
	if problem == "not an existing directory" {
		return p2runtime.Location{}, "cwd " + prefix + shown + " is not an existing directory.", nil
	}
	return p2runtime.Location{}, "cwd " + prefix + shown + ": " + problem, nil
}

func sameEnvironment(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// archiver is what archiving needs: to abort a conversation and to commit, from a tool call's API or the worker's Harness.
type archiver struct {
	abort  func(ctx context.Context, id durable.ConversationId) error
	commit func(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error)
}

func archiverOfAPI(api durable.ToolExecutionApi) archiver {
	return archiver{
		abort: func(ctx context.Context, id durable.ConversationId) error {
			handle, err := api.Conversation(ctx, id)
			if err != nil || handle == nil {
				return err
			}
			return handle.Abort(ctx, nil)
		},
		commit: func(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
			return api.Commit(ctx, change)
		},
	}
}

func archiverOfHarness(session harness.Harness) archiver {
	return archiver{
		abort: func(ctx context.Context, id durable.ConversationId) error {
			handle, err := session.Conversation(ctx, id)
			if err != nil || handle == nil {
				return err
			}
			return handle.Abort(ctx, nil)
		},
		commit: func(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
			return session.Commit(ctx, change)
		},
	}
}

// setArchived archives (aborting its work first, as archiving a session stops its worker) or restores a subagent of
// conversation parent. Used by the tool's archive/restore and by the browser's Archive/Restore.
func setArchived(ctx context.Context, target archiver, parent, conversation durable.ConversationId, archived bool) error {
	if archived {
		if err := target.abort(ctx, conversation); err != nil {
			return err
		}
	}
	_, err := target.commit(ctx, func(tx durable.Tx) (any, error) {
		state, err := durable.TxDoc[contract.SubagentState](tx, contract.SubagentsDoc, parent)
		if err != nil {
			return nil, err
		}
		agents := state.Object("agents")
		for _, name := range agents.Keys() {
			agent := agents.Object(name)
			if id, _ := agent.Get("conversationId").(float64); durable.ConversationId(id) != conversation {
				continue
			}
			if archived {
				return nil, agent.Set("archived", true)
			}
			agent.Delete("archived")
			return nil, nil
		}
		return nil, nil
	})
	return err
}

// agentTools are an agent's tools as its Durable agent state takes them: exactly these enabled (nested calls resolve among
// them, so the script-only ones are named too; the worker adds ones installed later, see enableScriptTools), and the deferred
// ones it is offered from the start.
func agentTools(choice contract.ToolChoice) (harness.Change[harness.ToolChange], harness.Change[harness.ToolChange]) {
	tools := choice.Tools
	if tools == nil {
		tools = []*durable.ToolRegistration{}
	}
	loaded := choice.Loaded
	if loaded == nil {
		loaded = []*durable.ToolRegistration{}
	}
	return harness.SetTo(harness.ToolChange{List: tools}), harness.SetTo(harness.ToolChange{Edit: &harness.ToolEdit{Add: loaded}})
}

// enableScriptTools adds the tools behind code mode installed now (names) to every subagent with code mode, by name: MCP
// tools come and go with their servers, so the names written at spawn go stale. It runs between scripts, not inside one: a
// script's call resolves the agent once, at its start. A name whose tool goes away is ignored by Durable and works again
// when it returns, so none is removed.
func enableScriptTools(ctx context.Context, session harness.Harness, names []string) error {
	registry, err := contract.Registry(ctx, session)
	if err != nil {
		return err
	}
	var lacking []durable.ConversationId
	for _, name := range registry.Names() {
		agent := registry.Agents[name]
		if !slices.Contains(agent.Tools, contract.CodemodeTool) {
			continue
		}
		state, err := durable.Snapshot(ctx, session, harness.AgentDoc, agent.ConversationId)
		if err != nil {
			return err
		}
		if state == nil || state.Tools == nil || !state.Tools.Exact {
			continue
		}
		if enabled := state.Tools.Names; slices.ContainsFunc(names, func(name string) bool { return !slices.Contains(enabled, name) }) {
			lacking = append(lacking, agent.ConversationId)
		}
	}
	if len(lacking) == 0 {
		return nil
	}
	_, err = session.Commit(ctx, func(tx durable.Tx) (any, error) {
		for _, conversation := range lacking {
			state, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, conversation)
			if err != nil {
				return nil, err
			}
			if _, ok := state.Get("tools").(*delta.Array); !ok {
				continue
			}
			tools := state.Array("tools")
			present := map[string]bool{}
			for _, each := range tools.Snapshot() {
				if name, ok := each.(string); ok {
					present[name] = true
				}
			}
			for _, name := range names {
				if !present[name] {
					present[name] = true
					if _, err := tools.Push(name); err != nil {
						return nil, err
					}
				}
			}
		}
		return nil, nil
	})
	return err
}
