package experimental

// pi: packages/coding-agent/src/experimental/client.ts

// pi: packages/coding-agent/src/experimental/client-runtime.ts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/coding-agent/src/experimental/client-runtime.ts:57-70. Selection validation runs before any directory discovery, authentication resolution or connection attempt.
func TestOpenClientRuntimeValidationPrecedesDiscovery(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		command ClientCommand
		want    string
	}{
		{"provider requires model", ClientCommand{Provider: new("")}, "Server model provider requires a model"},
		{"explicit connection model", ClientCommand{Connect: &TransportAddress{Transport: "unix", Path: "not-read"}, Model: new("")}, "Model selection is only valid when automatically activating a new server"},
	} {
		t.Run(row.name, func(t *testing.T) {
			directory := t.TempDir() + "/not-created"
			runtime, err := OpenClientRuntime(t.Context(), row.command, OpenClientRuntimeOptions{Directory: &directory})
			if runtime != nil || err == nil || err.Error() != row.want {
				t.Fatalf("open=%v, %v; want %q", runtime, err, row.want)
			}
			if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("validation touched discovery directory: %v", err)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/client.ts:9-15. The named Go variants serialize the same closed result shapes, without unrelated zero-value fields.
func TestClientResultJSONShapes(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		result     ClientResult
		kind, want string
	}{
		{ClientListResult{Sessions: []services.SessionAddress{}}, "list", `{"kind":"list","sessions":[]}`},
		{ClientAttachedResult{ServerId: "server", SessionId: ""}, "attached", `{"kind":"attached","serverId":"server","sessionId":""}`},
		{ClientPromptedResult{ServerId: "server", SessionId: "session", Text: "answer"}, "prompted", `{"kind":"prompted","serverId":"server","sessionId":"session","text":"answer"}`},
	} {
		data, err := json.Marshal(row.result)
		if err != nil || string(data) != row.want || row.result.Kind() != row.kind {
			t.Fatalf("result=%s, %v; kind=%s; want %s", data, err, row.result.Kind(), row.want)
		}
	}
}

// upstream: packages/coding-agent/src/experimental/client.ts:76-82. The prompt's text is the answer waitForPrompt settles with; a rejection or an unanswered prompt is an error with the reason.
func TestPromptClientSessionWaitsForTheAnswer(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name     string
		prompt   services.AgentOperationResponse
		result   services.AgentPromptResult
		wantText string
		wantErr  string
	}{
		{"answered", services.AgentOperationResponse{Accepted: true, OperationID: new("7")}, services.AgentPromptResult{Status: "done", Text: new("answer")}, "answer", ""},
		{"empty answer", services.AgentOperationResponse{Accepted: true, OperationID: new("7")}, services.AgentPromptResult{Status: "done", Text: new("")}, "", ""},
		{"rejected", services.AgentOperationResponse{Error: &services.AgentOperationError{Code: "busy", Message: "Conversation 1 is busy"}}, services.AgentPromptResult{}, "", "Conversation 1 is busy"},
		{"unanswered", services.AgentOperationResponse{Accepted: true, OperationID: new("7")}, services.AgentPromptResult{Status: "unanswered", Reason: new("aborted")}, "", "Prompt was not answered: aborted"},
	} {
		t.Run(row.name, func(t *testing.T) {
			controller := &promptControllerStub{prompt: row.prompt, result: row.result}
			text, err := promptClientSession(&ActivatedClientRuntimeServer{Agent: controller}, "question")
			if row.wantErr != "" {
				if err == nil || err.Error() != row.wantErr || text != "" {
					t.Fatalf("prompt = %q, %v; want error %q", text, err, row.wantErr)
				}
			} else if err != nil || text != row.wantText {
				t.Fatalf("prompt = %q, %v; want %q", text, err, row.wantText)
			}
			if row.prompt.Accepted != (controller.waited != "") || (row.prompt.Accepted && controller.waited != "7") {
				t.Fatalf("waited for %q after %+v", controller.waited, row.prompt)
			}
		})
	}
}

type promptControllerStub struct {
	services.AgentController
	prompt services.AgentOperationResponse
	result services.AgentPromptResult
	waited string
}

func (stub *promptControllerStub) Prompt(_ context.Context, request services.AgentPromptRequest) (services.AgentOperationResponse, error) {
	if request.Message != "question" || request.Images != nil {
		return services.AgentOperationResponse{}, errors.New("unexpected prompt request")
	}
	return stub.prompt, nil
}

func (stub *promptControllerStub) WaitForPrompt(_ context.Context, operationID string) (services.AgentPromptResult, error) {
	stub.waited = operationID
	return stub.result, nil
}

// upstream: packages/coding-agent/src/experimental/client-runtime.ts:106-127. The first disposal settles same-phase resources concurrently in source order; the disposed flag makes subsequent or reentrant calls return immediately.
func TestClientRuntimeDisposalJoinsSourcesAndRetainsErrors(t *testing.T) {
	t.Parallel()
	started := make(chan int, 2)
	release := make(chan struct{})
	failures := []error{errors.New("first source"), errors.New("second source")}
	runtime := &ClientRuntime{}
	for index, failure := range failures {
		runtime.sources = append(runtime.sources, func(ctx context.Context) error {
			if ctx != context.Background() {
				t.Error("cleanup Context changed")
			}
			started <- index
			<-release
			return failure
		})
	}
	closed := make(chan error, 1)
	finished := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	go func() { defer close(finished); closed <- runtime.Dispose() }()
	t.Cleanup(func() { releaseOnce(); <-finished })
	<-started
	<-started
	if err := runtime.Dispose(); err != nil {
		t.Fatalf("repeated disposal=%v, want nil", err)
	}
	releaseOnce()
	first := <-closed
	aggregate, ok := errors.AsType[*services.AggregateError](first)
	if !ok || aggregate.Message != "Failed to dispose experimental client runtime" || !reflect.DeepEqual(aggregate.Errors, []any{failures[0], failures[1]}) {
		t.Fatalf("cleanup=%#v", first)
	}
}
