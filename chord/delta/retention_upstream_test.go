package delta

import (
	"runtime"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/delta-tracker/retention.test.ts and retention.worker.ts. Upstream spawns Node with --expose-gc and WeakRef; Go observes the same retention contracts through the heap after runtime.GC, and through weak.Pointer for the tracker itself. Each case builds a payload of 100,000 rows (several megabytes) and asserts the heap returns to within a quarter of the payload once the last holder lets go, or stays above it while a holder remains.

const retentionRows = 100_000

func bigPayload() any {
	return chordjson.ObjectOf("rows", rows(retentionRows, func(at int) any { return chordjson.ObjectOf("value", float64(at)) }))
}

func heapInUse() uint64 {
	var stats runtime.MemStats
	for range 3 {
		runtime.GC()
	}
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// payloadBytes is what one bigPayload costs on the heap, measured rather than assumed.
func payloadBytes() uint64 {
	before := heapInUse()
	payload := bigPayload()
	after := heapInUse()
	runtime.KeepAlive(payload)
	return after - before
}

func expectReleased(t *testing.T, baseline, payload uint64, what string) {
	t.Helper()
	if now := heapInUse(); now > baseline+payload/4 {
		t.Fatalf("%s: released change data is still retained (%d bytes over baseline, payload %d)", what, now-baseline, payload)
	}
}

// expectDropped asserts that a payload counted in baseline has been released.
func expectDropped(t *testing.T, baseline, payload uint64, what string) {
	t.Helper()
	if now := heapInUse(); int64(now) > int64(baseline)-int64(payload)/2 {
		t.Fatalf("%s: released change data is still retained (%d bytes below baseline, payload %d)", what, int64(baseline)-int64(now), payload)
	}
}

// expectKept asserts that a payload counted in baseline is still held.
func expectKept(t *testing.T, baseline, payload uint64, what string) {
	t.Helper()
	if now := heapInUse(); int64(now) < int64(baseline)-int64(payload)/4 {
		t.Fatalf("%s: a retained value must retain its immutable revisions (%d bytes below baseline, payload %d)", what, int64(baseline)-int64(now), payload)
	}
}

func TestDeltaTrackerRetentionAcrossGCJobs(t *testing.T) {
	payload := payloadBytes()
	if payload < 1<<20 {
		t.Fatalf("payload too small to observe: %d", payload)
	}

	t.Run("validates aborted-payload retention semantics", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"payload":null}`))
		baseline := heapInUse()
		change := tracker.BeginChange()
		obj(t, mustState(t, change)).Set("payload", bigPayload())
		change.Abort()
		expectReleased(t, baseline, payload, "aborted")
		expectJSON(t, tracker.Value(), `{"payload":null}`)
	})

	t.Run("validates unadopted-prepared retention semantics", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"payload":null}`))
		baseline := heapInUse()
		func() {
			change := tracker.BeginChange()
			obj(t, mustState(t, change)).Set("payload", bigPayload())
			_ = mustPrepareCopy(t, change)
		}()
		expectReleased(t, baseline, payload, "unadopted prepared")
		expectJSON(t, tracker.Value(), `{"payload":null}`)
	})

	t.Run("validates draft-proxies retention semantics", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"payload":null}`))
		baseline := heapInUse()
		change := tracker.BeginChange()
		obj(t, mustState(t, change)).Set("payload", chordjson.ObjectOf("rows", []any{chordjson.ObjectOf("value", 1.0)}))
		change.Abort()
		expectReleased(t, baseline, payload, "draft handles")
		expectJSON(t, tracker.Value(), `{"payload":null}`)
	})

	t.Run("validates settled-lifecycle retention semantics", func(t *testing.T) {
		var retained []any
		reference, baseline := func() (weak.Pointer[copyTracker], uint64) {
			tracker := trackCopy(parse(t, `{"payload":null}`))
			baseline := heapInUse()
			change := tracker.BeginChange()
			obj(t, mustState(t, change)).Set("payload", chordjson.ObjectOf("rows", []any{chordjson.ObjectOf("value", 1.0)}))
			retainedPrepared := mustPrepareCopy(t, change)
			expectNoErr(t, tracker.Adopt(retainedPrepared))
			retained = append(retained, change, retainedPrepared)
			future := tracker.PrepareReplace(chordjson.ObjectOf("payload", bigPayload()))
			expectNoErr(t, tracker.Adopt(future))
			return weak.Make(tracker), baseline
		}()
		runtime.GC()
		if reference.Value() != nil {
			t.Fatal("a settled Change or Prepared retains its tracker")
		}
		expectReleased(t, baseline, payload, "settled lifecycle")
		runtime.KeepAlive(retained)
	})

	t.Run("validates obsolete-revisions retention semantics", func(t *testing.T) {
		baseline := heapInUse()
		tracker := trackCopy(chordjson.ObjectOf("payload", bigPayload()))
		for value := range 100 {
			expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(chordjson.ObjectOf("payload", chordjson.ObjectOf("rows", []any{chordjson.ObjectOf("value", float64(value))})))))
		}
		expectReleased(t, baseline, payload, "obsolete revisions")
		expectJSON(t, tracker.Value(), `{"payload":{"rows":[{"value":99}]}}`)
	})

	t.Run("validates retained-settled-prepared retention semantics", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("payload", bigPayload()))
		baseline := heapInUse()
		var retained []*copyPrepared
		func() {
			change := tracker.BeginChange()
			obj(t, arr(t, obj(t, obj(t, mustState(t, change)).Value("payload")).Value("rows"))[0]).Set("value", -1.0)
			prepared := mustPrepareCopy(t, change)
			expectNoErr(t, tracker.Adopt(prepared))
			retained = append(retained, prepared)
		}()
		expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"payload":{"rows":[{"value":1}]}}`))))
		expectKept(t, baseline, payload, "retained settled prepared")
		runtime.KeepAlive(retained) // len alone does not keep the backing array live
		retained = nil
		expectDropped(t, baseline, payload, "released settled prepared")
	})

	t.Run("validates retained-settled-change retention semantics", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("payload", bigPayload()))
		baseline := heapInUse()
		var retained []*copyChange
		func() {
			change := tracker.BeginChange()
			obj(t, arr(t, obj(t, obj(t, mustState(t, change)).Value("payload")).Value("rows"))[0]).Set("value", -1.0)
			prepared := mustPrepareCopy(t, change)
			expectNoErr(t, tracker.Adopt(prepared))
			retained = append(retained, change)
		}()
		expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"payload":{"rows":[{"value":1}]}}`))))
		expectDropped(t, baseline, payload, "retained settled change")
		if len(retained) != 1 {
			t.Fatal("change dropped")
		}
	})

	t.Run("validates retained-settled-proxy retention semantics", func(t *testing.T) {
		// A held child handle cannot be revoked in Go; the retained change is the observable handle.
		tracker := trackCopy(chordjson.ObjectOf("child", chordjson.ObjectOf("value", 0.0), "payload", bigPayload()))
		baseline := heapInUse()
		var retained []*copyChange
		func() {
			change := tracker.BeginChange()
			obj(t, obj(t, mustState(t, change)).Value("child")).Set("value", 1.0)
			expectNoErr(t, tracker.Adopt(mustPrepareCopy(t, change)))
			retained = append(retained, change)
		}()
		expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"child":{"value":2},"payload":{"rows":[{"value":1}]}}`))))
		expectDropped(t, baseline, payload, "retained settled handle")
		_, err := retained[0].State()
		expectErr(t, err, "settled")
	})

	t.Run("validates retained-large-settled-proxy retention semantics", func(t *testing.T) {
		tracker := trackCopy(bigPayload())
		baseline := heapInUse()
		var retained []*copyChange
		func() {
			change := tracker.BeginChange()
			rowsValue := arr(t, obj(t, mustState(t, change)).Value("rows"))
			for at := 1; at < 5_000; at++ {
				if obj(t, rowsValue[at]).Value("value") != float64(at) {
					t.Fatalf("row %d", at)
				}
			}
			obj(t, rowsValue[0]).Set("value", -1.0)
			expectNoErr(t, tracker.Adopt(mustPrepareCopy(t, change)))
			retained = append(retained, change)
		}()
		expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"rows":[{"value":1}]}`))))
		expectDropped(t, baseline, payload, "retained large settled handle")
		_, err := retained[0].State()
		expectErr(t, err, "settled")
	})

	t.Run("validates retained-large-placement-proxies retention semantics", func(t *testing.T) {
		for _, kind := range []string{"write", "writes", "insert", "override"} {
			tracker := trackCopy(parse(t, `{"child":{"value":0},"first":null,"second":null,"rows":[null]}`))
			baseline := heapInUse()
			var retained []*copyChange
			func() {
				change := tracker.BeginChange()
				state := obj(t, mustState(t, change))
				big := func() any { return rows(500_000, func(int) any { return 1.0 }) }
				switch kind {
				case "write":
					state.Set("first", big())
				case "writes":
					state.Set("first", big())
					state.Set("second", big())
				case "insert":
					state.Set("rows", append(arr(t, state.Value("rows")), big()))
				default:
					arr(t, state.Value("rows"))[0] = big()
				}
				expectNoErr(t, tracker.Adopt(mustPrepareCopy(t, change)))
				expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"child":{"value":1},"first":null,"second":null,"rows":[]}`))))
				retained = append(retained, change)
			}()
			expectReleased(t, baseline, 4<<20, kind)
			_, err := retained[0].State()
			expectErr(t, err, "settled")
		}
	})

	t.Run("validates stale-unprepared-change retention semantics", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("payload", bigPayload()))
		baseline := heapInUse()
		stale := tracker.BeginChange()
		obj(t, arr(t, obj(t, obj(t, mustState(t, stale)).Value("payload")).Value("rows"))[0]).Set("value", -1.0)
		expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"payload":{"rows":[{"value":1}]}}`))))
		_, err := stale.Prepare()
		expectErr(t, err, "settled")
		expectDropped(t, baseline, payload, "stale unprepared change")
		runtime.KeepAlive(stale)
	})

	t.Run("validates same-job-fast-cleanup retention semantics", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("rows", rows(5_000, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		ready := heapInUse()
		for value := range 100 {
			change := tracker.BeginChange()
			values := arr(t, obj(t, mustState(t, change)).Value("rows"))
			obj(t, values[len(values)-1]).Set("value", float64(-value-1))
			expectNoErr(t, tracker.Adopt(mustPrepareCopy(t, change)))
		}
		if retained := float64(int64(heapInUse())-int64(ready)) / (1 << 20); retained > 16 {
			t.Fatalf("same-job cleanup retained %.2f MiB", retained)
		}
		values := arr(t, obj(t, tracker.Value()).Value("rows"))
		if obj(t, values[len(values)-1]).Value("value") != -100.0 {
			t.Fatalf("last=%v", values[len(values)-1])
		}
	})

	t.Run("validates same-job-folded-ops-cleanup retention semantics", func(t *testing.T) {
		wide := chordjson.NewObject(0)
		for at := range 5_000 {
			wide.Set(sprintField(at), 0.0)
		}
		tracker := trackCopy(wide)
		ready := heapInUse()
		for value := 1; value <= 100; value++ {
			change := tracker.BeginChange()
			state := obj(t, mustState(t, change))
			for key := range state.All() {
				state.Set(key, float64(value))
			}
			prepared := mustPrepareCopy(t, change)
			if len(prepared.Ops) != 1 || Verb(prepared.Ops[0]) != "r" {
				t.Fatalf("ops=%d", len(prepared.Ops))
			}
			expectNoErr(t, tracker.Adopt(prepared))
		}
		if retained := float64(int64(heapInUse())-int64(ready)) / (1 << 20); retained > 8 {
			t.Fatalf("same-job folded operations retained %.2f MiB", retained)
		}
		if obj(t, tracker.Value()).Value("field0") != 100.0 {
			t.Fatal("field0")
		}
	})

	t.Run("validates lifecycle-churn retention semantics", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"value":0}`))
		for at := range 100_000 {
			change := tracker.BeginChange()
			obj(t, mustState(t, change)).Set("value", float64(at))
			change.Abort()
		}
		expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"value":1}`))))
		expectJSON(t, tracker.Value(), `{"value":1}`)
		if len(tracker.live) > 1_024 {
			t.Fatalf("tracker still registers %d aborted changes", len(tracker.live))
		}
	})
}

func sprintField(at int) string { return "field" + itoa(at) }

func itoa(at int) string {
	if at == 0 {
		return "0"
	}
	var digits []byte
	for ; at > 0; at /= 10 {
		digits = append([]byte{byte('0' + at%10)}, digits...)
	}
	return string(digits)
}
