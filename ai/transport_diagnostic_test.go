package ai

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
)

func TestClassifyTransportCause(t *testing.T) {
	cases := []struct {
		name      string
		transport *nodeTransportError
		code      string
	}{
		{"reset", &nodeTransportError{message: "fetch failed", cause: &net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}}, "ECONNRESET"},
		{"refused", &nodeTransportError{message: "fetch failed", cause: &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}}, "ECONNREFUSED"},
		{"pipe", &nodeTransportError{message: "fetch failed", cause: syscall.EPIPE}, "EPIPE"},
		{"dns not found", &nodeTransportError{message: "fetch failed", cause: &net.DNSError{Err: "no such host", IsNotFound: true}}, "ENOTFOUND"},
		{"dns again", &nodeTransportError{message: "fetch failed", cause: &net.DNSError{Err: "timeout", IsTimeout: true}}, "EAI_AGAIN"},
		{"dial timeout", &nodeTransportError{message: "fetch failed", cause: &net.OpError{Op: "dial", Err: context.DeadlineExceeded}}, "ETIMEDOUT"},
		{"headers timeout", &nodeTransportError{message: "fetch failed", cause: errors.New("idle"), headersTimeout: true}, "UND_ERR_HEADERS_TIMEOUT"},
		{"unknown authority", &nodeTransportError{message: "fetch failed", cause: x509.UnknownAuthorityError{}}, "UNABLE_TO_VERIFY_LEAF_SIGNATURE"},
		{"expired", &nodeTransportError{message: "fetch failed", cause: x509.CertificateInvalidError{Reason: x509.Expired}}, "CERT_HAS_EXPIRED"},
		{"hostname", &nodeTransportError{message: "fetch failed", cause: x509.HostnameError{Certificate: &x509.Certificate{}, Host: "h"}}, "ERR_TLS_CERT_ALTNAME_INVALID"},
		{"unexpected eof", &nodeTransportError{message: "terminated", cause: io.ErrUnexpectedEOF}, "UND_ERR_SOCKET"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostic, ok := transportFailureDiagnostic(tc.transport, false)
			if !ok || diagnostic.Type != "provider_transport_failure" || diagnostic.Error == nil {
				t.Fatalf("diagnostic = %+v ok=%v", diagnostic, ok)
			}
			if diagnostic.Error.Code != tc.code || diagnostic.Error.Message != tc.transport.cause.Error() {
				t.Fatalf("error = %+v, want code %q message %q", diagnostic.Error, tc.code, tc.transport.cause.Error())
			}
			if diagnostic.Details["phase"] != "before_message_stream_start" || diagnostic.Details["eventsEmitted"] != false {
				t.Fatalf("details = %v", diagnostic.Details)
			}
		})
	}
}

func TestTransportFailureDiagnosticSkipsAbortsAndOtherErrors(t *testing.T) {
	for _, err := range []error{
		&nodeTransportError{message: "This operation was aborted", cause: context.Canceled},
		&nodeTransportError{message: "fetch failed"},
		errors.New("plain"),
		context.DeadlineExceeded,
	} {
		if _, ok := transportFailureDiagnostic(err, false); ok {
			t.Fatalf("%v produced a diagnostic", err)
		}
	}
}

func TestProviderTransportResetRecordsDiagnosticAndKeepsMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _ := closeHTTPConnection(t, writer)
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}))
	defer server.Close()
	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	if result.ErrorMessage != "Connection error." || result.StopReason != StopReasonError {
		t.Fatalf("result = %q %q, want unchanged Connection error.", result.StopReason, result.ErrorMessage)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Type != "provider_transport_failure" || diagnostic.Error.Code == nil {
		t.Fatalf("diagnostic = %+v", diagnostic)
	}
}

func TestProviderTransportMidStreamRecordsDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, buffered := closeHTTPConnection(t, writer)
		_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 4096\r\n\r\n")
		_, _ = buffered.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		_ = buffered.Flush()
		_ = connection.Close()
	}))
	defer server.Close()
	result := transportErrorTestResult(t, transportErrorTestProvider(server.URL))
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Details["phase"] != "after_message_stream_start" || result.Diagnostics[0].Details["eventsEmitted"] != true {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestProviderContextCancelRecordsNoTransportDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) { <-request.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	stream, err := transportErrorTestProvider(server.URL).Stream(ctx, transcript, StreamOptions{})
	if err != nil {
		return
	}
	if result := stream.Result(); len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

// An entry stored before the diagnostic existed has no diagnostics member and is written back byte for byte.
func TestOldAssistantMessageWithoutDiagnosticRoundTrips(t *testing.T) {
	const stored = `{"role":"assistant","content":[],"api":"openai-completions","provider":"openai","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","timestamp":1,"errorMessage":"Connection error."}`
	var message AssistantMessage
	if err := json.Unmarshal([]byte(stored), &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Diagnostics) != 0 {
		t.Fatalf("loading invented diagnostics: %+v", message.Diagnostics)
	}
	out, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != stored {
		t.Fatalf("round trip changed:\n got %s\nwant %s", out, stored)
	}
}

// The diagnostic rides on the message only: the event order and the visible failure are what they were.
func TestTransportDiagnosticLeavesEventStreamUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _ := closeHTTPConnection(t, writer)
		_ = connection.Close()
	}))
	defer server.Close()
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
	stream, err := transportErrorTestProvider(server.URL).Stream(context.Background(), transcript, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for event := range stream.Events(context.Background()) {
		switch event.(type) {
		case ErrorEvent:
			kinds = append(kinds, "error")
		default:
			kinds = append(kinds, "other")
		}
	}
	if len(kinds) != 1 || kinds[0] != "error" {
		t.Fatalf("events = %v, want a single error event", kinds)
	}
	result := stream.Result()
	if result.StopReason != StopReasonError || result.ErrorMessage != "Connection error." || !IsRetryableAssistantError(*result) {
		t.Fatalf("result = %q %q", result.StopReason, result.ErrorMessage)
	}
}
