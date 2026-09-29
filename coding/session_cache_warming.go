package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Cache warming wiring. Mirrors upstream sdk.ts (the CacheWarmer each
// AgentSession gets, and the streamFn that restarts it) and agent-session.ts
// (onAgentSettled, dispose, cacheWarmingStatus, setCacheWarmingMode).

// sessionCacheWarming holds the warmer bound to the current inner Session. Retired warmers are owned only until their cancelled refreshes drain; Close joins those tasks without retaining completed Sessions.
type sessionCacheWarming struct {
	mu        sync.Mutex
	warmer    *icodingagent.CacheWarmer
	sessionID string
	closed    bool
	retired   sync.WaitGroup
}

// cacheWarmingStreamFn installs response observers and restarts cache warming
// before sending each agent request through the provider, as sdk.ts streamFn does.
func cacheWarmingStreamFn(session func() *Session) agent.StreamFn {
	return func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		if s := session(); s != nil {
			options.OnResponse = s.extensionProviderResponseHook
			var err error
			options, err = s.buildRequestOptions(model, options)
			if err != nil {
				return nil, err
			}
			s.startCacheWarming(model, transcript, options)
			return s.modelRuntime.StreamSimple(ctx, model, ai.Context{Messages: transcript.Messages()}, options), nil
		}
		if options.MaxRetries != nil {
			ctx = ai.WithProviderMaxRetries(ctx, *options.MaxRetries)
		}
		return model.Provider.Stream(ctx, transcript, options)
	}
}

// buildRequestOptions mirrors sdk.ts:310-338. Request presence wins over settings, including explicit zero, and the Session header hook runs after provider/header assembly.
func (s *Session) buildRequestOptions(model *ai.Model, options ai.StreamOptions) (ai.StreamOptions, error) {
	settings := s.services.SettingsManager()
	retry := settings.GetProviderRetrySettings()
	if options.TimeoutMs == nil {
		timeout, err := settings.GetProviderRequestTimeoutMs()
		if err != nil {
			return options, err
		}
		// upstream: packages/coding-agent/src/core/sdk.ts:effectiveTimeoutMs
		if timeout == 0 {
			if raw := settings.Get().Retry; raw == nil || raw.Provider == nil || raw.Provider.TimeoutMs == nil {
				timeout = 2147483647
			}
		}
		options.TimeoutMs = &timeout
	}
	if options.WebSocketConnectTimeoutMs == nil {
		timeout, err := settings.GetWebSocketConnectTimeoutMs()
		if err != nil {
			return options, err
		}
		options.WebSocketConnectTimeoutMs = timeout
	}
	if options.MaxRetries == nil {
		options.MaxRetries = new(retry.MaxRetries)
	}
	if options.MaxRetryDelayMs == nil {
		options.MaxRetryDelayMs = new(retry.MaxRetryDelayMs)
	}
	options.Headers = mergeRuntimeHeaders(model.ProviderMeta.Headers, options.Headers)
	options.TransformHeaders = s.extensionProviderHeadersHook
	return options, nil
}

// streamWarmRequest replays through the same Model Runtime without restarting the Agent's cache-warming hook, as upstream CacheWarmer calls modelRuntime.streamSimple.
func (s *Session) streamWarmRequest(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return s.modelRuntime.StreamSimple(ctx, model, ai.Context{Messages: transcript.Messages()}, options), nil
}

// installCacheWarmer binds a fresh warmer to inner and retires the previous
// one.
func (s *Session) installCacheWarmer(inner *icodingagent.Session) {
	inner.SetCacheReadPriceSource(s.services.Registry().CacheReadPrice)
	s.warming.mu.Lock()
	defer s.warming.mu.Unlock()
	if s.warming.closed {
		return
	}
	if previous := s.warming.warmer; previous != nil {
		previousID := s.warming.sessionID
		previous.Close()
		s.warming.retired.Go(func() {
			previous.Wait()
			s.cleanupRetiredSessionResources(previousID)
		})
	}
	warmer := icodingagent.NewCacheWarmer(s.streamWarmRequest, inner, s.services.SettingsManager().GetCacheWarmingMode, s.decideCacheWarming)
	warmer.SetOnWarmed(s.emitEntryAppended)
	s.warming.warmer, s.warming.sessionID = warmer, inner.ID()
}

// cleanupRetiredSessionResources releases provider resources owned by a
// replaced inner Session, as upstream teardownCurrent's dispose calls
// cleanupSessionResources(sessionId) for the outgoing session. It runs after the
// retired refresh drains, so a late provider completion cannot recreate a
// resource afterwards. When the retired ID is the current Session's ID again
// (same-ID replacement or a switch back), the resources are the successor's,
// and Close owns their cleanup. The check and cleanup hold warming.mu so a
// concurrent replacement cannot adopt that ID in between.
func (s *Session) cleanupRetiredSessionResources(sessionID string) {
	s.warming.mu.Lock()
	defer s.warming.mu.Unlock()
	if sessionID == s.warming.sessionID {
		return
	}
	_ = ai.CleanupSessionResources(sessionID)
}

func (s *Session) cacheWarmer() (*icodingagent.CacheWarmer, string) {
	s.warming.mu.Lock()
	defer s.warming.mu.Unlock()
	return s.warming.warmer, s.warming.sessionID
}

// startCacheWarming restarts warming from a session request. Compaction and
// summaries use their own paths, so only session requests replace the cache
// entry.
func (s *Session) startCacheWarming(model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) {
	warmer, sessionID := s.cacheWarmer()
	if warmer == nil || options.SessionID != sessionID {
		return
	}
	request := icodingagent.CacheWarmRequest{Model: model, Context: transcript, Options: options}
	warmer.Start(request, s.cacheContextIsCurrent(model))
}

// cacheContextIsCurrent keeps warming while the current transcript still
// extends the request's prefix on the same model. Messages compare by
// identity, as upstream compares message objects; a copied message list with
// the same messages stays current.
func (s *Session) cacheContextIsCurrent(requestModel *ai.Model) func() bool {
	messages := s.agent.MessagesSnapshot()
	return func() bool {
		current := s.agent.Model()
		if current == nil || providerID(current) != providerID(requestModel) || current.ID != requestModel.ID {
			return false
		}
		currentMessages := s.agent.MessagesSnapshot()
		return len(messages) <= len(currentMessages) && slices.EqualFunc(messages, currentMessages[:len(messages)], sameAgentMessage)
	}
}

func sameAgentMessage(a, b agent.AgentMessage) bool {
	return a.System == b.System && a.User == b.User && a.Assistant == b.Assistant && a.ToolResult == b.ToolResult &&
		reflect.ValueOf(a.Custom).UnsafePointer() == reflect.ValueOf(b.Custom).UnsafePointer()
}

// decideCacheWarming awaits extension decisions before a refresh starts. The last supplied action wins; without a runner the economic decision stands.
func (s *Session) decideCacheWarming(ctx context.Context, event icodingagent.CacheWarmingDecisionEvent) (icodingagent.CacheWarmingAction, error) {
	runner := s.currentRunner()
	if runner == nil {
		return event.Action, nil
	}
	action, err := runner.EmitCacheWarmingDecision(ctx, extension.CacheWarmingDecisionEvent{
		Type: event.Type, WarmCost: event.WarmCost, MissCost: event.MissCost,
		ContinuationProbability: event.ContinuationProbability, Action: extension.CacheWarmingAction(event.Action),
	})
	return icodingagent.CacheWarmingAction(action), err
}

// emitEntryAppended reports a persisted cache-warming usage entry on the
// session event stream, as the warmer's onWarmed callback does upstream. It
// gives up as soon as ctx ends: the warmer was closed, and its Close is
// waiting for this call to return before it does. The actual send reuses
// emitOrderedEvent's already-reviewed funnel/recover, run in a background
// goroutine bounded by s.closeDone, so a full channel during a warmer
// replacement (ctx cancelled, s.closeDone not yet closed) cannot make Close
// wait on channel capacity: the goroutine keeps trying to deliver the event
// until either it succeeds or the session itself closes.
func (s *Session) emitEntryAppended(ctx context.Context, entry icodingagent.UsageEntry) {
	raw, err := json.Marshal(entry)
	if err != nil || ctx.Err() != nil {
		return
	}
	sent := make(chan struct{})
	go func() {
		s.emitOrderedEvent(agent.EntryAppendedEvent{Entry: raw})
		close(sent)
	}()
	select {
	case <-ctx.Done():
	case <-sent:
	}
}

// CacheWarmingStatus reports the current cache-warming state and the policy
// inputs that produced it, or nil when the Session has no warmer. Mirrors
// upstream AgentSession.cacheWarmingStatus.
func (s *Session) CacheWarmingStatus() *icodingagent.CacheWarmingStatus {
	warmer, _ := s.cacheWarmer()
	if warmer == nil {
		return nil
	}
	status := warmer.Status()
	return &status
}

// SetCacheWarmingMode persists the cache-warming mode and immediately
// reconciles active warming. Mirrors upstream AgentSession.setCacheWarmingMode.
func (s *Session) SetCacheWarmingMode(mode icodingagent.CacheWarmingMode) error {
	if err := s.services.SettingsManager().SetCacheWarmingMode(mode); err != nil {
		return err
	}
	if warmer, _ := s.cacheWarmer(); warmer != nil {
		warmer.OnModeChanged()
	}
	return nil
}

// OnAgentSettled tells the cache warmer that an agent run settled, the first
// step of upstream AgentSession._emitAgentSettled.
func (s *Session) OnAgentSettled() {
	if warmer, _ := s.cacheWarmer(); warmer != nil {
		warmer.OnAgentSettled()
	}
}

// closeCacheWarming cancels the current warmer and prevents further installations. Every retired warmer is already cancelled.
func (s *Session) closeCacheWarming() {
	s.warming.mu.Lock()
	s.warming.closed = true
	warmer := s.warming.warmer
	s.warming.mu.Unlock()
	if warmer != nil {
		warmer.Close()
	}
}

// waitCacheWarming drains the current and retired warmers after closeCacheWarming prevents new work.
func (s *Session) waitCacheWarming() {
	if warmer, _ := s.cacheWarmer(); warmer != nil {
		warmer.Wait()
	}
	s.warming.retired.Wait()
}
