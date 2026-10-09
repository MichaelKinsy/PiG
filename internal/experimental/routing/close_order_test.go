package routing_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// upstream: packages/server/src/session-router.ts:110-120 closeInternal snapshots openingSessions, an insertion-ordered Map, awaits Promise.allSettled, and reports the rejections in that snapshot order.
// mutation-checked: negating the condition `operation.err != nil` at session_router.go:227 fails it.
func TestRouterCloseReportsOpeningFailuresInOpeningOrder(t *testing.T) {
	const sessions = 8
	entered := make([]chan struct{}, sessions)
	release := make([]chan struct{}, sessions)
	ids := make([]string, sessions)
	failures := make(map[string]error, sessions)
	for i := range sessions {
		entered[i], release[i] = make(chan struct{}), make(chan struct{})
		ids[i] = fmt.Sprintf("session-%d", i)
		failures[ids[i]] = fmt.Errorf("resolve %s failed", ids[i])
	}
	host := routing.ServerHost{
		ServerServices: routingtest.CreateTestServerServices(),
		ResolveSession: func(_ context.Context, id string) (routing.SessionMetadata, error) {
			index := slices.Index(ids, id)
			close(entered[index])
			<-release[index]
			return nil, failures[id]
		},
		OpenSession: func(context.Context, routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
			return nil, errors.New("unreachable")
		},
	}
	var mu sync.Mutex
	var reported []string
	server := createServer(t, host, func(options *routing.ServerOptions) {
		options.OnError = func(err error) {
			mu.Lock()
			defer mu.Unlock()
			for id, failure := range failures {
				if errors.Is(err, failure) {
					reported = append(reported, id)
				}
			}
		}
	})
	attaches := make([]<-chan attachResult, sessions)
	for i := range sessions {
		client := connect(t, server)
		client.hello(protocol.ProtocolVersion)
		attaches[i] = client.attachAsync(testServerID, ids[i])
		<-entered[i]
	}
	closing := make(chan error, 1)
	go func() { closing <- server.Close() }()
	pollUntil(t, "router draining in-flight acquisitions", routerIsDraining)
	for i := sessions - 1; i >= 0; i-- {
		close(release[i])
	}
	if err := <-closing; err != nil {
		t.Fatalf("Close = %v", err)
	}
	for _, attach := range attaches {
		<-attach
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(reported, ids) {
		t.Fatalf("opening failures reported as %v, want the opening order %v", reported, ids)
	}
}
