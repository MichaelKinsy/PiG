package subagents

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
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

// childFailure is a subagent's last run failure as a child carries it.
type childFailure struct {
	reason string
	detail *string
}

// seenChild is what the reports keep of a subagent between them: its newest entry, its newest message's time, its run
// failure.
type seenChild struct {
	lastEntryID *durable.EntryId
	updatedAt   *float64
	failure     *childFailure
}

// endings counts how many times each subagent's work ended since a report last read its live document, by conversation:
// the task graph's listener counts, a report's read takes the count it saw (an end counted meanwhile stays for the next).
type endings struct {
	mu     sync.Mutex
	counts map[durable.ConversationId]int
}

func (e *endings) count(id durable.ConversationId) (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n, ok := e.counts[id]
	return n, ok
}

func (e *endings) add(id durable.ConversationId) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counts[id]++
}

// settle forgets the count a read saw, unless more ends were counted while it read.
func (e *endings) settle(id durable.ConversationId, saw int, had bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now, ok := e.counts[id]; ok == had && now == saw {
		delete(e.counts, id)
	}
}

func (e *endings) prune(listed map[durable.ConversationId]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id := range e.counts {
		if !listed[id] {
			delete(e.counts, id)
		}
	}
}

// FailureSource says where a report takes a subagent's run failure from.
type FailureSource struct {
	// Read: the live document. Otherwise Failure is what the last report read.
	Read    bool
	Failure *childFailure
}

// failureSourceOf is where a report takes a subagent's run failure from, given what the last report saw of it (`known`),
// its newest entry now and whether its work ended since. A run's failure changes only in a commit that writes an entry or
// ends its work (a stop or a missing model can end it without an entry): with neither, and a report before, the cached
// one holds.
func failureSourceOf(known *seenChild, newest *durable.EntryId, ended bool) FailureSource {
	if known == nil || ended || !sameEntry(newest, known.lastEntryID) {
		return FailureSource{Read: true}
	}
	return FailureSource{Failure: known.failure}
}

func sameEntry(a, b *durable.EntryId) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func timestampOf(message ai.Message) float64 {
	switch message := message.(type) {
	case ai.SystemMessage:
		return float64(message.Timestamp)
	case ai.UserMessage:
		return float64(message.Timestamp)
	case ai.AssistantMessage:
		return float64(message.Timestamp)
	case ai.ToolResultMessage:
		return float64(message.Timestamp)
	}
	return 0
}

// newestEntries is how many of a subagent's newest entries a report reads to find its newest message.
const newestEntries = 20

// newestOf is a subagent's newest entry and when its newest message was written: its entries from the newest one seen
// on, newest first (an idle subagent's is one entry). Without a message among them, the time seen before stays.
func newestOf(ctx context.Context, conversation harness.Conversation, entries entriesReader, known *seenChild) (*durable.EntryId, *float64, error) {
	var since *durable.EntryId
	var knownTime *float64
	if known != nil {
		since, knownTime = known.lastEntryID, known.updatedAt
	}
	page, err := entries(ctx, conversation, durable.EntryQuery{MinEntryId: since}, newestEntries)
	if err != nil {
		return nil, nil, err
	}
	if len(page) == 0 || (since != nil && page[0].Id == *since) {
		return since, knownTime, nil
	}
	newest := page[0].Id
	for _, entry := range page {
		if len(entry.Model) > 0 {
			at := timestampOf(entry.Model[len(entry.Model)-1])
			return &newest, &at, nil
		}
	}
	return &newest, knownTime, nil
}

// entriesReader reads a conversation's entries, newest first.
type entriesReader func(ctx context.Context, conversation harness.Conversation, query durable.EntryQuery, limit int) ([]durable.EntryRecord, error)

// childFailureOf is a live document's run failure as a child carries it.
func childFailureOf(live *harness.LiveState) *childFailure {
	if live == nil || live.RunFailure == nil {
		return nil
	}
	return &childFailure{reason: string(live.RunFailure.Reason), detail: live.RunFailure.Detail}
}

// detailsOf is a subagent's newest entry, time and failure: its entries since the newest one seen, and its live
// document's run failure when that can have changed (`failureSourceOf`).
func detailsOf(ctx context.Context, session harness.Harness, id durable.ConversationId, seen map[durable.ConversationId]*seenChild, ended *endings) (lastEntryID float64, updatedAt *float64, failure *childFailure, err error) {
	conversation, err := session.Conversation(ctx, id)
	if err != nil || conversation == nil {
		return 0, nil, nil, err
	}
	known := seen[id]
	entries := func(ctx context.Context, c harness.Conversation, query durable.EntryQuery, limit int) ([]durable.EntryRecord, error) {
		page, err := c.Entries(ctx, query, limit, nil)
		if err != nil {
			return nil, err
		}
		return page.Items, nil
	}
	newest, at, err := newestOf(ctx, conversation, entries, known)
	if err != nil {
		return 0, nil, nil, err
	}
	saw, had := ended.count(id)
	if source := failureSourceOf(known, newest, had); !source.Read {
		failure = source.Failure
	} else {
		live, err := durable.Snapshot(ctx, session, harness.LiveDoc, id)
		if err != nil {
			return 0, nil, nil, err
		}
		failure = childFailureOf(live)
		// An end counted while the live document was read stays for the next report.
		ended.settle(id, saw, had)
	}
	seen[id] = &seenChild{lastEntryID: newest, updatedAt: at, failure: failure}
	if newest != nil {
		lastEntryID = float64(*newest)
	}
	return lastEntryID, at, failure, nil
}

// childrenOf is the session's subagents and one-shot agents as the server's index lists them. `seen` holds what the last
// report saw of each subagent, by conversation; it and `ended` are pruned to the registry's subagents.
func childrenOf(ctx context.Context, session harness.Harness, seen map[durable.ConversationId]*seenChild, ended *endings) ([]any, error) {
	registry, err := contract.Registry(ctx, session)
	if err != nil {
		return nil, err
	}
	listed := map[durable.ConversationId]bool{}
	for _, name := range registry.Names() {
		listed[registry.Agents[name].ConversationId] = true
	}
	for id := range seen {
		if !listed[id] {
			delete(seen, id)
		}
	}
	ended.prune(listed)
	children := []any{}
	for _, name := range registry.Names() {
		agent := registry.Agents[name]
		latest, updatedAt, failure, err := detailsOf(ctx, session, agent.ConversationId, seen, ended)
		if err != nil {
			return nil, err
		}
		entry := delta.JsonObjectOf("name", name, "conversationId", float64(agent.ConversationId), "model", agent.Model,
			"thinkingLevel", agent.ThinkingLevel, "tools", stringsAny(agent.Tools))
		if agent.Location != nil {
			entry.Set("location", locationJSON(*contract.LocationOf(agent.Location)))
		}
		entry.Set("lastEntryId", latest)
		if updatedAt != nil {
			entry.Set("updatedAt", *updatedAt)
		}
		if failure != nil {
			shown := delta.JsonObjectOf("reason", failure.reason)
			if failure.detail != nil {
				shown.Set("detail", *failure.detail)
			}
			entry.Set("failure", shown)
		}
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
		seen := map[durable.ConversationId]*seenChild{}
		ended := &endings{counts: map[durable.ConversationId]int{}}
		push := &childReporter{report: func(ctx context.Context) error {
			opened, err := session()
			if err != nil {
				return err
			}
			children, err := childrenOf(ctx, opened, seen, ended)
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
		// A subagent's run can end without an entry (stopped, no model): the subagents whose work ended are counted and
		// pushed, so the report reads their live documents. Other task changes cost a scan of the graph, no read.
		graph := self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[harness.TaskGraph]) (durable.Observation[harness.TaskGraph], error) {
			opened, err := harnessOf()
			if err != nil {
				return nil, err
			}
			held, err := opened.Durable()
			if err != nil {
				return nil, err
			}
			return held.ObserveTaskGraph(ctx, observer)
		}))
		working := map[durable.ConversationId]bool{}
		self.On(graph, func(_ chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
			current := workingChildren(graph.Value().(*delta.JsonObject), seen)
			var finished []durable.ConversationId
			for id := range working {
				if !current[id] {
					finished = append(finished, id)
				}
			}
			working = current
			if len(finished) == 0 {
				return nil
			}
			for _, id := range finished {
				ended.add(id)
			}
			return host.AwaitingCtx(ctx, func() error { return push.push(ctx) })
		})
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
		// First use in this section: the method runs off the loop and takes the kept natives.
		_, _ = harnessOf()
		_, _ = worker()
		return contracts.Implementation{Variant: "participant", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"archive": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in, _ := args.(*delta.JsonObject)
				conversation, _ := in.Value("conversation").(float64)
				archived, _ := in.Value("archived").(bool)
				// The method waits for Durable, whose publications need the host's loop.
				chordctx.Suspend(ctx)
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
	return p2runtime.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
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

// workingChildren is the subagents of `seen` with foreground work, as the task graph lists them.
func workingChildren(graph *delta.JsonObject, seen map[durable.ConversationId]*seenChild) map[durable.ConversationId]bool {
	working := map[durable.ConversationId]bool{}
	nodes, _ := graph.Value("tasks").(*delta.JsonObject)
	if nodes == nil {
		return working
	}
	for _, value := range nodes.All() {
		node, _ := value.(*delta.JsonObject)
		if background, _ := node.Value("background").(bool); background {
			continue
		}
		id, _ := node.Value("conversationId").(float64)
		if _, ok := seen[durable.ConversationId(id)]; ok {
			working[durable.ConversationId(id)] = true
		}
	}
	return working
}
