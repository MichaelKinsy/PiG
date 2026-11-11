package cachewarming

import (
	"context"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/cache-warming/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/cache-warming/worker.ts: cache warming in the session worker, a Durable extension whose
// `afterResponse` hook starts warming each conversation's last request (warmer.go), bound to the Harness, the settings and
// the worker's liveness.

// workerHost binds the warmers to the worker.
type workerHost struct {
	harnessOf func() (*p2runtime.HarnessParts, error)
	worker    func() (*p2runtime.WorkerParts, error)
	settings  *host.Reference
}

func (h *workerHost) session() (harness.Harness, error) {
	parts, err := h.harnessOf()
	if err != nil {
		return nil, err
	}
	return parts.Durable()
}

// Mode is the `cacheWarming` setting, "idle" by default.
func (h *workerHost) Mode() Mode {
	if current, err := h.settings.Member("settings").Value(); err == nil {
		if mode, ok := objectOf(current).Value("cacheWarming").(string); ok {
			return Mode(mode)
		}
	}
	return ModeIdle
}

func (h *workerHost) Phase(ctx context.Context, conversationID durable.ConversationId, request *p2runtime.ModelRequest) (ai.CacheWarmingPhase, bool, error) {
	session, err := h.session()
	if err != nil {
		return "", false, err
	}
	live, err := durable.Snapshot[harness.LiveState](ctx, session, harness.LiveDoc, conversationID)
	if err != nil {
		return "", false, err
	}
	// A request is in flight, backing off or being polled: its response replaces the run.
	if live != nil && live.Generation != nil {
		return "", false, nil
	}
	running := live != nil && live.Run != nil
	// After its run, a subagent's or one-shot agent's work is usually done; the main conversation and its `/tree` branches
	// wait for the person or for agents' reports.
	if !running {
		top, err := p2runtime.IsTopLevel(ctx, session, conversationID)
		if err != nil || !top {
			return "", false, err
		}
	}
	conversation, err := session.Conversation(ctx, conversationID)
	if err != nil || conversation == nil {
		return "", false, err
	}
	agent, err := conversation.Agent(ctx)
	if err != nil {
		return "", false, err
	}
	if agent.Model == nil || agent.Model.Provider != providerOf(request.Model) || agent.Model.ModelId != request.Model.ID {
		return "", false, nil
	}
	// A reset, compaction or edit replaced what the request sent: the next request starts differently.
	view, err := conversation.Context(ctx, nil)
	if err != nil || !startsWith(view.Messages, request.Messages) {
		return "", false, err
	}
	if running {
		return ai.CacheWarmingPhaseStreaming, true, nil
	}
	return ai.CacheWarmingPhaseIdle, true, nil
}

// Record adds the refresh's usage to the conversation's pi.usage, as Durable's own recording does.
func (h *workerHost) Record(ctx context.Context, conversationID durable.ConversationId, message *ai.AssistantMessage) error {
	session, err := h.session()
	if err != nil {
		return err
	}
	_, err = session.Commit(ctx, func(tx durable.Tx) (any, error) {
		return nil, harness.RecordUsage(tx, conversationID, harness.UsageModels, message.Provider+"/"+message.Model, message.Usage)
	})
	return err
}

func (h *workerHost) KeepAlive(delta int) {
	if parts, err := h.worker(); err == nil {
		parts.Liveness.AddCacheWarming(delta)
	}
}

// providerOf is the ID of the model's provider (a model made from a models.json override has no Provider value).
func providerOf(model *ai.Model) string {
	if model.ProviderMeta.ProviderID != "" || model.Provider == nil {
		return model.ProviderMeta.ProviderID
	}
	return model.Provider.ID()
}

// startsWith is whether current starts with sent, compared by role and timestamp.
func startsWith(current, sent []ai.Message) bool {
	if len(current) < len(sent) {
		return false
	}
	for index, message := range sent {
		if current[index].MessageRole() != message.MessageRole() || timestampOf(current[index]) != timestampOf(message) {
			return false
		}
	}
	return true
}

// timestampOf is the timestamp of a message of any role; 0 when it has none.
func timestampOf(message ai.Message) int64 {
	switch typed := message.(type) {
	case ai.UserMessage:
		return typed.Timestamp
	case *ai.UserMessage:
		return typed.Timestamp
	case ai.AssistantMessage:
		return typed.Timestamp
	case *ai.AssistantMessage:
		return typed.Timestamp
	case ai.ToolResultMessage:
		return typed.Timestamp
	case *ai.ToolResultMessage:
		return typed.Timestamp
	case ai.SystemMessage:
		return typed.Timestamp
	case *ai.SystemMessage:
		return typed.Timestamp
	}
	return 0
}

func objectOf(value delta.JsonValue) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func serveRuntime(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	harnessOf := p2runtime.HarnessOf(self)
	worker := p2runtime.WorkerOf(self)
	settings := self.Get(modelscontract.WorkerModels, delta.NewJsonObject(0))
	bound := &workerHost{harnessOf: harnessOf, worker: worker, settings: settings}
	// Timers and in-flight refreshes live in this native: a reload or the worker's stop ends them, and the next response
	// starts warming again. Nothing of it is durable: the requests it repeats are not stored.
	warmers := self.Open(
		func(context.Context) (any, error) { return NewConversationWarmers(chordctx.Background(), bound), nil },
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*ConversationWarmers).Close()
			return nil
		},
	)
	extension := &durable.Extension{
		Name: contract.ExtensionName,
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
			AfterResponse: func(ctx context.Context, message ai.AssistantMessage, api harness.HookApi) error {
				parts, err := harnessOf()
				if err != nil {
					return err
				}
				request, err := parts.RequestOf(ctx, api, message)
				if err != nil {
					// Warming is best effort: a response is never held up by it.
					fmt.Fprintln(os.Stderr, "p2 cache-warming: finding the request failed:", err)
					return nil
				}
				if request == nil {
					return nil
				}
				value, err := warmers.Value()
				if err != nil {
					return nil
				}
				value.(*ConversationWarmers).Start(api.ConversationId(), message, request, api.Models())
				return nil
			},
		})},
	}
	// `cacheWarming: "off"` uninstalls the hook live; runs already warming stop at their next refresh.
	switches := self.Writable(p2runtime.Keyed[*bool]{p2runtime.Entry(extension.Name, new(true))})
	self.On(settings.Member("settings"), func(call chord.WatchCall[delta.JsonValue], _ host.Ctx) *host.Handled {
		on := true
		if object := objectOf(call.Value); object != nil {
			on = object.Value("cacheWarming") != "off"
		}
		_ = switches.Set(p2runtime.Keyed[*bool]{p2runtime.Entry(extension.Name, new(on))})
		return nil
	})
	self.(host.StoreSelf).Contribute(p2runtime.ExtensionSwitches.Registry, switches)
	self.(host.StoreSelf).Contribute(p2runtime.Extensions.Registry, p2runtime.Keyed[*p2runtime.P2Extension]{p2runtime.Entry(extension.Name, &p2runtime.P2Extension{Extension: extension})})
	return contracts.Implementation{}, nil
}

// NewFacet is a new value of the plugin's worker facet (a reload with it replaces what the plugin contributed).
func NewFacet() host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(contract.CacheWarmingRuntime, host.LocalFactory(serveRuntime), host.ServeOptions{})
	})
}
