package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
)

// Ports apps/p2/plugins/mcp/worker.ts: the MCP plugin's worker facet: the worker object holding the server's `Mcp` view. It
// sets the locations the session's conversations work at (the session's, active subagents' own), with the project
// `.pi/mcp.json` read at each through its environment, as its leases, again whenever they may have changed and whenever the
// view (re)activates, and offers the servers' tools as one Durable extension that follows the view's `servers` and the code
// mode switch: as in pi, tools are for code mode scripts, and only without code mode does `tool_search` (installed by the
// worker's registry) load them for the model. No MCP process or credential is ever in a worker.

// retryAfter is how long to wait before setting the locations again after a failure.
const retryAfter = 2 * time.Second

// codemodeExtension is the code mode plugin's extension: its switch is on while the `codemode` setting is, absent without
// the plugin.
const codemodeExtension = "codemode"

// Worker is the plugin's worker facet.
var Worker = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.mcp-worker", mcpWorker)
})

// mcpWorker is the worker object: the view, the extension of the servers' tools, and the leases at the conversations'
// locations.
func mcpWorker(self host.StoreSelf) error {
	harnessOf := p2runtime.HarnessOf(self)
	workerOf := p2runtime.WorkerOf(self)
	view := self.Get(contract.Mcp, delta.NewJsonObject(0))
	call := func(ctx context.Context, request McpCallRequest) (*pimcp.CallToolResult, error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		environments, err := parts.Environments()
		if err != nil {
			return nil, err
		}
		location, err := environments.Locate(ctx, request.ConversationID, request.Read)
		if err != nil {
			return nil, err
		}
		worker, err := workerOf()
		if err != nil {
			return nil, err
		}
		args := delta.JsonValue(delta.NewJsonObject(0))
		if request.Args != nil {
			args = toJSON(request.Args)
		}
		answered, err := worker.Root(func(root host.Ctx) (any, error) {
			return view.Call("call", delta.JsonObjectOf("location", toJSON(formOf(location)), "server", request.Server, "tool", request.Tool, "args", args), root)
		}, ctx)
		if err != nil {
			return nil, err
		}
		encoded, err := chordjson.Marshal(answered)
		if err != nil {
			return nil, err
		}
		var result pimcp.CallToolResult
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, err
		}
		result.Raw = encoded
		return &result, nil
	}
	// Code mode switched off or not loaded: the tools would be unreachable, so `tool_search` loads them, as pi falls back.
	reach := self.Derive([]any{self.Registry(p2runtime.ExtensionSwitches.Registry)}, func(values []any) (delta.JsonValue, error) {
		if table, ok := values[0].(*p2runtime.KeyedTable[*bool]); ok {
			if enabled, found := table.Get(codemodeExtension); found && enabled != nil && *enabled {
				return string(ReachCodemode), nil
			}
		}
		return string(ReachToolSearch), nil
	})
	extension := self.Writable(extensionsFor(McpOffer{Servers: []string{}, SignIn: []string{}, Reach: ReachCodemode}, call))
	self.Contribute(p2runtime.Extensions.Registry, extension)
	offerTools(self, view, reach, extension, call)
	leaseLocations(self, view, harnessOf, workerOf)
	return nil
}

// extensionsFor is the extension offering offer: the servers' tools and section. `tool_search`, which reaches them without code
// mode, is the worker's registry's (`DurableRegistry`), shared with other plugins' deferred tools.
func extensionsFor(offer McpOffer, call McpCall) p2runtime.Keyed[*p2runtime.P2Extension] {
	return p2runtime.Keyed[*p2runtime.P2Extension]{p2runtime.Entry(contract.McpExtension, CreateMcpExtension(offer, call))}
}

// offerTools follows the view's `servers` and `reach`: a new server or tool list, a server starting or ending waiting for a
// sign-in (not another status change) or a new reach makes new extensions.
func offerTools(self host.StoreSelf, view *host.Reference, reach chord.Watchable[delta.JsonValue], extension state.Writable[any], call McpCall) {
	servers := passThrough(self, view.Member("servers"))
	input := self.Derive([]any{servers, reach}, func(values []any) (delta.JsonValue, error) {
		return delta.JsonObjectOf("servers", values[0], "reach", values[1]), nil
	})
	offered := ""
	self.On(input, func(change chord.WatchCall[delta.JsonValue], _ host.Ctx) *host.Handled {
		in := objectOf(change.Value)
		instances, err := instancesOf(in.Value("servers"))
		if err != nil {
			return host.Threw(err)
		}
		offer := McpOffer{Servers: ServerNames(instances), SignIn: SignInNames(instances), Tools: ServerTools(instances), Reach: McpReach(stringOf(in.Value("reach")))}
		key, err := json.Marshal(offerKey(offer))
		if err != nil {
			return host.Threw(err)
		}
		if string(key) == offered {
			return nil
		}
		offered = string(key)
		if err := extension.Set(extensionsFor(offer, call)); err != nil {
			return host.Threw(err)
		}
		return nil
	})
}

// offerKey is what an offer is compared by: its servers, those waiting for a sign-in, its tools and its reach.
func offerKey(offer McpOffer) any {
	tools := make([]any, len(offer.Tools))
	for i, entry := range offer.Tools {
		tools[i] = map[string]any{"server": entry.Server, "tool": entry.Tool}
	}
	return map[string]any{"servers": offer.Servers, "signIn": offer.SignIn, "tools": tools, "reach": offer.Reach}
}

// leaseLocations sets the locations the conversations work at as the view's leases: once live, then after every subagent
// commit, move and re-activation of the view; a failure is retried, a refusal (a newer worker runs) ends it.
func leaseLocations(self host.StoreSelf, view *host.Reference, harnessOf func() (*p2runtime.HarnessParts, error), workerOf func() (*p2runtime.WorkerParts, error)) {
	// Subagents' own locations change through commits of their registry; the session's through moves.
	commits := self.Open(
		func(context.Context) (any, error) {
			parts, err := harnessOf()
			if err != nil {
				return nil, err
			}
			opened, err := parts.Durable()
			if err != nil {
				return nil, err
			}
			return p2runtime.NewCommitSignal(opened, func(publication durable.CommitPublication) bool {
				return p2runtime.ChangesDocument(publication, subagentscontract.SubagentsDoc.AnyDefinition().Kind)
			}), nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*p2runtime.CommitSignal).Close()
			return nil
		})
	// Counts re-activations of the view (a new view object, a reload): its first activation is the loop's first send.
	activations := &p2runtime.VersionSignal{}
	self.Open(
		func(context.Context) (any, error) {
			previous := host.RefStateKind("")
			var seen sync.Mutex
			return p2runtime.Follow(view.State(), func(state *host.RefState) {
				seen.Lock()
				defer seen.Unlock()
				if state.Kind == host.RefStateLive && previous != "" && previous != host.RefStateLive {
					activations.Bump()
				}
				previous = state.Kind
			}), nil
		},
		func(stop any, _ host.NativeCloseReason, _ context.Context) error {
			stop.(func())()
			return nil
		})
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			signal, err := commits.Value()
			if err != nil {
				return err
			}
			runLeases(ctx, view, harnessOf, workerOf, signal.(*p2runtime.CommitSignal), activations)
			return nil
		})
	})
}

func runLeases(ctx context.Context, view *host.Reference, harnessOf func() (*p2runtime.HarnessParts, error), workerOf func() (*p2runtime.WorkerParts, error),
	commits *p2runtime.CommitSignal, activations *p2runtime.VersionSignal) {
	for ctx.Err() == nil {
		worker, err := workerOf()
		if err != nil {
			log.Printf("p2 mcp: the worker process is not open: %v", err)
			return
		}
		seenCommits, seenMoves, seenViews := commits.Version(), worker.Location.Version(), activations.Version()
		if err := setLocations(ctx, view, harnessOf); err != nil {
			if ctx.Err() != nil {
				return
			}
			if failure, ok := data.IsChordError(err); ok && failure.Code == data.CodeForbidden {
				log.Printf("p2 mcp: a newer worker of this session leases its MCP servers; this one stops: %v", err)
				return
			}
			log.Printf("p2 mcp: setting the session's MCP locations failed, retrying: %v", err)
			select {
			case <-time.After(retryAfter):
			case <-ctx.Done():
			}
			continue
		}
		// A move or an activation while setting is not missed: its version is newer.
		if worker.Location.Version() != seenMoves || activations.Version() != seenViews {
			continue
		}
		nextChange(ctx, []func(context.Context){
			func(stop context.Context) { _ = commits.Next(stop, seenCommits) },
			func(stop context.Context) { _, _ = worker.Location.Next(stop) },
			func(stop context.Context) { _ = activations.Next(stop, seenViews) },
		})
	}
}

// setLocations sets the locations the conversations work at now, with the project files there.
func setLocations(ctx context.Context, view *host.Reference, harnessOf func() (*p2runtime.HarnessParts, error)) error {
	parts, err := harnessOf()
	if err != nil {
		return err
	}
	opened, err := parts.Durable()
	if err != nil {
		return err
	}
	environments, err := parts.Environments()
	if err != nil {
		return err
	}
	locations, err := workLocations(ctx, opened, environments)
	if err != nil {
		return err
	}
	projects, err := projectFiles(ctx, locations, environments)
	if err != nil {
		return err
	}
	places := make([]any, len(locations))
	for i, location := range locations {
		places[i] = toJSON(formOf(location))
	}
	_, err = view.Call("setLocations", delta.JsonObjectOf("locations", places, "projects", projects), ctx)
	return err
}

// workLocations is every distinct location the session's conversations work at: the main one's and active subagents'.
func workLocations(ctx context.Context, opened harness.Harness, environments p2runtime.ExecutionEnvironments) ([]p2runtime.Location, error) {
	registry, err := opened.SnapshotErased(ctx, subagentscontract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
	if err != nil {
		return nil, err
	}
	// Archived subagents rest until a send restores them (a commit); one-shot agents always follow the session.
	ids := []durable.ConversationId{durable.ROOT_CONVERSATION_ID}
	if registry != nil {
		if agents, ok := registry.Value("agents").(*delta.JsonObject); ok {
			for _, each := range agents.All() {
				agent, _ := each.(*delta.JsonObject)
				if archived, _ := agent.Value("archived").(bool); !archived {
					ids = append(ids, durable.ConversationId(intOf(agent.Value("conversationId"))))
				}
			}
		}
	}
	var locations []p2runtime.Location
	for _, id := range ids {
		location, err := environments.Locate(ctx, id, nil)
		if err != nil {
			return nil, err
		}
		known := false
		for _, each := range locations {
			known = known || p2runtime.SameLocation(each, location)
		}
		if !known {
			locations = append(locations, location)
		}
	}
	return locations, nil
}

// projectFiles are the project configurations at locations: `<cwd>/.pi/mcp.json` read through each location's environment (the
// manager checks them). A missing file is none; one too large or unreadable is reported as such.
func projectFiles(ctx context.Context, locations []p2runtime.Location, environments p2runtime.ExecutionEnvironments) ([]any, error) {
	files := []any{}
	for _, location := range locations {
		executionEnv, err := environments.At(ctx, location)
		if err != nil {
			return nil, err
		}
		path, joinFailure := executionEnv.JoinPath(ctx, []string{location.Cwd, ".pi", "mcp.json"})
		var text string
		var readFailure error
		if joinFailure == nil {
			text, readFailure = executionEnv.ReadTextFile(ctx, path)
		} else {
			readFailure = joinFailure
		}
		switch {
		case readFailure == nil && len(utf16Units(text)) <= contract.ProjectFileMaxChars:
			files = append(files, delta.JsonObjectOf("location", toJSON(formOf(location)), "text", text))
		case readFailure == nil:
			files = append(files, delta.JsonObjectOf("location", toJSON(formOf(location)), "error", fmt.Sprintf("larger than %d characters", contract.ProjectFileMaxChars)))
		case !isNotFound(readFailure):
			files = append(files, delta.JsonObjectOf("location", toJSON(formOf(location)), "error", truncateUnits(failureMessage(readFailure), 2000)))
		}
	}
	return files, nil
}

// nextChange returns after the first of waits (each given a context that ends the others); an abort just ends the wait.
func nextChange(ctx context.Context, waits []func(context.Context)) {
	stop, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{}, len(waits))
	var running sync.WaitGroup
	for _, wait := range waits {
		running.Add(1)
		go func() {
			defer running.Done()
			wait(stop)
			done <- struct{}{}
		}()
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
	cancel()
	running.Wait()
}

func utf16Units(text string) []uint16 { return utf16.Encode([]rune(text)) }

// truncateUnits is JavaScript's `text.slice(0, count)`.
func truncateUnits(text string, count int) string {
	units := utf16Units(text)
	if len(units) <= count {
		return text
	}
	return string(utf16.Decode(units[:count]))
}

// isNotFound is whether err is a file that does not exist.
func isNotFound(err error) bool {
	var failure *durableenv.FileError
	return errors.As(err, &failure) && failure.Code == durableenv.FileErrorNotFound
}

// failureMessage is the message of a file failure.
func failureMessage(err error) string {
	var failure *durableenv.FileError
	if errors.As(err, &failure) {
		return failure.Message
	}
	return err.Error()
}
