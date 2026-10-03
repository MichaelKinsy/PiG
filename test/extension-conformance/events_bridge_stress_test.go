package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	busStressSubscribers = 100
	busStressEmits       = 1000
	// busStressHeapPerEmit is the per-emit heap growth the run must stay under: 64 MiB per 10000 emits, so the bound scales with busStressEmits and keeps the same sensitivity to per-emit retention.
	busStressHeapPerEmit = (64 << 20) / 10000
)

// One hundred subscribers over four realms (two node processes, two native), one thousand emits from all four realms at once, and one slow native listener. Every delivery arrives exactly once, and the Host and the test process hold no more memory or goroutines afterwards (upstream's bus keeps nothing per emit: event-bus.ts:12-33).
func TestNativeEventBusStressKeepsEveryDeliveryAndBoundedState(t *testing.T) {
	for _, language := range busNativeLanguages {
		t.Run(language, func(t *testing.T) {
			perRealm := busStressSubscribers / 4
			realms := []busFixture{
				busSpec("node", "isolated", "n1"),
				busSpec("node", "isolated", "n2"),
				busSpec(language, "isolated", "g1"),
				busSpec(language, "isolated", "g2"),
			}
			rig := newBusRig(t, realms...)
			for _, realm := range realms {
				for range perRealm {
					rig.must(realm.Name, "sub", "s count")
				}
			}
			rig.must("g1", "sub", "s sleep=1")
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			goroutinesBefore := runtime.NumGoroutine()
			retainedBefore := rig.host.EventBusRetention()
			if retainedBefore.Listeners != busStressSubscribers+1 || retainedBefore.Handlers != retainedBefore.Listeners {
				t.Fatalf("registry before the run = %+v, want %d listeners with one handler each", retainedBefore, busStressSubscribers+1)
			}

			var wg sync.WaitGroup
			errs := make(chan error, len(realms))
			for _, realm := range realms {
				wg.Go(func() { errs <- rig.run(realm.Name, "burst", fmt.Sprintf("s %d", busStressEmits/len(realms))) })
			}
			finished := make(chan struct{})
			go func() { wg.Wait(); close(finished) }()
			select {
			case <-finished:
			case <-time.After(40 * time.Minute):
				t.Fatal("the stress run did not finish")
			}
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, realm := range realms {
				rig.must(realm.Name, "report", "")
			}
			want := perRealm * busStressEmits
			seen := map[string]int{}
			for _, line := range rig.lines() {
				name, rest, _ := strings.Cut(line, "|")
				_, payload, _ := strings.Cut(rest, "|")
				var counts map[string]int
				if err := json.Unmarshal([]byte(payload), &counts); err != nil {
					t.Fatalf("report line %q: %v", line, err)
				}
				seen[name] = counts["s"]
			}
			for _, realm := range realms {
				if seen[realm.Name] != want {
					t.Fatalf("%s received %d deliveries, want %d", realm.Name, seen[realm.Name], want)
				}
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			// Upstream's bus keeps nothing per emit (event-bus.ts:12-33): every emission, dispatch snapshot and cross-process reference the run created is gone, and the registry is what it was before the first emit.
			// The owners' unpin notifications that release the payload leases are asynchronous, so the ledger drains after the last delivery. A lease that never drains is the leak, and the deadline reports it.
			drained := time.Now().Add(30 * time.Second)
			for rig.host.EventBusRetention() != retainedBefore && time.Now().Before(drained) {
				time.Sleep(10 * time.Millisecond)
			}
			if retained := rig.host.EventBusRetention(); retained != retainedBefore || retained.Emissions != 0 || retained.Snapshots != 0 {
				t.Fatalf("Host retains %+v after %d emits, want exactly the pre-run state %+v with no emission or snapshot", retained, busStressEmits, retainedBefore)
			}
			if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > int64(busStressEmits)*busStressHeapPerEmit {
				t.Fatalf("heap grew by %d bytes across %d emits", grown, busStressEmits)
			}
			if grown := runtime.NumGoroutine() - goroutinesBefore; grown > 16 {
				t.Fatalf("goroutines grew by %d across %d emits", grown, busStressEmits)
			}
		})
	}
}

// A native listener that never returns blocks its emitter, as a hung listener blocks upstream's emit (event-bus.ts:15-17), and nothing else: another realm emits and receives on other channels, and the emitter completes once the listener returns.
func TestNativeEventBusHungNativeListenerBlocksOnlyItsEmitter(t *testing.T) {
	t.Parallel()
	for _, language := range busNativeLanguages {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			rig := newBusRig(t,
				busSpec("node", "isolated", "emitter"),
				busSpec(language, "isolated", "hung", busListenerAt{"hang", "hang"}),
				busSpec("node", "isolated", "other"),
				busSpec(language, "isolated", "bystander", busListenerAt{"free", "record"}),
			)
			blocked := make(chan error, 1)
			go func() { blocked <- rig.run("emitter", "emit", "hang json 1") }()
			select {
			case err := <-blocked:
				t.Fatalf("the emit returned while its listener was still running: %v", err)
			case <-time.After(500 * time.Millisecond):
			}
			rig.must("other", "emit", "free json 2")
			rig.expect(`bystander|free|2`)
			rig.must("bystander", "emit", "free json 3")
			rig.expect(`bystander|free|2`, `bystander|free|3`)
			if err := os.WriteFile(rig.log+".release", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-blocked:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the emit stayed blocked after its listener returned")
			}
		})
	}
}
