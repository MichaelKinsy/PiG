package ai

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// transportCause is a Go transport failure described the way Node reports error.cause: a name, the Go message and a Node-style code.
type transportCause struct {
	name    string
	message string
	code    string
}

func (cause *transportCause) Error() string { return cause.message }
func (cause *transportCause) Name() string  { return cause.name }
func (cause *transportCause) DiagnosticCode() any {
	if cause.code == "" {
		return nil
	}
	return cause.code
}

// classifyTransportCause names the Go cause with the Node code a user would see for the same failure (ECONNRESET, ETIMEDOUT, ENOTFOUND, UND_ERR_HEADERS_TIMEOUT, CERT_*).
func classifyTransportCause(transport *nodeTransportError) *transportCause {
	cause := transport.cause
	out := &transportCause{name: "Error", message: cause.Error()}
	if code := nodeerrno.ErrorCode(cause); code != "" {
		out.code = code
		return out
	}
	if dns, ok := errors.AsType[*net.DNSError](cause); ok {
		switch {
		case dns.IsNotFound:
			out.code = "ENOTFOUND"
		case dns.IsTimeout || dns.IsTemporary:
			out.code = "EAI_AGAIN"
		}
		return out
	}
	if transport.headersTimeout {
		out.name, out.code = "HeadersTimeoutError", "UND_ERR_HEADERS_TIMEOUT"
		return out
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](cause); ok {
		out.code = "UNABLE_TO_VERIFY_LEAF_SIGNATURE"
		return out
	}
	if invalid, ok := errors.AsType[x509.CertificateInvalidError](cause); ok {
		out.code = "CERT_UNTRUSTED"
		if invalid.Reason == x509.Expired {
			out.code = "CERT_HAS_EXPIRED"
		}
		return out
	}
	if _, ok := errors.AsType[x509.HostnameError](cause); ok {
		out.code = "ERR_TLS_CERT_ALTNAME_INVALID"
		return out
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		out.code = "ETIMEDOUT"
		return out
	}
	if netErr, ok := errors.AsType[net.Error](cause); ok && netErr.Timeout() {
		out.code = "ETIMEDOUT"
		return out
	}
	if errors.Is(cause, io.ErrUnexpectedEOF) || errors.Is(cause, io.EOF) {
		out.name, out.code = "SocketError", "UND_ERR_SOCKET"
	}
	return out
}

// transportFailureDiagnostic is the provider_transport_failure diagnostic for err, or false when err is not a transport failure with a Go cause or is a caller abort. pig divergence (D180): Pi records this diagnostic only for the Codex WebSocket path; PiG records it for every transport failure so the cause is not lost.
func transportFailureDiagnostic(err error, eventsEmitted bool) (AssistantMessageDiagnostic, bool) {
	transport, ok := errors.AsType[*nodeTransportError](err)
	if !ok || transport.cause == nil || errors.Is(transport.cause, context.Canceled) {
		return AssistantMessageDiagnostic{}, false
	}
	if _, retryDelay := errors.AsType[*providerRetryDelayError](err); retryDelay {
		return AssistantMessageDiagnostic{}, false
	}
	phase := "before_message_stream_start"
	if eventsEmitted {
		phase = "after_message_stream_start"
	}
	return CreateAssistantMessageDiagnostic("provider_transport_failure", classifyTransportCause(transport), JsonObject{
		"eventsEmitted": eventsEmitted,
		"phase":         phase,
	}), true
}
