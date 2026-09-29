package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
)

type closeProbe struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (probe *closeProbe) Close() error {
	probe.once.Do(func() { close(probe.closed) })
	return nil
}

func (probe *closeProbe) isClosed() bool {
	select {
	case <-probe.closed:
		return true
	default:
		return false
	}
}

// smithy-go closes the request body as soon as Do returns (transport/http/client.go:112-117). Closing it before the transport finished writing fails the write and the transport then closes the connection, which ended an in-flight Bedrock stream whenever the response beat the write's bookkeeping. The body must outlive the write.
func TestBedrockRequestBodyClosesAfterTheWrite(t *testing.T) {
	t.Run("write completes", func(t *testing.T) {
		inner := &closeProbe{Reader: strings.NewReader("{}"), closed: make(chan struct{})}
		wrote, done := make(chan struct{}), make(chan struct{})
		body := &bedrockRequestBody{ReadCloser: inner, wrote: wrote, done: done}
		if err := body.Close(); err != nil {
			t.Fatal(err)
		}
		if inner.isClosed() {
			t.Fatal("closed before the transport finished writing")
		}
		if _, err := io.ReadAll(body); err != nil {
			t.Fatalf("a read after Close must still work while the write is pending: %v", err)
		}
		close(wrote)
		<-inner.closed
	})
	t.Run("write already completed", func(t *testing.T) {
		inner := &closeProbe{Reader: strings.NewReader("{}"), closed: make(chan struct{})}
		wrote := make(chan struct{})
		close(wrote)
		body := &bedrockRequestBody{ReadCloser: inner, wrote: wrote, done: make(chan struct{})}
		_ = body.Close()
		if !inner.isClosed() {
			t.Fatal("a finished write must close the body at once")
		}
	})
	t.Run("request canceled before the write", func(t *testing.T) {
		inner := &closeProbe{Reader: strings.NewReader("{}"), closed: make(chan struct{})}
		done := make(chan struct{})
		body := &bedrockRequestBody{ReadCloser: inner, wrote: make(chan struct{}), done: done}
		_ = body.Close()
		_ = body.Close()
		close(done)
		<-inner.closed
	})
}

// A transport that fails before it writes (a refused dial, a proxy error) never reports WroteRequest. net/http's Client closes the request body on that error, and the deferred close must still release it when the request context can never be canceled.
func TestBedrockRequestBodyClosesWhenTheTransportFailsBeforeTheWrite(t *testing.T) {
	inner := &closeProbe{Reader: strings.NewReader("{}"), closed: make(chan struct{})}
	// http.RoundTripper: RoundTrip must always close the body, including on errors; net/http's Transport does so before any write when the dial fails.
	tap := &bedrockBodyTap{inner: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		_ = request.Body.Close()
		return nil, errors.New("dial refused")
	})}}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://127.0.0.1:1/model/probe/converse-stream", inner)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tap.Do(request)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("the failing transport returned no error")
	}
	for range 1_000_000 {
		if inner.isClosed() {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("the request body outlived a transport failure that never wrote it")
}

func bedrockErrorServer(t *testing.T, status int, errorType, message string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-Errortype", errorType)
		w.Header().Set("X-Amzn-Requestid", "req-error")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"message":%q}`, message)
	}))
	t.Cleanup(server.Close)
	return server
}

func bedrockProbeStream(t *testing.T, ctx context.Context, baseURL string) *AssistantMessageEventStream {
	t.Helper()
	provider := NewBedrockProviderWithModel(Model{
		ID: "probe", DisplayName: "probe",
		ProviderMeta: ProviderMetadata{ProviderID: "probe-provider", API: APIBedrockConverseStream, BaseURL: baseURL},
		Capabilities: ModelCapabilities{ContextWindow: 4096, MaxOutputTokens: 256},
	})
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{
		APIKey: "test", Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

// A response that is not a 200 event stream reaches the SDK unchanged: its modeled error is deserialized from the body and formatted with Pi's prefix (bedrock-converse-stream.ts:337-360).
func TestBedrockErrorResponseReachesTheSDK(t *testing.T) {
	server := bedrockErrorServer(t, http.StatusBadRequest, "ValidationException", "bad input")
	result := bedrockProbeStream(t, t.Context(), server.URL).Result()
	if result.StopReason != StopReasonError || result.ErrorMessage != "Validation error: bad input" {
		t.Fatalf("result stop=%s message=%q", result.StopReason, result.ErrorMessage)
	}
	encoded, err := json.Marshal(result.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"status":400`, `"errorCode":"ValidationException"`, `"requestId":"req-error"`} {
		if !bytes.Contains(encoded, []byte(want)) {
			t.Fatalf("diagnostics %s lack %s", encoded, want)
		}
	}
}

func goroutinesIn(function string) int {
	var dump bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&dump, 2)
	return strings.Count(dump.String(), function)
}

// Cancelling while the pipeline waits for body bytes ends the stream as aborted and joins the provider's body reader.
func TestBedrockCancellationJoinsBodyReader(t *testing.T) {
	oracle, inputs := loadBedrockOracle(t)
	var tc bedrockOracleCase
	for _, candidate := range oracle.Cases {
		if candidate.HTTP == "h1" && string(candidate.Layers) == "0" && candidate.Consumer == "cancel" && candidate.Shape == "tool" && candidate.Delivery == "pending" {
			tc = candidate
		}
	}
	first, rest := bedrockOracleFrames(t, inputs, tc.Shape, tc.Delivery)
	server := newBedrockFixtureServer(t, first, rest, false)
	server.openOnce()
	_, port, _ := net.SplitHostPort(server.listener.Addr().String())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := bedrockProbeStream(t, ctx, "http://127.0.0.1:"+port)
	for event := range stream.Events(t.Context()) {
		if _, ok := event.(StartEvent); ok {
			cancel()
			break
		}
	}
	result := stream.Result()
	if result.StopReason != StopReasonAborted || result.ErrorMessage != "aborted" {
		t.Fatalf("result stop=%s message=%q", result.StopReason, result.ErrorMessage)
	}
	for range 1_000_000 {
		if goroutinesIn("observedBodyReadiness).readBody") == 0 && goroutinesIn("runBedrockPipeline") == 0 {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("the pipeline or its body reader outlived the aborted stream")
}
