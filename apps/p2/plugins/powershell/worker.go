// Package powershell is p2's powershell plugin (apps/p2/plugins/powershell): PowerShell where an agent works on Windows, as
// Pi Mobile offers it (offerPowerShell), per agent: Durable's `powershell` tool, deferred, is offered to each conversation
// whose location's machine runs Windows (this device on a Windows host, a remote environment once it reports Windows) and
// that has the shell tool `bash`. The main agent and each subagent are decided independently, from where each works now,
// and again after every move.
package powershell

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell/contract"
	subagents "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/tools"
	"github.com/MichaelKinsy/PiG/env"
)

// agentKind is the kind of Durable's agent document.
const agentKind = "pi.agent"

// retryAfter is how long to wait before asking an unreachable remote environment again.
const retryAfter = 30 * time.Second

func without(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool { return name == contract.ToolName })
}

// enables is whether state enables a tool: every tool unless an array leaves it out or `remove` names it.
func enables(state *harness.AgentState, name string) bool {
	if state == nil || state.Tools == nil {
		return true
	}
	if state.Tools.Exact {
		return slices.Contains(state.Tools.Names, name)
	}
	return !slices.Contains(state.Tools.Remove, name)
}

// offersPowerShell is whether the conversation is offered PowerShell now: enabled, and loaded for the model (it is
// deferred).
func offersPowerShell(state *harness.AgentState) bool {
	if !enables(state, contract.ToolName) {
		return false
	}
	if state == nil || state.ModelTools == nil {
		return false
	}
	if state.ModelTools.Exact {
		return slices.Contains(state.ModelTools.Names, contract.ToolName)
	}
	return slices.Contains(state.ModelTools.Add, contract.ToolName)
}

func listOf(names []string) []any {
	list := make([]any, len(names))
	for i, name := range names {
		list[i] = name
	}
	return list
}

// setPowerShell offers PowerShell to the agent state or takes it away, leaving its other tool choices as they are.
func setPowerShell(draft *delta.Object, on bool) error {
	var state harness.AgentState
	if current, err := durable.FromJsonValue[harness.AgentState](draft.Snapshot()); err == nil {
		state = current
	}
	with := func(names []string) []string {
		if on {
			return append(without(names), contract.ToolName)
		}
		return without(names)
	}
	if state.Tools != nil {
		switch {
		case state.Tools.Exact:
			if err := draft.Set("tools", listOf(with(state.Tools.Names))); err != nil {
				return err
			}
		case on && slices.Contains(state.Tools.Remove, contract.ToolName):
			filter := delta.NewJsonObject(1)
			filter.Set("remove", listOf(without(state.Tools.Remove)))
			if err := draft.Set("tools", filter); err != nil {
				return err
			}
		}
	}
	if state.ModelTools != nil && state.ModelTools.Exact {
		return draft.Set("modelTools", listOf(with(state.ModelTools.Names)))
	}
	var add, remove []string
	if state.ModelTools != nil {
		add, remove = state.ModelTools.Add, state.ModelTools.Remove
	}
	add = without(add)
	if on {
		add = append(add, contract.ToolName)
	}
	filter := delta.NewJsonObject(2)
	if len(remove) > 0 {
		filter.Set("remove", listOf(remove))
	}
	if len(add) > 0 {
		filter.Set("add", listOf(add))
	}
	return draft.Set("modelTools", filter)
}

func servePowerShell(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	harnessOf := p2runtime.HarnessOf(self)
	workerOf := p2runtime.WorkerOf(self)
	offeredState := self.Writable(delta.JsonValue(delta.NewJsonObject(0)))
	powershell := tools.CreatePowerShellTool(nil)
	powershell.Deferred = true
	extension := &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: contract.ExtensionName},
		Tools:     []p2runtime.P2Tool{{ToolRegistration: powershell}},
	}
	self.(host.StoreSelf).Contribute(p2runtime.Extensions.Registry, p2runtime.Keyed[*p2runtime.P2Extension]{p2runtime.Entry(extension.Extension.Name, extension)})
	// Agents' tool choices and the subagents' locations change through commits; the session's through moves.
	commits := self.Open(
		func(context.Context) (any, error) {
			parts, err := harnessOf()
			if err != nil {
				return nil, err
			}
			session, err := parts.Durable()
			if err != nil {
				return nil, err
			}
			return p2runtime.NewCommitSignal(session, func(publication durable.CommitPublication) bool {
				for _, change := range publication.Changes {
					var kind string
					switch typed := change.(type) {
					case durable.DocumentChange:
						kind = typed.Record.Kind
					case durable.DocumentRetireChange:
						kind = typed.Record.Kind
					default:
						continue
					}
					if kind == agentKind || kind == subagents.SubagentsKind {
						return true
					}
				}
				return false
			}), nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*p2runtime.CommitSignal).Close()
			return nil
		},
	)
	// Remote systems known so far, by environment name.
	var systemsMu sync.Mutex
	systems := map[string]bool{}
	// isWindows is whether location's machine runs Windows; known false while unknown (an unreachable environment).
	isWindows := func(ctx context.Context, location p2runtime.Location) (windows, known bool) {
		if location.Env == nil {
			return runtime.GOOS == "windows", true
		}
		systemsMu.Lock()
		cached, ok := systems[*location.Env]
		systemsMu.Unlock()
		if ok {
			return cached, true
		}
		parts, err := harnessOf()
		if err != nil {
			return false, false
		}
		environments, err := parts.Environments()
		if err != nil {
			return false, false
		}
		executionEnv, err := environments.At(ctx, location)
		if err != nil {
			return false, false
		}
		var connection *env.Connection
		switch typed := executionEnv.(type) {
		case *env.LocalExecutionEnv:
			connection = typed.Connection()
		case *env.RemoteExecutionEnv:
			connection = typed.Connection
		}
		if connection == nil {
			return false, false
		}
		info, err := connection.Info(ctx)
		if err != nil {
			return false, false
		}
		windows = info.OS == "windows"
		systemsMu.Lock()
		systems[*location.Env] = windows
		systemsMu.Unlock()
		return windows, true
	}
	decide := func(ctx context.Context, session harness.Harness) (unknown bool, err error) {
		registry, err := subagents.Registry(ctx, session)
		if err != nil {
			return false, err
		}
		ids := []durable.ConversationId{durable.ROOT_CONVERSATION_ID}
		for _, name := range registry.Names() {
			ids = append(ids, registry.Agents[name].ConversationId)
		}
		for _, key := range slices.Sorted(mapKeys(registry.OneShots)) {
			ids = append(ids, registry.OneShots[key].ConversationId)
		}
		parts, err := harnessOf()
		if err != nil {
			return false, err
		}
		environments, err := parts.Environments()
		if err != nil {
			return false, err
		}
		offered := delta.NewJsonObject(len(ids))
		type change struct {
			id durable.ConversationId
			on bool
		}
		var changes []change
		for _, id := range ids {
			state, err := durable.Snapshot(ctx, session, harness.AgentDoc, id)
			if err != nil {
				return false, err
			}
			location, err := environments.Locate(ctx, id, session)
			if err != nil {
				return false, err
			}
			windows, known := isWindows(ctx, location)
			// Unknown keeps what it has: an unreachable machine does not take PowerShell away.
			on := offersPowerShell(state)
			if known {
				on = windows && enables(state, "bash")
			} else {
				unknown = true
			}
			offered.Set(fmt.Sprint(int64(id)), on)
			if on != offersPowerShell(state) {
				changes = append(changes, change{id, on})
			}
		}
		if len(changes) > 0 {
			if _, err := session.Commit(ctx, func(tx durable.Tx) (any, error) {
				for _, each := range changes {
					draft, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, each.id)
					if err != nil {
						return nil, err
					}
					if err := setPowerShell(draft, each.on); err != nil {
						return nil, err
					}
				}
				return nil, nil
			}); err != nil {
				return false, err
			}
		}
		return unknown, offeredState.Set(offered)
	}
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		parts, _ := harnessOf()
		value, err := commits.Value()
		if err != nil || parts == nil {
			return host.Threw(err)
		}
		signal := value.(*p2runtime.CommitSignal)
		return host.AwaitingCtx(ctx, func() error {
			session, err := parts.Durable()
			if err != nil {
				return err
			}
			worker, err := workerOf()
			if err != nil {
				return err
			}
			for ctx.Err() == nil {
				seen := signal.Version()
				unknown, err := decide(ctx, session)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					fmt.Fprintln(os.Stderr, "p2 powershell: deciding which agents are offered PowerShell failed:", err)
					unknown = true
				}
				// Again after a relevant commit or a move (another machine may run another system), and later while a remote
				// system is unknown.
				waiting, stop := context.WithCancel(ctx)
				woken := make(chan struct{}, 3)
				go func() { _ = signal.Next(waiting, seen); woken <- struct{}{} }()
				go func() { _, _ = worker.Location.Next(waiting); woken <- struct{}{} }()
				if unknown {
					go func() {
						select {
						case <-time.After(retryAfter):
							woken <- struct{}{}
						case <-waiting.Done():
						}
					}()
				}
				select {
				case <-woken:
				case <-ctx.Done():
				}
				stop()
			}
			return nil
		})
	})
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"offered": self.Derive([]any{offeredState}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
		},
	}}, nil
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

func init() {
	worker := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(contract.PowerShell, host.LocalFactory(servePowerShell), host.ServeOptions{})
	})
	hosts.RegisterPlugin(hosts.Plugin{Name: "powershell", Worker: &worker})
}
