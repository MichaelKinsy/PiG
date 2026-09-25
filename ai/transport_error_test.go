package ai

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func transportErrorTestProvider(baseURL string) Provider {
	return NewOpenAIProvider(OpenAIConfig{
		BaseURL:    baseURL,
		APIKey:     "test-key",
		Model:      "test-model",
		ProviderID: "openai",
	})
}

func transportErrorTestResult(t *testing.T, provider Provider) *AssistantMessage {
	t.Helper()
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
	stream, err := provider.Stream(context.Background(), transcript, StreamOptions{})
	if err != nil {
		return &AssistantMessage{StopReason: StopReasonError, ErrorMessage: err.Error()}
	}
	return stream.Result()
}

func assertRetryableTransportResult(t *testing.T, result *AssistantMessage, category string) {
	t.Helper()
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, category) {
		t.Fatalf("result = reason %q error %q, want transport category %q", result.StopReason, result.ErrorMessage, category)
	}
	if !IsRetryableAssistantError(*result) {
		t.Fatalf("transport result is not retryable: %q", result.ErrorMessage)
	}
}

func closeHTTPConnection(t *testing.T, writer http.ResponseWriter) (net.Conn, *bufio.ReadWriter) {
	t.Helper()
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		t.Fatal("test server does not support hijacking")
	}
	connection, buffered, err := hijacker.Hijack()
	if err != nil {
		t.Fatalf("hijack: %v", err)
	}
	return connection, buffered
}

func TestProviderTransportFailureBeforeHeadersMatchesFetchFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _ := closeHTTPConnection(t, writer)
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	assertRetryableTransportResult(t, result, "fetch failed")
}

func TestProviderTransportFailureMidStreamMatchesTerminated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, buffered := closeHTTPConnection(t, writer)
		_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 4096\r\n\r\n")
		_, _ = buffered.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		_ = buffered.Flush()
		_ = connection.Close()
	}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	assertRetryableTransportResult(t, result, "terminated")
}

func TestProviderTLSFailureMatchesFetchFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	assertRetryableTransportResult(t, result, "fetch failed")
}

func TestProviderHTTPErrorStatusIsNotTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "HTTP 400") {
		t.Fatalf("result = reason %q error %q, want HTTP 400", result.StopReason, result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, "fetch failed") || strings.Contains(result.ErrorMessage, "terminated") {
		t.Fatalf("HTTP status was mislabeled as a transport failure: %q", result.ErrorMessage)
	}
	if IsRetryableAssistantError(*result) {
		t.Fatalf("HTTP 400 was classified retryable: %q", result.ErrorMessage)
	}
}

func TestProviderRetryDelayErrorIsNotTransportFailure(t *testing.T) {
	beforeRetries, beforeDelay := ConfiguredProviderRetry()
	t.Cleanup(func() {
		if err := ConfigureProviderRetry(beforeRetries, beforeDelay); err != nil {
			t.Fatalf("restore provider retry: %v", err)
		}
	})
	if err := ConfigureProviderRetry(1, 1); err != nil {
		t.Fatalf("configure provider retry: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("retry-after", "1")
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	if !strings.Contains(result.ErrorMessage, "retry delay") || strings.Contains(result.ErrorMessage, "fetch failed") {
		t.Fatalf("provider retry policy error was mislabeled: %q", result.ErrorMessage)
	}
	if !IsRetryableAssistantError(*result) {
		t.Fatalf("provider retry delay should reach the outer retry classifier: %q", result.ErrorMessage)
	}
}

func TestMistralTransportCancellationRemainsAborted(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		close(requestStarted)
		<-request.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	provider := NewMistralProvider(MistralConfig{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"})
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
	stream, err := provider.Stream(ctx, transcript, StreamOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	<-requestStarted
	cancel()

	resultReady := make(chan *AssistantMessage, 1)
	go func() { resultReady <- stream.Result() }()
	select {
	case result := <-resultReady:
		if result.StopReason != StopReasonAborted || IsRetryableAssistantError(*result) {
			t.Fatalf("result = reason %q error %q", result.StopReason, result.ErrorMessage)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Mistral stream did not observe cancellation")
	}
}

func TestBedrockRequestTransportFailureMatchesFetchFailure(t *testing.T) {
	transportErr := &smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	mapped := mapBedrockTransportError(context.Background(), transportErr, "fetch failed")
	result := &AssistantMessage{StopReason: StopReasonError, ErrorMessage: formatBedrockError(mapped)}
	assertRetryableTransportResult(t, result, "fetch failed")
}

func TestBedrockMidStreamTransportFailureMatchesTerminated(t *testing.T) {
	events := make(chan btypes.ConverseStreamOutput)
	close(events)
	streamErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	builder := newAssistantStreamBuilder(context.Background(), APIBedrockConverseStream, "amazon-bedrock", "test-model")
	provider := &BedrockProvider{}
	go provider.parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: events, err: streamErr}, builder)

	result := builder.stream.Result()
	assertRetryableTransportResult(t, result, "terminated")
}
