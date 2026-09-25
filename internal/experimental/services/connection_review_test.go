package services

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Promise.all maps the insertion-ordered binding set before awaiting it. Already rejected bindings therefore report the first binding's error, not a scheduler winner.
func TestReadyPreservesImmediateFailureOrder(t *testing.T) {
	for _, attached := range []bool{true, false} {
		t.Run(fmt.Sprintf("attached=%t", attached), func(t *testing.T) {
			client := &localClient{state: "connected"}
			if attached {
				client.attachment = &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}
			}
			first, second := &localBinding{}, &localBinding{}
			source := CreateSessionServiceSource(client, localOptions(first, second))
			defer func() {
				if err := source.Dispose(t.Context()); err != nil {
					t.Error(err)
				}
			}()
			for range 2 {
				binding, err := source.Open(openOptions())
				if err != nil {
					t.Fatal(err)
				}
				if err := binding.Ready(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			var calls []string
			firstFailure, secondFailure := errors.New("first ready failure"), errors.New("second ready failure")
			first.ready = func(_ context.Context, complete func(error)) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, "first")
				complete(firstFailure)
			}
			second.ready = func(_ context.Context, complete func(error)) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, "second")
				complete(secondFailure)
			}
			var err error
			if attached {
				err = source.WhenAttached("session", t.Context())
			} else {
				err = source.WhenDetached(t.Context())
			}
			if !errors.Is(err, firstFailure) {
				t.Errorf("ready returned %v; want first binding failure", err)
			}
			if !reflect.DeepEqual(calls, []string{"first", "second"}) {
				t.Errorf("readiness invocation order = %v", calls)
			}
		})
	}
}

func TestReadinessLaterRejectionCancelsAndDrainsEarlierWaiter(t *testing.T) {
	for _, attached := range []bool{true, false} {
		t.Run(fmt.Sprintf("attached=%t", attached), func(t *testing.T) {
			client := &localClient{state: "connected"}
			if attached {
				client.attachment = &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}
			}
			first, second := &localBinding{}, &localBinding{}
			failure := errors.New("later binding rejected")
			started, released := make(chan struct{}), make(chan struct{})
			first.ready = func(ctx context.Context, complete func(error)) {
				close(started)
				first.readyTasks.Go(func() { <-ctx.Done(); close(released); complete(ctx.Err()) })
			}
			second.ready = func(_ context.Context, complete func(error)) {
				second.readyTasks.Go(func() { <-started; complete(failure) })
			}
			source := CreateSessionServiceSource(client, localOptions(first, second))
			for range 2 {
				if _, err := source.Open(openOptions()); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var err error
			if attached {
				err = source.WhenAttached("session", ctx)
			} else {
				err = source.WhenDetached(ctx)
			}
			if !errors.Is(err, failure) || ctx.Err() != nil {
				t.Fatalf("later rejection waited for the earlier binding: %v (context=%v)", err, ctx.Err())
			}
			select {
			case <-released:
			default:
				t.Fatal("rejected readiness did not drain the earlier waiter")
			}
			if err := source.Dispose(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReadinessCancellationDrainsAllWaiters(t *testing.T) {
	client := &localClient{state: "connected"}
	locals := make([]*localBinding, 32)
	started := make(chan struct{}, len(locals))
	var active atomic.Int32
	for i := range locals {
		local := &localBinding{}
		local.ready = func(ctx context.Context, complete func(error)) {
			active.Add(1)
			started <- struct{}{}
			local.readyTasks.Go(func() { <-ctx.Done(); active.Add(-1); complete(ctx.Err()) })
		}
		locals[i] = local
	}
	source := CreateSessionServiceSource(client, localOptions(locals...))
	for range locals {
		if _, err := source.Open(openOptions()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- source.WhenDetached(ctx) }()
	for range locals {
		<-started
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled readiness = %v", err)
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("readiness retained %d cancelled waiters", got)
	}
	if err := source.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// connection.ts always creates the server transition's aggregate from all failed bindings, even when a binding already returned an aggregate (or wraps one).
func TestServerAlwaysWrapsBindingAggregate(t *testing.T) {
	inner := &AggregateError{Message: "inner aggregate", Errors: []any{errors.New("inner")}}
	for _, failures := range [][]error{{inner}, {fmt.Errorf("wrapped: %w", inner)}, {inner, errors.New("second binding")}} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			client := &localClient{state: "connected"}
			locals := make([]*localBinding, len(failures))
			for i := range locals {
				locals[i] = &localBinding{}
			}
			reported := make(chan error, 1)
			options := localOptions(locals...)
			options.OnError = func(err error) { reported <- err }
			source := CreateServerServiceSource(client, options)
			for i, local := range locals {
				binding, err := source.Open(openOptions())
				if err != nil {
					t.Fatal(err)
				}
				if err := binding.Ready(t.Context()); err != nil {
					t.Fatal(err)
				}
				local.rebind = func(context.Context, bool) error { return failures[i] }
			}
			client.connect("disconnected", nil)
			if err := source.Dispose(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-reported:
				want := &AggregateError{}
				for _, failure := range failures {
					want.Errors = append(want.Errors, failure)
				}
				if !reflect.DeepEqual(err, want) {
					t.Fatalf("server lost the outer aggregate boundary: %#v; want direct causes %v", err, failures)
				}
			default:
				t.Fatal("server did not report rebind failures")
			}
		})
	}
}

// connection.ts publishes before registering transition work. A thrown listener leaves neither a transition nor a binding rebind for disposal to drain.
func TestListenerPanicDoesNotOrphanTransition(t *testing.T) {
	for _, kind := range []string{"server", "session"} {
		for _, reconnect := range []bool{false, true} {
			name := kind + "/dispose"
			if reconnect {
				name = kind + "/reconnect"
			}
			t.Run(name, func(t *testing.T) {
				client := &localClient{state: "connected"}
				local := &localBinding{}
				var dispose func(context.Context) error
				var change, remove func()
				var binding *RoutedServiceBinding
				var err error
				if kind == "server" {
					source := CreateServerServiceSource(client, localOptions(local))
					binding, err = source.Open(openOptions())
					dispose = source.Dispose
					change = func() { client.connect("disconnected", nil) }
					remove = source.Connection.Subscribe(func(_ ServerConnectionState, _ context.Context, delivery ReplicatedStateDelivery) {
						if delivery.Kind == "update" {
							panic("listener failed")
						}
					})
				} else {
					source := CreateSessionServiceSource(client, localOptions(local))
					binding, err = source.Open(openOptions())
					dispose = source.Dispose
					change = func() { client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}) }
					remove = source.Attachment.Subscribe(func(_ SessionAttachmentState, _ context.Context, delivery ReplicatedStateDelivery) {
						if delivery.Kind == "update" {
							panic("listener failed")
						}
					})
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := binding.Ready(t.Context()); err != nil {
					t.Fatal(err)
				}
				if got := capturePanic(change); got != "listener failed" {
					t.Fatalf("publication panic = %v", got)
				}
				remove()
				want := []bool{kind == "server"}
				if reconnect {
					if kind == "server" {
						client.connect("connected", nil)
					} else {
						client.attach(nil)
					}
					want = append(want, kind == "server")
				}
				done := make(chan error, 1)
				go func() { done <- dispose(t.Context()) }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("listener panic orphaned transition work; disposal did not finish")
				}
				if got := local.history(); !reflect.DeepEqual(got, want) {
					t.Fatalf("failed publication started binding work: %v; want %v", got, want)
				}
			})
		}
	}
}
