package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

const bedrockFailureRequestID = "11111111-2222-3333-4444-555555555555"
const bedrockValidationMessage = "The provided model identifier is invalid."

func bedrockHTTPFailure(t *testing.T, ctx context.Context, status int, code, requestID string) *AssistantMessage {
	t.Helper()
	isolateBedrockConfig(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-Errortype", code)
		w.Header().Set("X-Amzn-Requestid", requestID)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"message":"The provided model identifier is invalid."}`)
	}))
	defer server.Close()
	provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL)
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
	if err != nil {
		t.Fatalf("expected terminal provider error event, got immediate %T", err)
	}
	return stream.Result()
}

func bedrockIteratorFailure(t *testing.T, err error, requestID string) *AssistantMessage {
	t.Helper()
	events := make(chan btypes.ConverseStreamOutput, 1)
	events <- &btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}}
	close(events)
	builder := newAssistantStreamBuilder(t.Context(), APIBedrockConverseStream, "amazon-bedrock", "us.anthropic.claude-opus-4-8")
	go (&BedrockProvider{}).parseBedrockEvents(t.Context(), &fakeBedrockEventStream{events: events, err: err}, builder, requestID)
	return builder.stream.Result()
}

func bedrockFailureDiagnostic(message *AssistantMessage) *AssistantMessageDiagnostic {
	for index := range message.Diagnostics {
		if message.Diagnostics[index].Type == "bedrock_response_failure" {
			return &message.Diagnostics[index]
		}
	}
	return nil
}

func assertBedrockFailureDetails(t *testing.T, message *AssistantMessage, want string) {
	t.Helper()
	if message.StopReason != StopReasonError {
		t.Fatalf("stop=%s error=%q", message.StopReason, message.ErrorMessage)
	}
	diagnostic := bedrockFailureDiagnostic(message)
	if diagnostic == nil {
		t.Fatal("missing bedrock_response_failure diagnostic")
	}
	raw, err := json.Marshal(diagnostic.Details)
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, raw, want)
}

func TestBedrockUpstreamErrorMetadata(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:103
	t.Run("records status, error code and request id for a non-2xx from client.send()", func(t *testing.T) {
		message := bedrockHTTPFailure(t, t.Context(), 400, "ValidationException", bedrockFailureRequestID)
		assertBedrockFailureDetails(t, message, `{"status":400,"errorCode":"ValidationException","requestId":"`+bedrockFailureRequestID+`"}`)
		raw, err := json.Marshal(bedrockFailureDiagnostic(message))
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		if len(object) != 3 || object["details"] == nil || object["timestamp"] == nil || object["type"] == nil {
			t.Fatalf("diagnostic keys=%s", raw)
		}
		if _, present := object["error"]; present {
			t.Fatalf("diagnostic includes error: %s", raw)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:120
	t.Run("leaves errorMessage untouched so retry classification is unaffected", func(t *testing.T) {
		message := bedrockHTTPFailure(t, t.Context(), 400, "ValidationException", bedrockFailureRequestID)
		if message.ErrorMessage != "Validation error: "+bedrockValidationMessage {
			t.Fatalf("errorMessage=%q", message.ErrorMessage)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:132
	t.Run("reports only the request id for a modeled mid-stream exception", func(t *testing.T) {
		message := bedrockIteratorFailure(t, errors.New("Too many requests, please wait."), bedrockFailureRequestID)
		assertBedrockFailureDetails(t, message, `{"requestId":"`+bedrockFailureRequestID+`"}`)
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:142
	t.Run("captures the error code for an unmodeled mid-stream error", func(t *testing.T) {
		message := bedrockIteratorFailure(t, &smithy.GenericAPIError{Code: "ModelStreamErrorException", Message: "Model stream terminated unexpectedly."}, bedrockFailureRequestID)
		assertBedrockFailureDetails(t, message, `{"errorCode":"ModelStreamErrorException","requestId":"`+bedrockFailureRequestID+`"}`)
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:154
	t.Run("does not report a transport failure name as a provider error code", func(t *testing.T) {
		message := bedrockIteratorFailure(t, &smithy.GenericAPIError{Code: "TimeoutError", Message: "Connection timed out after 1000 ms"}, bedrockFailureRequestID)
		assertBedrockFailureDetails(t, message, `{"requestId":"`+bedrockFailureRequestID+`"}`)
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:163
	t.Run("emits no diagnostic when the failure carries no provider metadata", func(t *testing.T) {
		isolateBedrockConfig(t)
		provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", "https://bedrock-runtime.us-east-1.amazonaws.com")
		provider.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
			return nil, errors.New("socket hang up")
		}
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{CacheRetention: CacheRetentionNone})
		if err != nil {
			t.Fatal(err)
		}
		message := stream.Result()
		if message.StopReason != StopReasonError || message.ErrorMessage != "socket hang up" || bedrockFailureDiagnostic(message) != nil {
			t.Fatalf("message=%+v", message)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:173
	t.Run("emits no diagnostic for an aborted turn", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		message := bedrockHTTPFailure(t, ctx, 400, "ValidationException", bedrockFailureRequestID)
		if message.StopReason != StopReasonAborted || bedrockFailureDiagnostic(message) != nil {
			t.Fatalf("message=%+v", message)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:189
	t.Run("drops header-derived values that exceed the length bound", func(t *testing.T) {
		message := bedrockHTTPFailure(t, t.Context(), 400, strings.Repeat("E", 5000)+"Exception", strings.Repeat("R", 5000))
		assertBedrockFailureDetails(t, message, `{"status":400}`)
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-error-metadata.test.ts:200
	t.Run("omits the SDK's Unknown placeholder instead of reporting it as a code", func(t *testing.T) {
		message := bedrockHTTPFailure(t, t.Context(), 403, "Unknown", bedrockFailureRequestID)
		assertBedrockFailureDetails(t, message, `{"status":403,"requestId":"`+bedrockFailureRequestID+`"}`)
	})
}
