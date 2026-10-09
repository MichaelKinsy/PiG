package services

// pi: packages/coding-agent/src/experimental/services/agent-controller.ts

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// This transport controls the producer boundary, not the Go scheduler. Loopback supplies real catalogue/subscription behavior but does not gain an inferred BeginInvoke capability.
type controllerAdmissionTransport struct {
	chord.RemoteServiceTransport
	calls          []chord.ServiceCall
	contexts       []context.Context
	result         json.RawMessage
	failure        error
	admissionError error
	settled        <-chan struct{}
}

func (transport *controllerAdmissionTransport) BeginInvoke(ctx context.Context, call chord.ServiceCall) (*chord.ServiceInvocation, error) {
	if transport.admissionError != nil {
		return nil, transport.admissionError
	}
	transport.calls = append(transport.calls, call)
	transport.contexts = append(transport.contexts, ctx)
	result, failure, settled := transport.result, transport.failure, transport.settled
	return chord.NewServiceInvocation(func(waitCtx context.Context) (json.RawMessage, error) {
		if settled != nil {
			select {
			case <-settled:
			case <-waitCtx.Done():
				return nil, context.Cause(waitCtx)
			}
		}
		return result, failure
	}), nil
}

func newControllerAdmission(t *testing.T) (AgentController, *controllerAdmissionTransport) {
	t.Helper()
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(AgentControllerDefinition))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	if err := chord.Provide[AgentController](provider, AgentControllerDefinition, CreateAgentController(nil, nil)); err != nil {
		t.Fatal(err)
	}
	transport := &controllerAdmissionTransport{RemoteServiceTransport: chord.NewLoopbackTransport(provider)}
	binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: chord.ServiceIDs(AgentControllerID), Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	controller, err := chord.UseRemoteClient(binding, AgentControllerDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	return controller, transport
}

func waitControllerResult[T any](ctx context.Context, operation *chord.ServiceResultInvocation[T], err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return operation.Wait(ctx)
}

// upstream: packages/coding-agent/src/experimental/services/agent-controller.ts:38-55. Every Promise-bearing method keeps its wire name/argument list; the native begin/wait split is not a second published service.
func TestAgentControllerInitiationPreservesWireMethodsAndValues(t *testing.T) {
	t.Parallel()
	controller, transport := newControllerAdmission(t)
	initiator := controller.(AgentControllerInitiator)
	prompt := AgentPromptRequest{Message: "hello", Images: []AgentPromptImage{}}
	operationResponse := AgentOperationResponse{Accepted: true, OperationID: new("op")}
	queueResponse := AgentQueueResponse{Accepted: true, EntryID: new("entry")}
	for _, row := range []struct {
		name     string
		args     string
		response string
		want     any
		run      func(context.Context) (any, error)
	}{
		{"prompt", `[{"message":"hello","images":[]}]`, `{"accepted":true,"operationId":"op","error":null}`, operationResponse, func(ctx context.Context) (any, error) {
			op, err := initiator.BeginPrompt(ctx, prompt)
			return waitControllerResult(ctx, op, err)
		}},
		{"abort", `[]`, "", json.RawMessage(nil), func(ctx context.Context) (any, error) {
			op, err := initiator.BeginAbort(ctx)
			if err != nil {
				return nil, err
			}
			return op.Wait(ctx)
		}},
		{"steer", `[{"message":"hello","images":[]}]`, `{"accepted":true,"entryId":"entry","error":null}`, queueResponse, func(ctx context.Context) (any, error) {
			op, err := initiator.BeginSteer(ctx, prompt)
			return waitControllerResult(ctx, op, err)
		}},
		{"followUp", `[{"message":"hello","images":[]}]`, `{"accepted":true,"entryId":"entry","error":null}`, queueResponse, func(ctx context.Context) (any, error) {
			op, err := initiator.BeginFollowUp(ctx, prompt)
			return waitControllerResult(ctx, op, err)
		}},
		{"cancelQueued", `[""]`, `{"outcome":"already_consumed"}`, AgentCancelQueuedResponse{Outcome: "already_consumed"}, func(ctx context.Context) (any, error) {
			op, err := initiator.BeginCancelQueued(ctx, "")
			return waitControllerResult(ctx, op, err)
		}},
		{"compact", `[{"customInstructions":""}]`, `{"accepted":true,"operationId":"op","error":null}`, operationResponse, func(ctx context.Context) (any, error) {
			op, err := initiator.BeginCompact(ctx, AgentCompactionRequest{CustomInstructions: new("")})
			return waitControllerResult(ctx, op, err)
		}},
		{"waitForPrompt", `["op"]`, `{"status":"done","text":"answer","reason":null}`, AgentPromptResult{Status: "done", Text: new("answer")}, func(ctx context.Context) (any, error) {
			op, err := initiator.BeginWaitForPrompt(ctx, "op")
			return waitControllerResult(ctx, op, err)
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			transport.result = nil
			if row.response != "" {
				transport.result = json.RawMessage(row.response)
			}
			before := len(transport.calls)
			got, err := row.run(context.Background())
			if err != nil || !reflect.DeepEqual(got, row.want) {
				t.Fatalf("result=%#v err=%v, want %#v", got, err, row.want)
			}
			if len(transport.calls) != before+1 {
				t.Fatalf("admissions=%d, want %d", len(transport.calls), before+1)
			}
			call := transport.calls[before]
			args, err := json.Marshal(call.Args)
			if err != nil {
				t.Fatal(err)
			}
			if call.ServiceId != AgentControllerID || call.Member != row.name || call.Instance != nil || string(args) != row.args || transport.contexts[before] != context.Background() {
				t.Fatalf("call=%+v args=%s ctx=%v", call, args, transport.contexts[before])
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/client-tui.ts:522-571 and services/agent-controller.ts:39. Cancelling presentation observation must not replace BACKGROUND_CONTEXT or wait for its operation to end.
func TestAgentControllerInitiationSeparatesAdmissionCompletionAndObservation(t *testing.T) {
	t.Parallel()
	controller, transport := newControllerAdmission(t)
	initiator := controller.(AgentControllerInitiator)
	settled := make(chan struct{})
	transport.settled = settled
	transport.result = json.RawMessage(`{"accepted":true,"operationId":"later","error":null}`)
	op, err := initiator.BeginPrompt(context.Background(), AgentPromptRequest{Message: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.calls) != 1 || transport.contexts[0] != context.Background() {
		t.Fatalf("operation not admitted with original Context: %+v", transport.calls)
	}
	waitCtx, cancel := context.WithCancelCause(t.Context())
	observerClosed := errors.New("presentation closed")
	cancel(observerClosed)
	if _, err := op.Wait(waitCtx); !errors.Is(err, observerClosed) {
		t.Fatalf("wait error=%v, want %v", err, observerClosed)
	}
	if err := transport.contexts[0].Err(); err != nil {
		t.Fatalf("producer cancelled: %v", err)
	}
	close(settled)
	response, err := op.Wait(t.Context())
	if err != nil || !response.Accepted || response.OperationID == nil || *response.OperationID != "later" {
		t.Fatalf("late response=%+v, %v", response, err)
	}
	transport.admissionError = errors.New("send rejected")
	if op, err := initiator.BeginCompact(t.Context(), AgentCompactionRequest{}); op != nil || !errors.Is(err, transport.admissionError) {
		t.Fatalf("admission=%v, %v", op, err)
	}
	transport.admissionError = nil
	transport.failure = errors.New("remote response rejected")
	op, err = initiator.BeginCompact(t.Context(), AgentCompactionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := op.Wait(t.Context()); !errors.Is(err, transport.failure) {
		t.Fatalf("completion error=%v", err)
	}
	transport.failure = nil
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"accepted":`)} {
		transport.result = raw
		op, err := initiator.BeginCompact(t.Context(), AgentCompactionRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := op.Wait(t.Context()); err == nil {
			t.Fatalf("invalid result accepted: %s", raw)
		}
	}
}

// upstream: packages/chord/src/facets/host.ts:423-508,574 and packages/coding-agent/src/experimental/client-tui.ts:564-571. AgentController is acquired through the selected FacetHost graph and retains replacement/revocation guards.
func TestAgentControllerInitiationUsesSelectedFacetAndRetainedView(t *testing.T) {
	t.Parallel()
	first, firstTransport := newControllerAdmission(t)
	second, secondTransport := newControllerAdmission(t)
	firstTransport.result = json.RawMessage(`{"accepted":true,"operationId":"first","error":null}`)
	secondTransport.result = json.RawMessage(`{"accepted":true,"operationId":"second","error":null}`)
	provider := func(controller AgentController) chord.Facet {
		return chord.Facet{Id: "selected-controller", Setup: func(env *chord.FacetEnvironment) error {
			return chord.ProvideService(env, AgentControllerDefinition, controller)
		}}
	}
	var ref *chord.ServiceRef[AgentController]
	consumer := chord.Facet{Id: "presentation", Setup: func(env *chord.FacetEnvironment) error {
		var err error
		ref, err = chord.UseService(env, AgentControllerDefinition)
		return err
	}}
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{consumer, provider(first)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	selected, err := ref.Get()
	if err != nil {
		t.Fatal(err)
	}
	initiator, ok := selected.(AgentControllerInitiator)
	if !ok {
		t.Fatal("selected service view lost invocation admission")
	}
	subscription, err := host.Services().Subscribe(AgentControllerID, chord.ServiceSingleton, func(context.Context, chord.ServiceProviderUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := subscription.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	var methods []string
	for _, member := range subscription.Snapshot().Instances[0].Members {
		if member.Kind != chord.MemberMethod {
			t.Fatalf("unexpected controller state: %+v", member)
		}
		methods = append(methods, member.Name)
	}
	// The exact seven declarations in agent-controller.ts:38-55 are the denominator, not the concrete Go method set.
	wantMethods := []string{"abort", "cancelQueued", "compact", "followUp", "prompt", "steer", "waitForPrompt"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("wire methods=%v, want %v", methods, wantMethods)
	}
	begin := initiator.BeginPrompt
	for _, want := range []string{"first", "second"} {
		if want == "second" {
			if err := host.Reload(t.Context(), []chord.Facet{provider(second)}); err != nil {
				t.Fatal(err)
			}
		}
		op, err := begin(context.Background(), AgentPromptRequest{})
		if err != nil {
			t.Fatal(err)
		}
		response, err := op.Wait(t.Context())
		if err != nil || response.OperationID == nil || *response.OperationID != want {
			t.Fatalf("selected response=%+v, %v; want %s", response, err, want)
		}
	}
	if len(firstTransport.calls) != 1 || len(secondTransport.calls) != 1 {
		t.Fatalf("admission bypassed replacement: %d/%d", len(firstTransport.calls), len(secondTransport.calls))
	}
	if err := host.Reload(t.Context(), []chord.Facet{provider(blockingController{})}); err != nil {
		t.Fatal(err)
	}
	if op, err := begin(t.Context(), AgentPromptRequest{}); op != nil || err == nil || err.Error() != "Selected AgentController does not expose invocation admission" {
		t.Fatalf("blocking override received fabricated admission: %v, %v", op, err)
	}
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if op, err := begin(t.Context(), AgentPromptRequest{}); op != nil || err == nil {
		t.Fatalf("revoked view admitted operation: %v, %v", op, err)
	}
}

// blockingController implements the controller contract with blocking methods only: it exposes no invocation admission.
type blockingController struct{}

func (blockingController) Prompt(context.Context, AgentPromptRequest) (AgentOperationResponse, error) {
	return AgentOperationResponse{}, nil
}
func (blockingController) Steer(context.Context, AgentPromptRequest) (AgentQueueResponse, error) {
	return AgentQueueResponse{}, nil
}
func (blockingController) FollowUp(context.Context, AgentPromptRequest) (AgentQueueResponse, error) {
	return AgentQueueResponse{}, nil
}
func (blockingController) CancelQueued(context.Context, string) (AgentCancelQueuedResponse, error) {
	return AgentCancelQueuedResponse{}, nil
}
func (blockingController) Abort(context.Context) error { return nil }
func (blockingController) Compact(context.Context, AgentCompactionRequest) (AgentOperationResponse, error) {
	return AgentOperationResponse{}, nil
}
func (blockingController) WaitForPrompt(context.Context, string) (AgentPromptResult, error) {
	return AgentPromptResult{}, nil
}
