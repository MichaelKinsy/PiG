package host

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Not upstream's: Pi's hosts share one event loop, so a test awaits the other hosts' frames by hops of that loop. A link whose
// other end lives in another process asks the host that takes it for the wait (DeliveryAwaiter).

// awaitingLink is a link that is a DeliveryAwaiter: it records the order of what the host does with it, and keeps the wait
// the host gave it.
type awaitingLink struct {
	link.Link
	mu    sync.Mutex
	steps []string
	await func(context.Context) error
}

func newAwaitingLink(inner link.Link) *awaitingLink { return &awaitingLink{Link: inner} }

func (l *awaitingLink) step(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, name)
}

func (l *awaitingLink) Taken(await func(context.Context) error) {
	l.step("taken")
	l.mu.Lock()
	l.await = await
	l.mu.Unlock()
}

func (l *awaitingLink) Send(message []byte) {
	l.step("send")
	l.Link.Send(message)
}

func (l *awaitingLink) OnMessage(listener func([]byte)) {
	l.step("listen")
	l.Link.OnMessage(listener)
}

func (l *awaitingLink) recorded() ([]string, func(context.Context) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.steps), l.await
}

func TestDeliveryAwaiterIsTakenOnceBeforeTheHostSendsOnTheLinkOrListensToIt(t *testing.T) {
	for _, role := range []string{"accept", "connect"} {
		t.Run(role, func(t *testing.T) {
			h := newHost(t, HostOptions{OnError: func(HostError) {}})
			mustLoad(t)(h.Load(nil, background))
			a, b := link.MemoryLinkPair()
			taking := newAwaitingLink(a)
			var err error
			if role == "accept" {
				err = h.Accept(taking, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background)
			} else {
				err = h.Connect(taking, LinkOptions{Principal: serverPrincipal(), Codec: link.BinaryCodec}, background)
			}
			must(t, err)
			steps, await := taking.recorded()
			if len(steps) == 0 || steps[0] != "taken" || slices.Contains(steps[1:], "taken") {
				t.Fatalf("steps = %v, want one taken, first", steps)
			}
			if !slices.Contains(steps, "send") || !slices.Contains(steps, "listen") {
				t.Fatalf("steps = %v, want the host's hello and its listening after taken", steps)
			}
			if await == nil {
				t.Fatal("the host gave no wait")
			}
			ctx, cancel := context.WithTimeout(background, 15*time.Second)
			defer cancel()
			if err := await(ctx); err != nil {
				t.Fatalf("await = %v", err)
			}
			b.Close()
			if err := await(ctx); err != nil {
				t.Fatalf("await on a link that closed = %v, want nil at once", err)
			}
		})
	}
}
