package pico3

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestWatchConvergesAndStops(t *testing.T) {
	env := openEnv(t, openOptions{})
	collector := collectWatch(t, env.root)
	for _, text := range []string{"one", "two"} {
		env.wait(env.send(env.root, text))
	}
	env.idle()
	_, err := env.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	folded := collector.view
	for index, envelope := range collector.Envelopes() {
		equal(t, envelope.Revision, index+1, "contiguous revision")
		folded = must(ApplyEnvelope(folded, envelope))
	}
	fresh := must(env.root.Watch(bg))
	defer fresh.Stop()
	equal(t, folded, fresh.View, "folded view")
	collector.watch.Stop()
	collector.watch.Stop()
	delivered := len(collector.Envelopes())
	_, err = env.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	equal(t, len(collector.Envelopes()), delivered, "stopped watch")
}

func TestWatchListenerFailureAndCapacity(t *testing.T) {
	var reports []error
	env := openEnv(t, openOptions{onReport: func(err error) { reports = append(reports, err) }})
	bad := must(env.root.Watch(bg))
	good := must(env.root.Watch(bg))
	defer good.Stop()
	count := 0
	bad.Start(func(*Envelope) { panic("bad listener") })
	good.Start(func(*Envelope) { count++ })
	_, err := env.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	if !bad.Closed() || good.Closed() || count != 1 || len(reports) != 1 || !strings.Contains(reports[0].Error(), "bad listener") {
		t.Fatalf("watch failure isolation: %v %v %d %v", bad.Closed(), good.Closed(), count, reports)
	}
	buffered := must(env.root.Watch(bg))
	for range WatchCapacity + 1 {
		_, err = env.root.Write(bg, NewEntry{Kind: "note"})
		check(t, err)
	}
	if !buffered.Closed() || !strings.Contains(reports[len(reports)-1].Error(), "capacity 256 exceeded") {
		t.Fatalf("overflow: %v", reports)
	}
	fresh := must(env.root.Watch(bg))
	defer fresh.Stop()
	equal(t, len(arr(fresh.View, "entries")), WatchCapacity+2, "fresh after overflow")
}

func TestWatchStartOrdersConcurrentDelivery(t *testing.T) {
	watch := &Watch{onStop: func() {}, onReport: func(err error) { t.Error(err) }}
	watch.accept(&Envelope{Revision: 1})
	watch.accept(&Envelope{Revision: 2})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var revisions []int
	go func() {
		defer close(done)
		watch.Start(func(envelope *Envelope) {
			if envelope.Revision == 1 {
				close(entered)
				<-release
			}
			mu.Lock()
			revisions = append(revisions, envelope.Revision)
			mu.Unlock()
		})
	}()
	<-entered
	watch.accept(&Envelope{Revision: 3})
	close(release)
	<-done
	equal(t, revisions, []int{1, 2, 3}, "buffered order")
	watch.Stop()
}

func TestWatchProjectionFailureIsReported(t *testing.T) {
	reports := make(chan error, 2)
	fail := false
	env := openEnv(t, openOptions{onReport: func(err error) { reports <- err }})
	namespace := must(env.h.Namespace("project", NamespaceDefaults{Sticky: JsonObject{"value": 0}}, func(value JsonObject) (JsonValue, error) {
		if fail {
			return nil, errors.New("projection failed")
		}
		return value, nil
	}))
	watch := must(env.root.Watch(bg))
	watch.Start(func(*Envelope) {})
	fail = true
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		node, err := tx.Plugins(namespace)
		if err != nil {
			return nil, err
		}
		node.Set("value", 1)
		return nil, nil
	})
	check(t, err)
	if !watch.Closed() {
		t.Fatal("failed projection left watch open")
	}
	select {
	case err := <-reports:
		if !strings.Contains(err.Error(), "projection failed") {
			t.Fatal(err)
		}
	default:
		t.Fatal("missing report")
	}
}
