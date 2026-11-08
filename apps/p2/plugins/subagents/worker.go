package subagents

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/subagents/worker.ts: the session's subagents on its worker: their tools, where they work, their
// read-only one-shot agents, the child list pushed to the server and the browser's Archive/Restore.

const readOnly = "One-shot agents are read-only"

func cached[T any](get func() (*T, error)) func() (*T, error) {
	var kept atomic.Pointer[T]
	return func() (*T, error) {
		if value := kept.Load(); value != nil {
			return value, nil
		}
		value, err := get()
		if err == nil {
			kept.Store(value)
		}
		return value, err
	}
}

// childReporter pushes the session's child list to the server, one push at a time, each reading the registry when it
// starts.
type childReporter struct {
	mu     sync.Mutex
	report func(ctx context.Context) error
}

// push resolves once the server committed a report read after every change made before this call.
func (r *childReporter) push(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.report(ctx)
}

// reports is the worker's child reporter, for methods that push what they changed (contributed by the tools' object).
type reports struct{ reporter atomic.Pointer[childReporter] }

// childrenOf is the session's subagents and one-shot agents as the server's index lists them.
func childrenOf(ctx context.Context, session harness.Harness) ([]any, error) {
	registry, err := contract.Registry(ctx, session)
	if err != nil {
		return nil, err
	}
	children := []any{}
	for _, name := range registry.Names() {
		agent := registry.Agents[name]
		var latest durable.EntryId
		if child, err := session.Conversation(ctx, agent.ConversationId); err != nil {
			return nil, err
		} else if child != nil {
			page, err := child.Entries(ctx, durable.EntryQuery{}, 1, nil)
			if err != nil {
				return nil, err
			}
			if len(page.Items) > 0 {
				latest = page.Items[0].Id
			}
		}
		entry := delta.JsonObjectOf("name", name, "conversationId", float64(agent.ConversationId), "model", agent.Model,
			"thinkingLevel", agent.ThinkingLevel, "tools", stringsAny(agent.Tools))
		if agent.Location != nil {
			entry.Set("location", locationJSON(*contract.LocationOf(agent.Location)))
		}
		entry.Set("lastEntryId", float64(latest))
		if agent.Archived {
			entry.Set("archived", true)
		}
		children = append(children, entry)
	}
	callIDs := make([]string, 0, len(registry.OneShots))
	for callID := range registry.OneShots {
		callIDs = append(callIDs, callID)
	}
	slices.Sort(callIDs)
	for _, callID := range callIDs {
		agent := registry.OneShots[callID]
		children = append(children, delta.JsonObjectOf("name", agent.Label, "kind", "agent", "callId", callID,
			"conversationId", float64(agent.ConversationId), "model", agent.Model, "thinkingLevel", agent.ThinkingLevel,
			"tools", stringsAny(agent.Tools), "lastEntryId", float64(0)))
	}
	return children, nil
}

// placement is where the conversations besides the main one work: the opening Harness gets it (without a Harness
// dependency) and moves the followers with the session. Only a subagent's own registry location keeps it elsewhere.
type conversationPlacement struct{}

func (conversationPlacement) Place(ctx context.Context, session harness.Harness, sessionLocation p2runtime.Location, values *delta.JsonObject) ([]durable.ConversationId, []p2runtime.Location, error) {
	var recorded delta.JsonValue
	if values != nil {
		recorded = values.Value("subagentLocations")
	}
	return placeSubagents(ctx, session, spawnEnvironments(recorded, sessionLocation.Env))
}

func (conversationPlacement) Own(ctx context.Context, read durable.DocumentReader, id durable.ConversationId) (*p2runtime.Location, error) {
	return contract.OwnLocation(ctx, read, id)
}

// toolsContribution contributes the subagent and team tools, the hooks around them and the reports to the server.
func toolsContribution(shared *reports) func(self host.StoreSelf) error {
	return func(self host.StoreSelf) error {
		harnessOf := cached(p2runtime.HarnessOf(self))
		config := self.Registry(p2runtime.HarnessConfig.Registry)
		feed := self.Get(contract.Subagents, delta.NewJsonObject(0))
		extensionsSource := self.Registry(p2runtime.Extensions.Registry)
		switchesSource := self.Registry(p2runtime.ExtensionSwitches.Registry)
		extensions := func() *p2runtime.KeyedTable[*p2runtime.P2Extension] {
			table, _ := p2runtime.Extensions.Table(extensionsSource)
			return table
		}
		session := func() (harness.Harness, error) {
			parts, err := harnessOf()
			if err != nil {
				return nil, err
			}
			return parts.Durable()
		}
		environment := subagentEnvironment{
			// Durable phases capture their tool registration, while model choices come from the live model observer.
			models: func() durable.Models {
				table, _ := config.Value().(*p2runtime.KeyedTable[p2runtime.HarnessConfigEntry])
				if table == nil {
					panic("No model configuration")
				}
				entry, ok := table.Get("default")
				if !ok || entry.Models == nil {
					panic("No model configuration")
				}
				return entry.Models()
			},
			access: func() map[string]p2runtime.P2Tool { return p2runtime.ToolsByName(extensions()) },
			// A spawned subagent's own location is on its caller's machine: where the caller works now.
			locate: locator{
				locate: func(ctx context.Context, conversation durable.ConversationId, read durable.DocumentReader) (p2runtime.Location, error) {
					parts, err := harnessOf()
					if err != nil {
						return p2runtime.Location{}, err
					}
					environments, err := parts.Environments()
					if err != nil {
						return p2runtime.Location{}, err
					}
					return environments.Locate(ctx, conversation, read)
				},
				directory: func(ctx context.Context, target p2runtime.LocationInput, from p2runtime.Location) (p2runtime.Location, string) {
					parts, err := harnessOf()
					if err != nil {
						return p2runtime.Location{}, err.Error()
					}
					environments, err := parts.Environments()
					if err != nil {
						return p2runtime.Location{}, err.Error()
					}
					return p2runtime.ResolveDirectory(ctx, environments, target, from)
				},
				sandbox: func(ctx context.Context, location p2runtime.Location) (string, error) {
					parts, err := harnessOf()
					if err != nil {
						return "", err
					}
					environments, err := parts.Environments()
					if err != nil {
						return "", err
					}
					return environments.Sandbox(ctx, location.Env)
				},
			},
		}
		subagent := &p2runtime.P2Extension{
			Extension: &durable.Extension{Name: subagentExtension, Tasks: []durable.AnyTask{anchor.Erased(), reporter.Erased()}},
			Tools:     []p2runtime.P2Tool{newSubagentTool(environment)},
		}
		team := &p2runtime.P2Extension{
			Extension: &durable.Extension{Name: teamExtensionName},
			Tools:     []p2runtime.P2Tool{teamTool()},
		}
		p2runtime.ContributeKeyed(self, p2runtime.Extensions,
			p2runtime.Entry(subagent.Extension.Name, subagent), p2runtime.Entry(team.Extension.Name, team))

		// The tools behind code mode, as the Harness's registry installs them (switched-off extensions left out).
		var enabled atomic.Pointer[[]string]
		refresh := func(ctx host.Ctx) *host.Handled {
			_, _ = harnessOf()
			names := scriptToolNames(extensions(), switchesSource)
			if previous := enabled.Load(); previous != nil && slices.Equal(*previous, names) {
				return nil
			}
			return host.AwaitingCtx(ctx, func() error {
				opened, err := session()
				if err != nil {
					return err
				}
				if err := enableScriptTools(ctx, opened, names); err != nil {
					return err
				}
				enabled.Store(&names)
				return nil
			})
		}
		self.OnNative(extensionsSource, func(_ chord.NativeWatchCall[any], ctx host.Ctx) *host.Handled { return refresh(ctx) })
		self.OnNative(switchesSource, func(_ chord.NativeWatchCall[any], ctx host.Ctx) *host.Handled { return refresh(ctx) })

		p2runtime.ContributeKeyed(self, p2runtime.ConversationConfigured, p2runtime.Entry[p2runtime.ConversationConfiguredHook]("subagents",
			func(ctx host.Ctx, conversation harness.Conversation) error {
				if conversation.Id() == durable.ROOT_CONVERSATION_ID {
					return nil
				}
				agent, err := conversation.Agent(ctx)
				if err != nil {
					return err
				}
				names := contract.ChoosableTools(contract.CallerToolsOf(agent), p2runtime.ToolsByName(extensions()))
				model := ""
				if agent.Model != nil {
					model = agent.Model.Provider + "/" + agent.Model.ModelId
				}
				_, err = conversation.Commit(ctx, func(tx durable.Tx) (any, error) {
					state, err := durable.TxDoc[contract.SubagentState](tx, contract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
					if err != nil {
						return nil, err
					}
					agents := state.Object("agents")
					for _, name := range agents.Keys() {
						child := agents.Object(name)
						if id, _ := child.Get("conversationId").(float64); durable.ConversationId(id) != conversation.Id() {
							continue
						}
						if model != "" {
							if err := child.Set("model", model); err != nil {
								return nil, err
							}
						}
						if err := child.Set("thinkingLevel", string(agent.ThinkingLevel)); err != nil {
							return nil, err
						}
						tools := make([]string, len(names))
						for i, tool := range names {
							tools[i] = tool.Name
						}
						return nil, child.Set("tools", stringsAny(tools))
					}
					return nil, nil
				})
				return err
			}))
		// Only the sidebar orders by activity, and it lists no one-shot agents: their entries do not report. The set is also
		// what refuses steering them: the check runs on the host's loop, where a read of the session would wait for a
		// publication that loop has to deliver, so it never reads the Durable registry.
		var oneShotsMu sync.Mutex
		oneShots := map[durable.ConversationId]bool{}
		// A one-shot agent belongs to the script waiting for its answer: it can be read and stopped, not steered.
		p2runtime.ContributeKeyed(self, p2runtime.ReadOnlyConversations, p2runtime.Entry[p2runtime.ReadOnlyCheck]("subagents",
			func(_ host.Ctx, conversation int) (string, error) {
				oneShotsMu.Lock()
				defer oneShotsMu.Unlock()
				if oneShots[durable.ConversationId(conversation)] {
					return readOnly, nil
				}
				return "", nil
			}))

		commits := self.Open(
			func(context.Context) (any, error) {
				opened, err := session()
				if err != nil {
					return nil, err
				}
				return p2runtime.NewCommitSignal(opened, func(publication durable.CommitPublication) bool {
					oneShotsMu.Lock()
					defer oneShotsMu.Unlock()
					for _, change := range publication.Changes {
						switch typed := change.(type) {
						case durable.DocumentChange:
							if typed.Record.Kind == contract.SubagentsKind {
								return true
							}
						case durable.DocumentRetireChange:
							if typed.Record.Kind == contract.SubagentsKind {
								return true
							}
						case durable.EntryWrite:
							if id := typed.Value.ConversationId; id != durable.ROOT_CONVERSATION_ID && !oneShots[id] {
								return true
							}
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
		push := &childReporter{report: func(ctx context.Context) error {
			opened, err := session()
			if err != nil {
				return err
			}
			children, err := childrenOf(ctx, opened)
			if err != nil {
				return err
			}
			oneShotsMu.Lock()
			for _, each := range children {
				if child := each.(*delta.JsonObject); child.Value("kind") == "agent" {
					id, _ := child.Value("conversationId").(float64)
					oneShots[durable.ConversationId(id)] = true
				}
			}
			oneShotsMu.Unlock()
			_, err = feed.Call("report", delta.JsonObjectOf("children", children), ctx)
			return err
		}}
		shared.reporter.Store(push)
		// What the agent changes on its own (a spawn, a subagent's messages moving it up the list) is pushed after each
		// commit that touches it; methods push their own changes before they answer (`SubagentControl.archive`).
		self.OnLive(func(ctx host.Ctx) *host.Handled {
			_, _ = harnessOf()
			value, err := commits.Value()
			if err != nil {
				return host.Threw(err)
			}
			signal := value.(*p2runtime.CommitSignal)
			return host.AwaitingCtx(ctx, func() error {
				for ctx.Err() == nil {
					seen := signal.Version()
					err := push.push(ctx)
					if err == nil {
						err = signal.Next(ctx, seen)
					}
					if err != nil {
						if ctx.Err() != nil {
							return nil
						}
						fmt.Fprintln(os.Stderr, "p2 subagents: report failed, retrying:", err)
						select {
						case <-time.After(time.Second):
						case <-ctx.Done():
						}
					}
				}
				return nil
			})
		})
		return nil
	}
}

// scriptToolNames are the names of the tools behind code mode, as the Harness's registry installs them: those of the
// extensions not switched off that the model does not call, sorted.
func scriptToolNames(extensions *p2runtime.KeyedTable[*p2runtime.P2Extension], switchesSource interface{ Value() any }) []string {
	switches, _ := switchesSource.Value().(*p2runtime.KeyedTable[*bool])
	names := []string{}
	if extensions == nil {
		return names
	}
	for _, key := range extensions.Keys() {
		if switches != nil {
			if on, _ := switches.Get(key); on != nil && !*on {
				continue
			}
		}
		extension, _ := extensions.Get(key)
		for _, tool := range extension.Tools {
			if !contract.CalledByModel(tool.ToolRegistration) {
				names = append(names, tool.Name)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func serveControl(shared *reports) func(*delta.JsonObject, data.Principal, host.Self) (contracts.Implementation, error) {
	return func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, _ := params.Value("session").(string)
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanSend(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("participants only")
		}
		harnessOf := cached(p2runtime.HarnessOf(self))
		worker := cached(p2runtime.WorkerOf(self))
		return contracts.Implementation{Variant: "participant", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"archive": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in, _ := args.(*delta.JsonObject)
				conversation, _ := in.Value("conversation").(float64)
				archived, _ := in.Value("archived").(bool)
				parts, err := worker()
				if err != nil {
					return nil, err
				}
				return nil, parts.Liveness.Call(func() error {
					harnessParts, err := harnessOf()
					if err != nil {
						return err
					}
					opened, err := harnessParts.Durable()
					if err != nil {
						return err
					}
					registry, err := contract.Registry(ctx, opened)
					if err != nil {
						return err
					}
					id := durable.ConversationId(conversation)
					for _, agent := range registry.OneShots {
						if agent.ConversationId == id {
							return p2runtime.Forbidden(readOnly)
						}
					}
					known := false
					for _, agent := range registry.Agents {
						known = known || agent.ConversationId == id
					}
					if !known {
						return fmt.Errorf("Unknown conversation")
					}
					if err := setArchived(ctx, archiverOfHarness(opened), durable.ROOT_CONVERSATION_ID, id, archived); err != nil {
						return err
					}
					// The sidebar's archived flag is the server's: pushed before the answer.
					if reporter := shared.reporter.Load(); reporter != nil {
						return reporter.push(ctx)
					}
					return nil
				})
			},
		}}}, nil
	}
}

// workerFacet is the plugin's worker facet.
func workerFacet() host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
		shared := &reports{}
		// Subagents spawned without a cwd and one-shot agents follow the session: the opening Harness gets this (without a
		// Harness dependency) and moves them with it.
		if err := p2runtime.ConversationLocations.Contribute(self, p2runtime.Entry[p2runtime.ConversationPlacement]("subagents", conversationPlacement{})); err != nil {
			return err
		}
		if err := p2runtime.Contribute(facet, "p2.subagent-tools", toolsContribution(shared)); err != nil {
			return err
		}
		return facet.Serve(contract.SubagentControl, host.RemoteFactory(serveControl(shared)), host.ServeOptions{})
	})
}
