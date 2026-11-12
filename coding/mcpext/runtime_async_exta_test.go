package mcpext_test

// pi: packages/coding-agent/src/extensions/mcp/runtime.ts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// loggedTransport records its creation and its Close in the shared log, so a test can assert the order of both.
type loggedTransport struct {
	mcp.Transport
	n   int
	log *orderLog
	one sync.Once
}

func (l *loggedTransport) Close() error {
	l.one.Do(func() { l.log.add(fmt.Sprintf("close %d", l.n)) })
	return l.Transport.Close()
}

// gatedConnection is a connection whose first transport creation blocks until gate is closed.
type gatedConnection struct {
	*mcpext.Connection
	created atomic.Int32
	gate    chan struct{}
	release func()
	log     *orderLog
}

func newGatedConnection(t *testing.T, gateFirst bool) *gatedConnection {
	t.Helper()
	g := &gatedConnection{gate: make(chan struct{}), log: &orderLog{}}
	g.release = sync.OnceFunc(func() { close(g.gate) })
	if !gateFirst {
		g.release()
	}
	servers := &serverSet{}
	t.Cleanup(servers.closeAll)
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: stdioEntry(),
		Cwd:   ".",
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			n := int(g.created.Add(1))
			g.log.add(fmt.Sprintf("create %d", n))
			if n == 1 {
				<-g.gate
			}
			return &loggedTransport{Transport: createFakeTransport(servers, fakeTransportOptions{}), n: n, log: g.log}, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	g.Connection = connection
	t.Cleanup(connection.Close)
	t.Cleanup(g.release)
	return g
}

func assertLog(t *testing.T, log *orderLog, want ...string) {
	t.Helper()
	got := log.snapshot()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

func isDone[T any](ch <-chan T) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

type clientResult struct {
	client *mcp.Client
	err    error
}

// runtime.ts:239-247: concurrent getClient calls share the one `opening` promise. A caller whose context ends stops waiting but does
// not stop the shared open (Go: the open runs on the connection's own lifetime); later callers receive its client.
func TestMCPGetClientSharesOneConnectionAttemptAndASharedOpenSurvivesACanceledCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGatedConnection(t, true)
		canceledCtx, cancel := context.WithCancel(context.Background())
		results := make(chan clientResult, 3)
		canceled := make(chan clientResult, 1)
		go func() {
			c, err := g.GetClient(canceledCtx)
			canceled <- clientResult{c, err}
		}()
		// The canceled caller starts the shared open, so its context must not own it.
		synctest.Wait()
		for range 2 {
			go func() {
				c, err := g.GetClient(context.Background())
				results <- clientResult{c, err}
			}()
		}
		synctest.Wait()
		cancel()
		synctest.Wait()
		if got := <-canceled; !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled caller = %v, want context.Canceled", got.err)
		}
		if isDone(results) {
			t.Fatal("a caller returned before the shared open finished")
		}
		g.release()
		synctest.Wait()
		first, second := <-results, <-results
		if first.err != nil || second.err != nil || first.client == nil || first.client != second.client {
			t.Fatalf("callers got %+v and %+v, want one shared client", first, second)
		}
		if n := g.created.Load(); n != 1 {
			t.Fatalf("%d transports created, want one shared attempt", n)
		}
	})
}

// runtime.ts:306-316: reconnect awaits an open in progress, then drops the client it produced and opens a new one.
func TestMCPReconnectWaitsForAnInFlightOpenThenReplacesItsClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGatedConnection(t, true)
		go func() { _, _ = g.GetClient(context.Background()) }()
		synctest.Wait()
		reconnected := make(chan error, 1)
		go func() { reconnected <- g.Reconnect(context.Background()) }()
		synctest.Wait()
		if isDone(reconnected) {
			t.Fatal("Reconnect returned while the open it must wait for was still running")
		}
		g.release()
		synctest.Wait()
		if err := <-reconnected; err != nil {
			t.Fatal(err)
		}
		assertLog(t, g.log, "create 1", "close 1", "create 2")
	})
}

// runtime.ts:306-316: signOut awaits an open in progress, then drops the client it produced and marks the server as needing sign-in.
func TestMCPSignOutWaitsForAnInFlightOpenThenDropsItsClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGatedConnection(t, true)
		go func() { _, _ = g.GetClient(context.Background()) }()
		synctest.Wait()
		signedOut := make(chan struct{})
		go func() { g.SignOut(); close(signedOut) }()
		synctest.Wait()
		if isDone(signedOut) {
			t.Fatal("SignOut returned while the open it must wait for was still running")
		}
		g.release()
		synctest.Wait()
		if !isDone(signedOut) {
			t.Fatal("SignOut did not return after the open finished")
		}
		assertLog(t, g.log, "create 1", "close 1")
		if state := g.State(); state != mcpext.StateNeedsAuth {
			t.Fatalf("state = %v, want needs-auth", state)
		}
	})
}

// runtime.ts:445-454: close closes the client and then waits for the token refresh that is running (authProvider.settled), so a
// rotated refresh token is saved before close returns.
func TestMCPCloseClosesTheClientThenWaitsForTokenSettlement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGatedConnection(t, false)
		if _, err := g.GetClient(context.Background()); err != nil {
			t.Fatal(err)
		}
		release := make(chan struct{})
		releaseSettled := sync.OnceFunc(func() { close(release) })
		t.Cleanup(releaseSettled)
		mcpext.SetSettledForTest(g.Connection, func(context.Context) {
			g.log.add("settled")
			<-release
		})
		closed := make(chan struct{})
		go func() { g.Close(); close(closed) }()
		synctest.Wait()
		if isDone(closed) {
			t.Fatal("Close returned before token settlement finished")
		}
		assertLog(t, g.log, "create 1", "close 1", "settled")
		releaseSettled()
		synctest.Wait()
		if !isDone(closed) {
			t.Fatal("Close did not return after settlement")
		}
	})
}
