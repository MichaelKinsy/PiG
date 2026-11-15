package host

import (
	"fmt"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Support of host_tiling_test.go and host_list_test.go, from packages/chord/test/support.ts at Pi 0db9861ec018.
//
// What Pi's support.ts gives these two files and where Go has it already (do NOT redraft):
//   changesOf  -> nexttest.ChangesOf  (chord/next/internal/nexttest/watch.go:56)
//   framesOf   -> nexttest.FramesOf   (watch.go:44)
//   valuesOf   -> nexttest.ValuesOf   (watch.go:70)
//   delivered  -> deliveredOn(host)   (host_reference_support_test.go:21)
//   flushed    -> flushed(host)       (host_events_support_test.go:195)
//   expectCollected -> expectCollected / collectedWithin (host_attempt_support_test.go:207, jobs_support_test.go:366)
//   memoryStore -> memoryStore(t) (host_next_support_test.go:382); faultyStore(t) gives holdNextCommit (:559)
// What is here: instancesOf (as `instances`), Promise.all (`parallel`), mulberry32 (`random`), the JSON readers of a
// list value.

// instances is `instancesOf(Class, method)`: weak references to every instance a test's factory handed over (Pi patches the
// prototype's method; Go has no prototype, the test's factory records what `self.List` / `ownQueries` returned: listOf,
// ownQueries).
type instances[T any] struct {
	mu   sync.Mutex
	refs []weak.Pointer[T]
}

func (i *instances[T]) add(instance *T) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.refs = append(i.refs, weak.Make(instance))
}

// count is `instances.length`.
func (i *instances[T]) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.refs)
}

// at is `instances.at(index)`; a negative index counts from the end.
func (i *instances[T]) at(index int) weak.Pointer[T] {
	i.mu.Lock()
	defer i.mu.Unlock()
	if index < 0 {
		index += len(i.refs)
	}
	return i.refs[index]
}

// listOf is the list a handle of `self.List` stands for (a live attempt's).
func listOf(handle *ListHandle) *list { return handle.list }

// lsSettled waits until everything the lists of the host started on their own ran: the reads their readers queued for a source
// that moved (qrSettled), the loads of their pages and the reads a rebound started (`list.idle`), and then the frames
// published and delivered: what Pi's `await flushed()` lets the microtasks do, there being no I/O to wait for there.
func lsSettled(host Host) {
	qrSettled(host)
	for _, l := range listsOf(host) {
		l.idle()
	}
	flushed(host)
	deliveredOn(host)
}

// listsOf are the lists of the host's attempts.
func listsOf(host Host) []*list {
	var lists []*list
	hostTurn(host, func() {
		for _, object := range host.(*chordHost).objectList() {
			for _, each := range []*attempt{object.phase.attempt, object.exposed, object.old, object.kept, object.showsReadiness} {
				if each == nil {
					continue
				}
				for _, reader := range each.readers {
					if l, ok := reader.reading.(*list); ok {
						lists = append(lists, l)
					}
				}
			}
		}
	})
	return lists
}

// describe is what a list shows of its tilings, for a failure's message: each page's bounds, rows and state.
func (l *list) describe() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := ""
	for name, tiling := range map[string]*tiling{"current": l.current, "next": l.next} {
		if tiling == nil {
			continue
		}
		tiling.mu.Lock()
		out += "\n" + name + ":"
		for _, page := range tiling.pages {
			ids := []any{}
			for _, row := range page.rows {
				ids = append(ids, row["id"])
			}
			out += fmt.Sprintf("\n  (%v, %v] ran=%v more=%v failure=%v rows=%v", page.after, page.through, page.ran, page.more, page.failure, ids)
		}
		tiling.mu.Unlock()
	}
	return out
}

// parallel is `Promise.all([...calls started together])`: every call runs on its own goroutine, all have returned when it does.
func parallel(calls ...func()) {
	var group sync.WaitGroup
	for _, call := range calls {
		group.Add(1)
		go func() {
			defer group.Done()
			call()
		}()
	}
	group.Wait()
}

// firstError is the first non-nil of errs (a facet's serves in turn).
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// random is `random(seed)`: the small deterministic generator (mulberry32) of both Pi tests, step for step.
type random struct{ state uint32 }

func newRandom(seed uint32) *random { return &random{state: seed} }

func (r *random) next() float64 {
	r.state += 0x6d2b79f5
	t := r.state
	t = (t ^ t>>15) * (t | 1)
	t ^= t + (t^t>>7)*(t|61)
	return float64(t^t>>14) / 4294967296
}

// int is `r.int(below)`.
func (r *random) int(below int) int { return int(r.next() * float64(below)) }

// chance is `r.chance(p)`.
func (r *random) chance(p float64) bool { return r.next() < p }

// listMemberValue is `ref.list.value`: the list member's rows and `moreAfter`; its error is Pi's throw (a failed list).
func listMemberValue(ref *Reference) (rows []any, moreAfter bool, err error) {
	value, err := ref.Member("list").Value()
	if err != nil {
		return nil, false, err
	}
	object := value.(*delta.JsonObject)
	return object.Value("rows").([]any), object.Value("moreAfter").(bool), nil
}

// mustListValue is listMemberValue where a failure fails the test (Pi: `screen.list.value` thrown inside an expectation).
func mustListValue(t testing.TB, ref *Reference) (rows []any, moreAfter bool) {
	t.Helper()
	rows, moreAfter, err := listMemberValue(ref)
	if err != nil {
		t.Fatalf("list.value: %v", err)
	}
	return rows, moreAfter
}

// rowIDs are the `id`s of a list's rows, numbers (the rows/list tests) ...
func rowIDs(rows []any) []int { return idsOf(rows) }

// ... or strings (the sessions' ids of the tiling test).
func sessionIDs(rows []any) []string {
	ids := []string{}
	for _, row := range rows {
		ids = append(ids, row.(*delta.JsonObject).Value("id").(string))
	}
	return ids
}

// calls is `ref.member(args, context)` of Pi, as one call: the error fails the test.
func mustCall(t testing.TB, ref *Reference, method string, args delta.JsonValue) {
	t.Helper()
	if _, err := ref.Call(method, args, background); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

var _ = fmt.Sprint

// callsIDs is `calls.map((call) => (call.kind === "value" ? ids(call.value) : call.kind))`.
func callsIDs(calls []chord.WatchCall[delta.JsonValue]) []any {
	out := []any{}
	for _, call := range calls {
		if call.Kind == chord.WatchCallValue {
			out = append(out, rowIDs(call.Value.(*delta.JsonObject).Value("rows").([]any)))
		} else {
			out = append(out, fmt.Sprint(call.Kind))
		}
	}
	return out
}
