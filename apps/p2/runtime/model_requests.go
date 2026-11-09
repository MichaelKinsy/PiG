package runtime

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/src/runtime/worker.ts (ModelRequest, HarnessParts.requestOf and `remembering`, Pi c9552c7d): the Harness's
// Models remembers each generation request, so that a GenerationTask's `afterResponse` hook can find the request that
// produced its message.
//
// Pi keys the request by the message object `streamSimple().result()` resolved with (a WeakMap): JavaScript hands the same
// object to the hook. Durable's Go hook gets the message by value, a copy of it, so the key is the conversation's provider
// session ID (the `pi.provider` document, the SessionID of every request the Harness sends for the conversation), and the
// remembered response must be the hook's message (sameResponse). Equivalence with the WeakMap: a conversation has at most
// one generation request in flight (one run, one attempt at a time), so the newest request under its session ID is the one
// whose response a hook sees, except for a response fetched after a deferred request, which no request produced (Pi's
// WeakMap has no entry for it either: sameResponse refuses it); a request nobody takes is replaced by the next one under
// its session ID, where Pi's entry goes with its message.

// ModelRequest is one provider request the Harness sent: the model, the messages after Durable's `beforeRequest` chain, and
// the attempt's options without its Signal, Telemetry and OnPayload. Read-only: the messages are Durable's entry messages;
// never mutate any of it. Sending it again skips Durable's `beforeProviderRequest` hooks (no p2 extension has one).
type ModelRequest struct {
	Model    *ai.Model
	Messages []ai.Message
	Options  ai.StreamOptions
}

// loggedRequest is a request and, once its stream ended, the message it ended with.
type loggedRequest struct {
	request ModelRequest
	// settled is closed once result is set (or the request's context ended: result stays nil).
	settled chan struct{}
	result  *ai.AssistantMessage
}

// requestLog is the Harness's remembered requests, by provider session ID.
type requestLog struct {
	mu        sync.Mutex
	bySession map[string]*loggedRequest
}

func newRequestLog() *requestLog { return &requestLog{bySession: map[string]*loggedRequest{}} }

// rememberingModels is Models whose StreamSimple remembers each request (`remembering`). CompleteSimple (compaction, the
// cache refreshes) is not remembered.
type rememberingModels struct {
	durable.Models
	log *requestLog
}

func (log *requestLog) remembering(models durable.Models) durable.Models {
	return &rememberingModels{Models: models, log: log}
}

func (models *rememberingModels) StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessageEventStream {
	stream := models.Models.StreamSimple(ctx, model, request, options...)
	var attempt ai.StreamOptions
	if len(options) > 0 {
		attempt = options[0]
	}
	// Every request of the Harness has a session ID; without one there is nothing to find it by.
	if attempt.SessionID == "" {
		return stream
	}
	attempt.Signal, attempt.Telemetry, attempt.OnPayload = nil, nil, nil
	entry := &loggedRequest{request: ModelRequest{Model: model, Messages: request.Messages, Options: attempt}, settled: make(chan struct{})}
	models.log.mu.Lock()
	models.log.bySession[attempt.SessionID] = entry
	models.log.mu.Unlock()
	go func() {
		defer close(entry.settled)
		// A request whose context ended settles without a result, as the Harness stops waiting for it.
		if result, err := stream.ResultContext(ctx); err == nil {
			entry.result = result
		}
	}()
	return stream
}

// requestOf takes the request that produced response, the message an `afterResponse` hook of api's conversation got; each
// response is taken once. nil when none produced it.
func (log *requestLog) requestOf(ctx context.Context, api harness.HookApi, response ai.AssistantMessage) (*ModelRequest, error) {
	provider, err := durable.Snapshot[harness.ProviderState](ctx, api, harness.ProviderDoc, api.ConversationId())
	if err != nil || provider == nil {
		return nil, err
	}
	log.mu.Lock()
	entry := log.bySession[provider.SessionId]
	log.mu.Unlock()
	if entry == nil {
		return nil, nil
	}
	// The stream the hook's message came from ended before the hook ran; the goroutine recording its result follows.
	select {
	case <-entry.settled:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if entry.result == nil || !sameResponse(entry.result, response) {
		return nil, nil
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.bySession[provider.SessionId] != entry {
		return nil, nil
	}
	delete(log.bySession, provider.SessionId)
	return &entry.request, nil
}

// sameResponse is whether message is the copy of result the Harness's hook got (it is a copy of the stream's message, which
// shares what a copy shares). Every response a stream measured carries its own DurationMs, which the copy shares; a message
// without one is told by what the provider reported.
func sameResponse(result *ai.AssistantMessage, message ai.AssistantMessage) bool {
	if result.DurationMs != nil && message.DurationMs != nil {
		return result.DurationMs == message.DurationMs
	}
	return result.DurationMs == nil && message.DurationMs == nil &&
		result.Timestamp == message.Timestamp && result.ResponseID == message.ResponseID && result.StopReason == message.StopReason &&
		result.Provider == message.Provider && result.Model == message.Model && result.Usage == message.Usage
}
