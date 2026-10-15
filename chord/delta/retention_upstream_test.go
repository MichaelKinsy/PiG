package delta

import (
	"runtime"
	"testing"
	"weak"
)

// Ports packages/chord/test/delta-tracker/retention.test.ts and retention.worker.ts onto the overlay Tracker. Upstream spawns Node with --expose-gc and observes WeakRef targets; Go observes weak.Pointer targets after runtime.GC, with the same scenarios and sizes. A WeakRef to an array is a weak pointer to its first element, which keeps the backing array alive exactly as long as the array. A WeakRef to a draft proxy is a weak pointer to the draft node behind the handle, because a Go handle is a small value made per read. The two same-job scenarios compare heap use, as upstream compares process.memoryUsage().heapUsed.

// collect is retention.worker.ts:9-17 collect.
func collect[T any](t *testing.T, reference weak.Pointer[T], what string) {
	t.Helper()
	for range 30 {
		runtime.GC()
		if reference.Value() == nil {
			return
		}
	}
	t.Fatalf("%s: released change data is still retained", what)
}

// assertRetained is retention.worker.ts:42-49 assertRetained.
func assertRetained[T any](t *testing.T, reference weak.Pointer[T], what string) {
	t.Helper()
	for range 5 {
		runtime.GC()
	}
	if reference.Value() == nil {
		t.Fatalf("%s: retained Prepared must retain its immutable revisions", what)
	}
}

func weakArray(t *testing.T, value any) weak.Pointer[any] {
	t.Helper()
	array, ok := value.([]any)
	if !ok || len(array) == 0 {
		t.Fatalf("%T is not a non-empty array", value)
	}
	return weak.Make(&array[0])
}

func valueRows(length int) []any {
	return rows(length, func(value int) any { return JsonObjectOf("value", float64(value)) })
}

func filled(length int, value float64) []any {
	return rows(length, func(int) any { return value })
}

func expectRevoked(t *testing.T, use func(), what string) {
	t.Helper()
	defer func() {
		if recovered, _ := recover().(error); recovered != ErrRevoked {
			t.Fatalf("%s: expected the settled draft to be revoked, got %v", what, recovered)
		}
	}()
	use()
}

// draftNode is the node a handle reaches while its change is open.
func draftNode(ref nodeRef) *node { return ref.ctx.nodes[ref.index-1] }

func heapUsed() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func TestDeltaTrackerRetentionAcrossGCJobs(t *testing.T) {
	nullPayload := func() *Tracker { return Track(JsonObjectOf("payload", nil)) }
	payload := func(length int) *JsonObject { return JsonObjectOf("payload", JsonObjectOf("rows", valueRows(length))) }
	replace := func(t *testing.T, tracker *Tracker, value *JsonObject) {
		t.Helper()
		mustNoErr(t, tracker.Adopt(tracker.PrepareReplace(value)))
	}

	t.Run("validates aborted-payload retention semantics", func(t *testing.T) {
		// retention.worker.ts:19-25, :283-301
		tracker := nullPayload()
		reference := func() weak.Pointer[node] {
			change := tracker.BeginChange()
			mustNoErr(t, change.State().Set("payload", JsonObjectOf("rows", valueRows(50_000))))
			reference := weak.Make(draftNode(change.State().Object("payload").Array("rows").ref))
			change.Abort()
			return reference
		}()
		collect(t, reference, "aborted payload")
		expectJSONText(t, tracker.Value(), `{"payload":null}`)
	})

	t.Run("validates unadopted-prepared retention semantics", func(t *testing.T) {
		// retention.worker.ts:27-32
		tracker := nullPayload()
		reference := func() weak.Pointer[any] {
			change := tracker.BeginChange()
			mustNoErr(t, change.State().Set("payload", JsonObjectOf("rows", valueRows(50_000))))
			return weakArray(t, field(t, mustPrepare(t, change).Value(), "payload", "rows"))
		}()
		collect(t, reference, "unadopted prepared")
		expectJSONText(t, tracker.Value(), `{"payload":null}`)
	})

	t.Run("validates draft-proxies retention semantics", func(t *testing.T) {
		// retention.worker.ts:34-40
		tracker := nullPayload()
		reference := func() weak.Pointer[node] {
			change := tracker.BeginChange()
			mustNoErr(t, change.State().Set("payload", JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)})))
			reference := weak.Make(draftNode(change.State().Object("payload").Array("rows").Object(0).ref))
			change.Abort()
			return reference
		}()
		collect(t, reference, "draft handles")
		expectJSONText(t, tracker.Value(), `{"payload":null}`)
	})

	t.Run("validates settled-lifecycle retention semantics", func(t *testing.T) {
		// retention.worker.ts:51-73
		var retained []any
		trackerReference, future := func() (weak.Pointer[Tracker], weak.Pointer[any]) {
			tracker := nullPayload()
			change := tracker.BeginChange()
			mustNoErr(t, change.State().Set("payload", JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)})))
			retainedPrepared := mustPrepare(t, change)
			mustNoErr(t, tracker.Adopt(retainedPrepared))
			retained = append(retained, change, retainedPrepared)
			futurePrepared := tracker.PrepareReplace(payload(50_000))
			mustNoErr(t, tracker.Adopt(futurePrepared))
			return weak.Make(tracker), weakArray(t, field(t, futurePrepared.Value(), "payload", "rows"))
		}()
		collect(t, trackerReference, "a settled Change or Prepared retains its tracker")
		collect(t, future, "future revision")
		runtime.KeepAlive(retained)
	})

	t.Run("validates obsolete-revisions retention semantics", func(t *testing.T) {
		// retention.worker.ts:273-282
		tracker := Track(payload(50_000))
		oldRoot := weakArray(t, field(t, tracker.Value(), "payload", "rows"))
		for value := range 100 {
			replace(t, tracker, JsonObjectOf("payload", JsonObjectOf("rows", []any{JsonObjectOf("value", float64(value))})))
		}
		collect(t, oldRoot, "obsolete revisions")
		expectJSONText(t, tracker.Value(), `{"payload":{"rows":[{"value":99}]}}`)
	})

	t.Run("validates retained-settled-prepared retention semantics", func(t *testing.T) {
		// retention.worker.ts:74-98
		tracker := Track(payload(50_000))
		var retainedLargePrepared []*Prepared
		base, value := func() (weak.Pointer[any], weak.Pointer[any]) {
			change := tracker.BeginChange()
			mustNoErr(t, change.State().Object("payload").Array("rows").Object(0).Set("value", -1.0))
			prepared := mustPrepare(t, change)
			base, value := weakArray(t, field(t, prepared.Base(), "payload", "rows")), weakArray(t, field(t, prepared.Value(), "payload", "rows"))
			mustNoErr(t, tracker.Adopt(prepared))
			retainedLargePrepared = append(retainedLargePrepared, prepared)
			return base, value
		}()
		replace(t, tracker, JsonObjectOf("payload", JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)})))
		assertRetained(t, base, "base")
		assertRetained(t, value, "value")
		runtime.KeepAlive(retainedLargePrepared)
		retainedLargePrepared = nil
		collect(t, base, "released base")
		collect(t, value, "released value")
	})

	t.Run("validates retained-settled-change retention semantics", func(t *testing.T) {
		// retention.worker.ts:99-121
		tracker := Track(payload(50_000))
		var retainedSettledChanges []*Change
		base, value := func() (weak.Pointer[any], weak.Pointer[any]) {
			change := tracker.BeginChange()
			mustNoErr(t, change.State().Object("payload").Array("rows").Object(0).Set("value", -1.0))
			prepared := mustPrepare(t, change)
			base, value := weakArray(t, field(t, prepared.Base(), "payload", "rows")), weakArray(t, field(t, prepared.Value(), "payload", "rows"))
			mustNoErr(t, tracker.Adopt(prepared))
			retainedSettledChanges = append(retainedSettledChanges, change)
			return base, value
		}()
		replace(t, tracker, JsonObjectOf("payload", JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)})))
		collect(t, base, "base held by a settled change")
		collect(t, value, "value held by a settled change")
		if len(retainedSettledChanges) != 1 {
			t.Fatal("the settled change was dropped")
		}
		runtime.KeepAlive(retainedSettledChanges)
	})

	t.Run("validates retained-settled-proxy retention semantics", func(t *testing.T) {
		// retention.worker.ts:122-143
		tracker := Track(JsonObjectOf("child", JsonObjectOf("value", 0.0), "payload", JsonObjectOf("rows", valueRows(50_000))))
		obsolete := weakArray(t, field(t, tracker.Value(), "payload", "rows"))
		var retainedSettledChanges []*Object
		retainedSettledChanges = append(retainedSettledChanges, func() *Object {
			change := tracker.BeginChange()
			held := change.State().Object("child")
			mustNoErr(t, held.Set("value", 1.0))
			mustNoErr(t, tracker.Adopt(mustPrepare(t, change)))
			return held
		}())
		replace(t, tracker, JsonObjectOf("child", JsonObjectOf("value", 2.0), "payload", JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)})))
		collect(t, obsolete, "revision held by a settled draft")
		expectRevoked(t, func() { retainedSettledChanges[0].Get("value") }, "held child")
	})

	t.Run("validates retained-large-settled-proxy retention semantics", func(t *testing.T) {
		// retention.worker.ts:144-163
		tracker := Track(JsonObjectOf("rows", valueRows(100_000)))
		obsolete := weakArray(t, field(t, tracker.Value(), "rows"))
		var retainedSettledChanges []*Object
		retainedSettledChanges = append(retainedSettledChanges, func() *Object {
			change := tracker.BeginChange()
			held := change.State().Array("rows").Object(0)
			for index := 1; index < 5_000; index++ {
				if got := change.State().Array("rows").Object(index).Get("value"); got != float64(index) {
					t.Fatalf("row %d: %v", index, got)
				}
			}
			mustNoErr(t, held.Set("value", -1.0))
			mustNoErr(t, tracker.Adopt(mustPrepare(t, change)))
			return held
		}())
		replace(t, tracker, JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)}))
		collect(t, obsolete, "revision held by a large settled draft")
		expectRevoked(t, func() { retainedSettledChanges[0].Get("value") }, "held row")
	})

	t.Run("validates retained-large-placement-proxies retention semantics", func(t *testing.T) {
		// retention.worker.ts:164-210
		var retainedSettledChanges []func()
		run := func(kind string) weak.Pointer[any] {
			initialRows := []any{}
			if kind == "override" {
				initialRows = []any{nil}
			}
			tracker := Track(JsonObjectOf("child", JsonObjectOf("value", 0.0), "visited", valueRows(5_000), "first", nil, "second", nil, "rows", initialRows))
			change := tracker.BeginChange()
			visited := change.State().Array("visited")
			for index := range visited.Len() {
				if visited.Object(index).Get("value").(float64) < 0 {
					t.Fatal("visited")
				}
			}
			var held func()
			if kind == "write" || kind == "writes" {
				child := change.State().Object("child")
				held = func() { child.Get("value") }
				mustNoErr(t, change.State().Set("first", filled(50_000, 1)))
				if kind == "writes" {
					mustNoErr(t, change.State().Set("second", filled(50_000, 2)))
				}
			} else {
				rowsDraft := change.State().Array("rows")
				held = func() { rowsDraft.Len() }
				if kind == "insert" {
					_, err := rowsDraft.Push(filled(50_000, 1))
					mustNoErr(t, err)
				} else {
					mustNoErr(t, rowsDraft.Set(0, filled(50_000, 1)))
				}
			}
			prepared := mustPrepare(t, change)
			placed := field(t, prepared.Value(), "first")
			if kind == "insert" || kind == "override" {
				placed = field(t, prepared.Value(), "rows", 0)
			}
			reference := weakArray(t, placed)
			mustNoErr(t, tracker.Adopt(prepared))
			replace(t, tracker, JsonObjectOf("child", JsonObjectOf("value", 1.0), "visited", []any{}, "first", nil, "second", nil, "rows", []any{}))
			retainedSettledChanges = append(retainedSettledChanges, held)
			return reference
		}
		for _, kind := range []string{"write", "writes", "insert", "override"} {
			collect(t, run(kind), kind)
		}
		for _, held := range retainedSettledChanges {
			expectRevoked(t, held, "held placement draft")
		}
	})

	t.Run("validates stale-unprepared-change retention semantics", func(t *testing.T) {
		// retention.worker.ts:211-224
		tracker := Track(payload(50_000))
		stale := tracker.BeginChange()
		mustNoErr(t, stale.State().Object("payload").Array("rows").Object(0).Set("value", -1.0))
		obsolete := weakArray(t, field(t, tracker.Value(), "payload", "rows"))
		replace(t, tracker, JsonObjectOf("payload", JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)})))
		_, err := stale.Prepare()
		if err == nil {
			t.Fatal("a stale change prepared")
		}
		collect(t, obsolete, "revision held by a stale change")
		runtime.KeepAlive(stale)
	})

	t.Run("validates same-job-fast-cleanup retention semantics", func(t *testing.T) {
		// retention.worker.ts:225-242
		tracker := Track(JsonObjectOf("rows", valueRows(100_000)))
		readyHeap := heapUsed()
		for value := range 100 {
			change := tracker.BeginChange()
			draft := change.State().Array("rows")
			for index := range 4_094 {
				if got := draft.Object(index).Get("value"); got != float64(index) {
					t.Fatalf("row %d: %v", index, got)
				}
			}
			mustNoErr(t, draft.Object(draft.Len()-1).Set("value", float64(-value-1)))
			mustNoErr(t, tracker.Adopt(mustPrepare(t, change)))
		}
		if retainedMiB := float64(int64(heapUsed())-int64(readyHeap)) / (1 << 20); retainedMiB >= 16 {
			t.Fatalf("same-job cleanup retained %.2f MiB", retainedMiB)
		}
		if got := field(t, tracker.Value(), "rows", 99_999, "value"); got != -100.0 {
			t.Fatalf("last row %v", got)
		}
	})

	t.Run("validates same-job-folded-ops-cleanup retention semantics", func(t *testing.T) {
		// retention.worker.ts:243-262
		wide := JsonObjectOf()
		for index := range 5_000 {
			wide.Set("field"+itoa(index), 0.0)
		}
		tracker := Track(wide)
		readyHeap := heapUsed()
		for value := 1; value <= 100; value++ {
			change := tracker.BeginChange()
			for index := range 5_000 {
				mustNoErr(t, change.State().Set("field"+itoa(index), float64(value)))
			}
			prepared := mustPrepare(t, change)
			if len(prepared.Ops()) != 1 || Verb(prepared.Ops()[0]) != "r" {
				t.Fatalf("ops %d", len(prepared.Ops()))
			}
			mustNoErr(t, tracker.Adopt(prepared))
		}
		if retainedMiB := float64(int64(heapUsed())-int64(readyHeap)) / (1 << 20); retainedMiB >= 8 {
			t.Fatalf("same-job folded operations retained %.2f MiB", retainedMiB)
		}
		if got := tracker.Value().Value("field0"); got != 100.0 {
			t.Fatalf("field0 %v", got)
		}
	})

	t.Run("validates lifecycle-churn retention semantics", func(t *testing.T) {
		// retention.worker.ts:263-272. Upstream releases an aborted change's registration (tracker.ts abortContext, clearContext); the registry bound is the observable Go form.
		tracker := Track(jsonObject(t, `{"value":0}`))
		for index := range 100_000 {
			change := tracker.BeginChange()
			mustNoErr(t, change.State().Set("value", float64(index)))
			change.Abort()
		}
		if live := len(tracker.owner.contexts); live > 256 {
			t.Fatalf("the tracker still registers %d aborted changes", live)
		}
		replace(t, tracker, jsonObject(t, `{"value":1}`))
		expectJSONText(t, tracker.Value(), `{"value":1}`)
	})
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for ; value > 0; value /= 10 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
	}
	return string(digits)
}

// upstream: packages/chord/src/delta/tracker.ts:216 (#contexts = new Set<WeakRef<OverlayContext>>()): the tracker holds an open change weakly, so a change the caller drops without settling it is reclaimed with its draft before any adoption.
func TestTrackerReleasesADroppedOpenChange(t *testing.T) {
	tracker := Track(jsonObject(t, `{"payload":null}`))
	reference := func() weak.Pointer[node] {
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("payload", JsonObjectOf("rows", valueRows(50_000))))
		return weak.Make(draftNode(change.State().Object("payload").Array("rows").ref))
	}()
	collect(t, reference, "a dropped open change")
	expectJSONText(t, tracker.Value(), `{"payload":null}`)
}
