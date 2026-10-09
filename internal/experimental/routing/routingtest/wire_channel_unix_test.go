//go:build unix

package routingtest_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// packages/server/src/testing/client.ts WireChannel (send client.ts:22, sendFragmented client.ts:23, close client.ts:24) as the Unix socket transport implements it:
// send writes the chunk, sendFragmented writes it in two parts split with Array.prototype.slice index semantics, and close
// destroys the socket once and returns after the socket closed, also when it is already closed.
// Pi: packages/server/src/testing/client.ts:22 (send)
// mutation-checked: the mutant "unix SendFragmented drops the second chunk" fails it.
func TestUnixWireChannelSendsFragmentsAndCloses(t *testing.T) {
	listener, path := listenUnix(t)
	accepted := acceptOne(t, listener)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := routingtest.ConnectUnixTestClient(ctx, path)
	mustNoError(t, err)
	peer := <-accepted
	if peer == nil {
		t.Fatal("the listener accepted no connection")
	}
	defer func() { _ = peer.Close() }()
	wire := routingtest.WireChannelOf(client)

	read := func(n int) string {
		t.Helper()
		buffer := make([]byte, n)
		mustNoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
		_, err := io.ReadFull(peer, buffer)
		mustNoError(t, err)
		return string(buffer)
	}

	mustNoError(t, wire.Send([]byte("abc")))
	if got := read(3); got != "abc" {
		t.Fatalf("Send delivered %q, want abc", got)
	}
	for _, splitAt := range []int{2, -2, 0, 99} {
		mustNoError(t, wire.SendFragmented([]byte("abcdef"), splitAt))
		if got := read(6); got != "abcdef" {
			t.Fatalf("SendFragmented(split %d) delivered %q, want abcdef", splitAt, got)
		}
	}

	mustNoError(t, wire.Close())
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("after Close the peer read %v, want io.EOF", err)
	}
	mustNoError(t, wire.Close())
	if err := wire.Send([]byte("x")); err == nil {
		t.Fatal("Send after Close succeeded, want the closed socket's error")
	}
}
