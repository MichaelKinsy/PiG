package ai

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestProviderTransportFailureBeforeHeadersMatchesConnectionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _ := closeHTTPConnection(t, writer)
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	// The OpenAI SDK rejects a pre-header fetch failure with APIConnectionError("Connection error.").
	assertRetryableTransportResult(t, result, "Connection error.")
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

func TestProviderTLSFailureMatchesConnectionError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	assertRetryableTransportResult(t, result, "Connection error.")
}

func TestProviderHTTPErrorStatusIsNotTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer server.Close()

	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	// Pi's OpenAI SDK message is normalized with the parsed error object, not a Go HTTP prefix.
	if result.StopReason != StopReasonError || result.ErrorMessage != `400: {"message":"invalid request"}` {
		t.Fatalf("result = reason %q error %q, want normalized provider error 400", result.StopReason, result.ErrorMessage)
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
