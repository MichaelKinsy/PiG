package coding

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// Pi 0.87.1 agent-session.ts:2087-2092,3559-3563: the unawaited name emission
// releases its caller when a handler awaits waitForIdle, not when the run ends.
func TestSessionNameHandlerWaitForIdleReleasesNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s *Session
		var mu sync.Mutex
		var trace []string
		record := func(value string) {
			mu.Lock()
			defer mu.Unlock()
			trace = append(trace, value)
		}
		handlerDone := make(chan error, 1)
		providerEntered, releaseProvider := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(releaseProvider) })
		h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_info_changed": {func(args ...any) (any, error) {
				if args[0].(extension.SessionInfoChangedEvent).Name == "first" {
					record("first-prefix")
					err := s.WaitForIdle(args[1].(context.Context))
					if err == nil {
						record("first-done")
					}
					handlerDone <- err
					return nil, err
				}
				record("second")
				return nil, nil
			}},
		}}}, func(messages []ai.Message) *ai.AssistantMessage {
			close(providerEntered)
			<-releaseProvider
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		})
		s = h.session
		// Registered after the harness so even a failed assertion releases the Provider before Close.
		t.Cleanup(release)
		promptDone := make(chan error, 1)
		go func() { _, err := s.Prompt(t.Context(), "hello"); promptDone <- err }()
		synctest.Wait()
		select {
		case <-providerEntered:
		default:
			t.Fatal("prompt did not reach held Provider")
		}
		firstSet, secondSet := make(chan error, 1), make(chan error, 1)
		go func() { firstSet <- s.SetSessionName("first") }()
		synctest.Wait()
		select {
		case err := <-firstSet:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Error("first setter waited for idle handler completion")
		}
		go func() { secondSet <- s.SetSessionName("second") }()
		synctest.Wait()
		select {
		case err := <-secondSet:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Error("second setter blocked behind idle handler")
		}
		mu.Lock()
		if !reflect.DeepEqual(trace, []string{"first-prefix", "second"}) {
			t.Errorf("trace while Provider is held = %q, want [first-prefix second]", trace)
		}
		mu.Unlock()
		select {
		case err := <-handlerDone:
			t.Fatalf("idle handler completed before Provider release: %v", err)
		default:
		}
		release()
		synctest.Wait()
		// Assert before teardown: cancellation must not masquerade as successful settlement.
		select {
		case err := <-promptDone:
			if err != nil {
				t.Errorf("prompt: %v", err)
			}
		default:
			t.Error("prompt could not settle after Provider release")
		}
		select {
		case err := <-handlerDone:
			if err != nil {
				t.Errorf("idle handler: %v", err)
			}
		default:
			t.Error("idle handler did not complete after settlement")
		}
		mu.Lock()
		if !reflect.DeepEqual(trace, []string{"first-prefix", "second", "first-done"}) {
			t.Errorf("settled trace = %q", trace)
		}
		mu.Unlock()
		// Avoid taking the Session lock in a known-deadlocked pre-fix run.
		if !t.Failed() {
			if text := s.LastAssistantText(); text == nil || *text != "done" {
				t.Errorf("assistant result = %v, want done", text)
			}
			if got := h.provider.callCount(); got != 1 {
				t.Errorf("Provider calls = %d, want one prompt", got)
			}
		}
	})
}

func TestWaitForIdleAcknowledgesOnlyPendingWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newRecoveryHarness(t, harnessOptions{}).session
		invoked := make(chan struct{})
		ctx, cancel := context.WithCancel(invocation.WithAcknowledgment(t.Context(), func() { close(invoked) }))
		defer cancel()
		if err := s.WaitForIdle(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case <-invoked:
			t.Fatal("idle fast path acknowledged without a pending wait")
		default:
		}
		_, end := s.beginAgentRun(t.Context())
		defer end()
		done := make(chan error, 1)
		go func() { done <- s.WaitForIdle(ctx) }()
		synctest.Wait()
		select {
		case <-invoked:
		default:
			t.Error("pending idle wait did not acknowledge its suspension")
		}
		select {
		case err := <-done:
			t.Fatalf("pending wait completed before cancellation: %v", err)
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("wait error = %v, want context.Canceled", err)
			}
		default:
			t.Error("cancelled idle wait did not return")
		}
	})
}

func BenchmarkSessionWaitForIdleSuspension(b *testing.B) {
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	s, err := NewSession(services, SessionOptions{NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Error(err)
		}
	})
	b.ReportAllocs()
	for b.Loop() {
		_, end := s.beginAgentRun(b.Context())
		invoked := make(chan struct{})
		ctx := invocation.WithAcknowledgment(b.Context(), func() { close(invoked) })
		done := make(chan error, 1)
		go func() { done <- s.WaitForIdle(ctx) }()
		<-invoked
		end()
		if err := <-done; err != nil {
			b.Fatal(err)
		}
	}
}
