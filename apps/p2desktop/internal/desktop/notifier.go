package desktop

import (
	"context"
	"fmt"
	"os"
	"sync"

	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// notifier watches the live status of every session as the device owner (the same `p2.session-status` reader the web UI's
// sidebar holds, over a memory link to the in-process server) and reports a session that stopped working while the window
// is not in front. It takes no part in the protocol: it is one more owner client of the server.
type notifier struct {
	client host.Host
	ref    *host.Reference
	stop   func()

	mu   sync.Mutex
	busy map[string]bool   // sessions seen working
	err  map[string]string // the last error reported per session
}

// the notifier is one more owner client of the server; it adds nothing to the protocol.
// startNotifier connects an owner client to server and calls report(session, kind) when a session finishes ("finished")
// or fails ("failed"). Call close to stop it before the server closes.
func startNotifier(server host.Host, report func(session, kind string)) (*notifier, error) {
	ctx := chordctx.Background()
	down, up := link.MemoryLinkPair()
	owner := p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
	if err := server.Accept(down, host.LinkOptions{Principal: p2runtime.ToChord(owner)}, ctx); err != nil {
		return nil, err
	}
	client, err := p2runtime.NewHost(host.HostOptions{OnError: func(failure host.HostError) {
		fmt.Fprintf(os.Stderr, "p2 desktop: notifier: %v\n", failure)
	}})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*notifier, error) {
		_ = client.Close(ctx)
		return nil, err
	}
	if err := client.Connect(up, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, ctx); err != nil {
		return fail(err)
	}
	if _, err := client.Load(nil, ctx); err != nil {
		return fail(err)
	}
	ref, err := client.Hold(ctx, statuscontract.SessionStatus, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return fail(err)
	}
	if err := p2runtime.WhenLive(ctx, ref); err != nil {
		ref.Release()
		return fail(err)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	watch, err := ref.Member("statuses").Watch(watchCtx)
	if err != nil {
		cancel()
		ref.Release()
		return fail(err)
	}
	n := &notifier{client: client, ref: ref, busy: map[string]bool{}, err: map[string]string{}}
	n.stop = func() {
		cancel()
		_, _ = watch.Stop()
	}
	n.observe(watch.Value(), report)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			n.observe(call.Value, report)
		}
		return nil
	})
	return n, nil
}

// observe compares statuses with what it saw before: a session that was working and is not any more finished, and a new
// error is a failure. Sessions idle in the first snapshot say nothing.
func (n *notifier) observe(statuses delta.JsonValue, report func(session, kind string)) {
	all, _ := statuses.(*delta.JsonObject)
	if all == nil {
		return
	}
	var events [][2]string
	n.mu.Lock()
	for _, session := range all.Keys() {
		entry, _ := all.Value(session).(*delta.JsonObject)
		if entry == nil {
			continue
		}
		busy, _ := entry.Value("busy").([]any)
		working := len(busy) > 0
		if n.busy[session] && !working {
			events = append(events, [2]string{session, "finished"})
		}
		n.busy[session] = working
		failure, _ := entry.Value("error").(string)
		if failure != "" && failure != n.err[session] {
			events = append(events, [2]string{session, "failed"})
		}
		n.err[session] = failure
	}
	n.mu.Unlock()
	for _, event := range events {
		report(event[0], event[1])
	}
}

// close stops following and closes the client.
func (n *notifier) close() {
	n.stop()
	n.ref.Release()
	_ = n.client.Close(chordctx.Background())
}
