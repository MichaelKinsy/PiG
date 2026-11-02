package session_test

// Costs of the Session's ordering between publications: what one committed publication pays to start, before the next part
// of its call is published, the callers it released and the waits its observers woke.
//
// BenchmarkSessionPublish is the shape of a Harness settle: each iteration is one commit whose publication wakes one waiter
// (DeferUntilPublished, as the Harness wakes the waiters it settles), and the iteration ends when the waiter ran. The
// sub-benchmarks differ in the number of goroutines parked in the process: the ordering must cost the same at any number.

import (
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// awaitWake blocks until done is closed: the wait a woken goroutine is parked in, which the publication that wakes it waits for.
func awaitWake(kernel *session.SessionImpl, done <-chan struct{}) { kernel.Await(ctx, done) }

// parkGoroutines starts n goroutines that stay parked until the test ends.
func parkGoroutines(tb testing.TB, n int) {
	tb.Helper()
	stop := make(chan struct{})
	started := make(chan struct{}, n)
	for range n {
		go func() {
			started <- struct{}{}
			<-stop
		}()
	}
	for range n {
		<-started
	}
	tb.Cleanup(func() { close(stop) })
}

func BenchmarkSessionPublish(b *testing.B) {
	for _, parked := range []int{0, 100, 1000} {
		b.Run("parked="+strconv.Itoa(parked), func(b *testing.B) {
			parkGoroutines(b, parked)
			g := openGC(b, 1)
			id := g.ids[0]
			wakes := make(chan chan struct{}, 1)
			g.session.FollowCommits(func(_ ctxT, commit session.ObservedCommit) {
				if commit.Memory {
					return
				}
				select {
				case done := <-wakes:
					g.session.DeferUntilPublished(func() { close(done) })
				default:
				}
			})
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				done := make(chan struct{})
				wakes <- done
				resumed := make(chan struct{})
				go func() {
					awaitWake(g.session, done)
					close(resumed)
				}()
				if _, err := g.session.Commit(ctx, func(tx durable.Tx) (any, error) {
					return appendChange(id, "bench")(tx)
				}); err != nil {
					b.Fatal(err)
				}
				<-resumed
			}
			b.StopTimer()
			g.close()
		})
	}
}

// BenchmarkSessionCommit is a commit with nothing woken: the cost of delivering a caller's outcome in line order.
func BenchmarkSessionCommit(b *testing.B) {
	for _, parked := range []int{0, 100, 1000} {
		b.Run("parked="+strconv.Itoa(parked), func(b *testing.B) {
			parkGoroutines(b, parked)
			g := openGC(b, 1)
			id := g.ids[0]
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := g.session.Commit(ctx, func(tx durable.Tx) (any, error) {
					return appendChange(id, "bench")(tx)
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			g.close()
		})
	}
}
