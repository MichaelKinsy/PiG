package ai

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// Undici's buildConnector installs one timer and clears it on secureConnect, not on TCP connect.
func TestHTTPConnectBudgetIncludesTCPAndTLS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		client, peer := net.Pipe()
		defer func() { _ = peer.Close() }()
		dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Sub(start) != 10*time.Second {
				t.Fatalf("TCP budget=%v present=%v", deadline.Sub(start), ok)
			}
			time.Sleep(6 * time.Second)
			return client, nil
		}
		conn, err := dialStreamingTLS(t.Context(), "tcp", "example.test:443", dial, &tls.Config{}, defaultHTTPConnectTimeout)
		if conn != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("conn=%v error=%v", conn, err)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Fatalf("TCP and TLS received separate budgets: %v", elapsed)
		}
		if _, err := client.Write([]byte("closed")); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("failed handshake retained socket: %v", err)
		}
	})
}

func TestHTTPConnectBudgetHonorsEarlierCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		start := time.Now()
		dial := func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
		conn, err := dialStreamingTLS(ctx, "tcp", "example.test:443", dial, &tls.Config{}, defaultHTTPConnectTimeout)
		if conn != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 2*time.Second {
			t.Fatalf("conn=%v error=%v elapsed=%v", conn, err, time.Since(start))
		}
	})
}

func TestHTTPConnectBudgetHonorsHandshakeCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		client, peer := net.Pipe()
		defer func() { _ = peer.Close() }()
		started := make(chan struct{})
		dial := func(context.Context, string, string) (net.Conn, error) { close(started); return client, nil }
		done := make(chan error, 1)
		go func() {
			_, err := dialStreamingTLS(ctx, "tcp", "example.test:443", dial, &tls.Config{}, defaultHTTPConnectTimeout)
			done <- err
		}()
		<-started
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("handshake cancellation=%v", err)
		}
	})
}
