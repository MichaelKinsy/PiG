package ai

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestHTTPBodyReadOwnershipTransferKeepsNewRequest(t *testing.T) {
	owner := &bodyReadOwner{}
	oldRead := &bodyReadOperation{signals: make(chan bodyReadSignal, 2)}
	newRead := &bodyReadOperation{signals: make(chan bodyReadSignal, 2)}
	owner.begin(oldRead)
	// net/http can publish the next request's connection before the prior body worker finishes returning from EOF.
	owner.begin(newRead)
	owner.clear(oldRead)
	owner.pending()
	if signal := <-newRead.signals; !signal.pending {
		t.Fatalf("new request lost its pending signal: %#v", signal)
	}
	owner.clear(newRead)
}

func TestHTTPBodyReadRetireDoesNotClearReusedConnection(t *testing.T) {
	owner := &bodyReadOwner{}
	oldBody := &observedResponseBody{ReadCloser: io.NopCloser(strings.NewReader("old")), owner: owner}
	newBody := &observedResponseBody{ReadCloser: io.NopCloser(strings.NewReader("new")), owner: owner}
	oldRead := &bodyReadOperation{signals: make(chan bodyReadSignal, 2)}
	newRead := &bodyReadOperation{signals: make(chan bodyReadSignal, 2)}
	oldBody.begin(oldRead)
	newBody.begin(newRead)
	oldBody.retire()
	oldBody.clear(oldRead)
	owner.pending()
	if signal := <-newRead.signals; !signal.pending {
		t.Fatalf("old body retired the new request: %#v", signal)
	}
	newBody.clear(newRead)
}

func TestHTTPBodyReadCompletionDoesNotInventPending(t *testing.T) {
	operation := &bodyReadOperation{signals: make(chan bodyReadSignal, 2)}
	operation.complete([]byte("ready"), io.EOF)
	operation.pending()
	signal := <-operation.signals
	if signal.pending || string(signal.data) != "ready" || !errors.Is(signal.err, io.EOF) {
		t.Fatalf("buffered read was classified as pending: %#v", signal)
	}
}
