package ai

// Ports the "connection error cause detail" cases of packages/ai/test/error-body.test.ts (.upstream/current).
// Pi's `cause` chain is the Unwrap chain; a `code` property is a Code method.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
)

type codedError struct{ message, code string }

func (err *codedError) Error() string { return err.message }
func (err *codedError) Code() string  { return err.code }

// causeChain is an error with a message and one cause, the way `new Error(message, { cause })` is.
type causeChain struct {
	message string
	cause   error
}

func (err *causeChain) Error() string { return err.message }
func (err *causeChain) Unwrap() error { return err.cause }

func TestConnectionErrorCauseDetailUpstream(t *testing.T) {
	// .upstream/current/packages/ai/test/error-body.test.ts:256
	t.Run("appends the deepest cause of a connection error with no status or body", func(t *testing.T) {
		socket := &codedError{"connect ECONNREFUSED 127.0.0.1:8080", "ECONNREFUSED"}
		fetch := &causeChain{"fetch failed", socket}
		err := &causeChain{"Connection error.", fetch}
		if got := FormatProviderError(NormalizeProviderError(err)); got != "Connection error.: connect ECONNREFUSED 127.0.0.1:8080" {
			t.Fatal(got)
		}
	})
	// .upstream/current/packages/ai/test/error-body.test.ts:267
	t.Run("adds the error code when the cause message lacks it", func(t *testing.T) {
		err := &causeChain{"Connection error.", &codedError{"other side closed", "UND_ERR_SOCKET"}}
		if got := WithCauseDetail(err); got != "Connection error.: other side closed (UND_ERR_SOCKET)" {
			t.Fatal(got)
		}
	})
	// .upstream/current/packages/ai/test/error-body.test.ts:274
	t.Run("leaves errors without a cause unchanged", func(t *testing.T) {
		if got := WithCauseDetail(errors.New("Connection error.")); got != "Connection error." {
			t.Fatal(got)
		}
	})
	t.Run("stops at five levels", func(t *testing.T) {
		var err error = errors.New("level 6")
		for level := 5; level >= 0; level-- {
			err = &causeChain{fmt.Sprintf("level %d", level), err}
		}
		if got := WithCauseDetail(err); got != "level 0: level 5" {
			t.Fatal(got)
		}
	})
	t.Run("an error with a status keeps its message", func(t *testing.T) {
		err := &providerError{message: "Connection error.", status: new(502)}
		if got := NormalizeProviderError(err).Message; got != "Connection error." {
			t.Fatal(got)
		}
	})
	// .upstream/current/packages/ai/test/error-body.test.ts:278: a real request to a closed local port through the Node fetch boundary,
	// whose "fetch failed" carries Node's connection failure as its cause.
	t.Run("surfaces the reason from a real connection failure", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+address+"/v1/chat/completions", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&nodeFetchTransport{base: http.DefaultTransport}).RoundTrip(request)
		if err == nil {
			t.Fatal("expected a connection failure")
		}
		if got, want := FormatProviderError(NormalizeProviderError(err)), "fetch failed: connect ECONNREFUSED "+address; got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	})
	t.Run("an aborted request keeps its message", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&nodeFetchTransport{base: http.DefaultTransport}).RoundTrip(request)
		if err == nil {
			t.Fatal("expected an abort")
		}
		if got := WithCauseDetail(err); got != "This operation was aborted" {
			t.Fatalf("message = %q", got)
		}
	})
}
