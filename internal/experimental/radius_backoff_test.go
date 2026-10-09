package experimental

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// relayAttemptClock records the fake-time offset of each attempt inside a synctest bubble.
type relayAttemptClock struct {
	mu       sync.Mutex
	start    time.Time
	attempts []time.Duration
}

func (clock *relayAttemptClock) record() {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.attempts = append(clock.attempts, time.Since(clock.start))
}

func (clock *relayAttemptClock) snapshot() []time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return slices.Clone(clock.attempts)
}

func relaySeconds(values ...int) []time.Duration {
	durations := make([]time.Duration, len(values))
	for i, value := range values {
		durations[i] = time.Duration(value) * time.Second
	}
	return durations
}

// upstream: packages/coding-agent/src/experimental/radius-relay.ts:16-17,136-170. After each failed attempt the host waits retryMs, starting at HOST_RETRY_INITIAL_MS (1 s) and doubling up to HOST_RETRY_MAX_MS (30 s): attempts at 0, 1, 3, 7, 15, 31, 61 and 91 s.
func TestRadiusHostRetryBackoffDoublesToThirtySeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &relayAttemptClock{start: time.Now()}
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
			ServerID: testServerID,
			Auth:     explicitAuth(t, "http://localhost"),
			WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
				clock.record()
				return nil, errors.New("gateway unavailable")
			},
			Accept: func(*RelayServerByteConnection) RelayByteConnectionHandler { return RelayByteConnectionHandler{} },
		})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		time.Sleep(91*time.Second + time.Millisecond)
		synctest.Wait()
		host.Close()
		if got, want := clock.snapshot(), relaySeconds(0, 1, 3, 7, 15, 31, 61, 91); !slices.Equal(got, want) {
			t.Fatalf("attempts at %v, want %v", got, want)
		}
	})
}

// upstream: packages/coding-agent/src/experimental/radius-relay.ts:156-160. A connection resets retryMs, so after a connected socket drops the next attempt follows after 1 s, not after the backed-off wait: failures at 0, 1 and 3 s, a connection at 7 s that drops, then an attempt at 8 s.
func TestRadiusHostConnectionResetsRetryBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &relayAttemptClock{start: time.Now()}
		var statuses []string
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
			ServerID: testServerID,
			Auth:     explicitAuth(t, "http://localhost"),
			WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
				clock.record()
				if len(clock.snapshot()) != 4 {
					return nil, errors.New("gateway unavailable")
				}
				socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
				if err := socket.Close(1006, "lost"); err != nil {
					return nil, err
				}
				return socket, nil
			},
			OnStatus: func(status RadiusRelayHostStatus) { statuses = append(statuses, status.Status) },
			Accept:   func(*RelayServerByteConnection) RelayByteConnectionHandler { return RelayByteConnectionHandler{} },
		})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		time.Sleep(8*time.Second + time.Millisecond)
		synctest.Wait()
		host.Close()
		if got, want := clock.snapshot(), relaySeconds(0, 1, 3, 7, 8); !slices.Equal(got, want) {
			t.Fatalf("attempts at %v, want %v", got, want)
		}
		if !slices.Contains(statuses, "connected") {
			t.Fatalf("statuses = %v", statuses)
		}
	})
}

// upstream: packages/coding-agent/src/experimental/radius-relay.ts:18,143-147. Without credentials the host reports not_authenticated and checks again after MISSING_AUTH_RETRY_MS (30 s), without backing off.
func TestRadiusHostMissingAuthRetriesEveryThirtySeconds(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	synctest.Test(t, func(t *testing.T) {
		clock := &relayAttemptClock{start: time.Now()}
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
			ServerID: testServerID,
			Auth:     explicitAuth(t, "http://localhost"),
			OnStatus: func(status RadiusRelayHostStatus) {
				if status.Status != "not_authenticated" {
					t.Errorf("status = %+v", status)
				}
				clock.record()
			},
			Accept: func(*RelayServerByteConnection) RelayByteConnectionHandler { return RelayByteConnectionHandler{} },
		})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		time.Sleep(90*time.Second + time.Millisecond)
		synctest.Wait()
		host.Close()
		if got, want := clock.snapshot(), relaySeconds(0, 30, 60, 90); !slices.Equal(got, want) {
			t.Fatalf("checks at %v, want %v", got, want)
		}
	})
}

// upstream: packages/coding-agent/src/experimental/radius-relay.ts:19-20,386-400. A disconnected client waits CLIENT_RETRY_INITIAL_MS (1 s) before its first reconnect and doubles the wait after each failure up to CLIENT_RETRY_MAX_MS (30 s): attempts at 1, 3, 7, 15, 31, 61 and 91 s.
func TestRadiusClientReconnectBackoffDoublesToThirtySeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &relayAttemptClock{start: time.Now()}
		client := &fakeReconnectClient{connected: true}
		client.reconnect = func(context.Context) error {
			clock.record()
			return errors.New("gateway unavailable")
		}
		reconnect := NewRadiusClientReconnect(t.Context(), client, func(context.Context, string) error { return nil })
		client.Disconnect("lost")
		time.Sleep(91*time.Second + time.Millisecond)
		synctest.Wait()
		reconnect.Dispose()
		if got, want := clock.snapshot(), relaySeconds(1, 3, 7, 15, 31, 61, 91); !slices.Equal(got, want) {
			t.Fatalf("attempts at %v, want %v", got, want)
		}
	})
}
