package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// compat.ts complete / completeSimple await stream(model, context, options).result(); options.signal reaches the provider, and the awaited result is the
// provider's own message when the signal aborts the request (here an "aborted" message), never a hang.
func TestCompleteHandsTheCallersCancellationToTheProviderAndReturnsItsAbortedMessage(t *testing.T) {
	for _, simple := range []bool{false, true} {
		name := map[bool]string{false: "complete", true: "completeSimple"}[simple]
		t.Run(name, func(t *testing.T) {
			resetCompatRegistry(t)
			entered := make(chan struct{})
			provider := func(ctx context.Context, model *Model, _ TranscriptContext, _ StreamOptions) (*AssistantMessageEventStream, error) {
				stream := NewAssistantMessageEventStream()
				go func() {
					close(entered)
					message := &AssistantMessage{API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, Model: model.ID, StopReason: StopReasonAborted}
					select {
					case <-ctx.Done():
					case <-time.After(10 * time.Second):
						message.StopReason, message.ErrorMessage = StopReasonError, "the caller's cancellation never reached the provider"
					}
					_ = stream.Push(ErrorEvent{Reason: message.StopReason, Error: message})
					stream.End(message)
				}()
				return stream, nil
			}
			RegisterAPIProvider(APIProvider{API: APIOpenAIResponses, Stream: provider, StreamSimple: provider})
			complete := Complete
			if simple {
				complete = CompleteSimple
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type outcome struct {
				message *AssistantMessage
				err     error
			}
			done := make(chan outcome, 1)
			go func() {
				message, err := complete(ctx, compatTestModel("p", APIOpenAIResponses), compatHello)
				done <- outcome{message, err}
			}()
			<-entered
			cancel()
			got := <-done
			if got.err != nil || got.message.StopReason != StopReasonAborted {
				t.Fatalf("message %+v, error %v; want the provider's aborted message", got.message, got.err)
			}
		})
	}
}

// radius-config.ts loadRadiusGatewayConfig passes the signal to fetch: a cancellation while the gateway has not answered rejects the call with the
// cancellation and releases the request.
func TestLoadRadiusGatewayConfigIsCancelledWhileTheGatewayHasNotAnswered(t *testing.T) {
	entered := make(chan struct{})
	released := make(chan struct{})
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
		close(released)
	}))
	defer server.Close()
	defer close(stop)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := LoadRadiusGatewayConfig(ctx, server.URL, "key"); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil || ctx.Err() == nil {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the cancellation did not end the request")
	}
	<-released
}
