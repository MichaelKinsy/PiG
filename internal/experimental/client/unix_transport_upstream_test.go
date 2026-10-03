package client

// Ports packages/client/test/unix-transport.test.ts (option validation, which upstream runs on every platform).

import (
	"regexp"
	"testing"
)

func TestUnixTransportUpstream(t *testing.T) {
	t.Parallel()
	t.Run("rejects invalid Unix transport options", func(t *testing.T) {
		// upstream: packages/client/test/unix-transport.test.ts:49
		t.Parallel()
		if _, err := CreateUnixTransportFactory(UnixTransportOptions{Path: ""}); err == nil || !regexp.MustCompile(`must not be empty`).MatchString(err.Error()) {
			t.Fatalf("empty path error = %v", err)
		}
		zero := float64(0)
		if _, err := CreateUnixTransportFactory(UnixTransportOptions{Path: "/tmp/pi.sock", MaxPendingBytes: &zero}); err == nil || !regexp.MustCompile(`positive`).MatchString(err.Error()) {
			t.Fatalf("maxPendingBytes 0 error = %v", err)
		}
	})
}
