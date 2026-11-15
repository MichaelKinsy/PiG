package host

import (
	"fmt"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"
	"github.com/MichaelKinsy/PiG/chord/next/store"
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
// What is here: instancesOf (as `instances`), Promise.all (`parallel`; `commitLog.start` where the calls make commits and
// their order matters), mulberry32 (`random`), the JSON readers of a list value.

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

// lsSettled stands for Pi's `await flushed()` (packages/chord/test/support.ts:380-383: `new Promise((resolve) => setImmediate(resolve))`,
// "waits until every pending promise callback has run"): it resolves once every microtask chain the code under test started has
// run, the reads and loads of the lists and readers (`void this.read`, `load()`, the re-reads of a moved source) and the frames
// they publish; Pi's memory store does no I/O, so those chains end in microtasks. lsSettled awaits the same set: the reads the
// readers queued for a source that moved (qrSettled), the host's loop's tasks (deliveredOn: the loads of the lists' pages and the
// reads a rebound started are tasks of that loop), then the host's turn queue (flushed) and the watch deliveries (deliveredOn).
// A call a test made has settled when it returned, as Pi's awaited call has: lsSettled waits for what the lists started on their
// own after it.
func lsSettled(host Host) {
	// First the tasks of the loop (a reload's successor starts in one and makes its lists and reads, a list's loads are tasks):
	// what the reads below wait for exists once they ran.
	deliveredOn(host)
	qrSettled(host)
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
		group.Go(func() {
			call()
		})
	}
	group.Wait()
}

// commitLog is what the store's commits tell a test (Pi's tests replace `store.commit`; Go has `storeseam.WrapAdopt`): how
// many enlisted, and the groups its OnCommitted listener reported: one notification for several seq numbers is a group
// (`seq - last > 1`).
type commitLog struct {
	mu       sync.Mutex
	changed  *sync.Cond
	enlisted int
	groups   int
	last     int64
	reported int64
}

func newCommitLog(last int64) *commitLog {
	c := &commitLog{last: last, reported: last}
	c.changed = sync.NewCond(&c.mu)
	return c
}

func (c *commitLog) onCommitted(seq int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq-c.last > 1 {
		c.groups++
	}
	c.last, c.reported = seq, seq
	c.changed.Broadcast()
}

func (c *commitLog) enlist() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enlisted++
	c.changed.Broadcast()
}

// waitReported waits until the listener reported seq.
func (c *commitLog) waitReported(seq int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.reported < seq {
		c.changed.Wait()
	}
}

func (c *commitLog) count() int   { c.mu.Lock(); defer c.mu.Unlock(); return c.enlisted }
func (c *commitLog) grouped() int { c.mu.Lock(); defer c.mu.Unlock(); return c.groups }

// start starts call on a goroutine of group, in the order Pi's calls are made in. A call that makes a commit (`commits`) is
// waited for until that commit enlisted, so the next call's commit queues behind it: Pi's calls of one turn enlist in call
// order, and the queue adopts in the order of the enlisting, so the truth a test tracks call by call is the store's.
func (c *commitLog) start(group *sync.WaitGroup, commits bool, call func()) {
	before, finished := c.count(), false
	group.Go(func() {
		call()
		c.mu.Lock()
		finished = true
		c.changed.Broadcast()
		c.mu.Unlock()
	})
	if !commits {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.enlisted <= before && !finished { // a call that fails before its commit enlists does not hold the test
		c.changed.Wait()
	}
}

// startInOrder starts calls that each make one commit, each once the commit of the one before enlisted; the returned func
// waits for all of them.
func (c *commitLog) startInOrder(calls ...func()) (wait func()) {
	var group sync.WaitGroup
	for _, call := range calls {
		c.start(&group, true, call)
	}
	return group.Wait
}

// countCommits starts a commitLog over a store from its current seq on: every commit that takes its place in the store's queue
// is counted (`storeseam.WrapAdopt`, the adoption itself unchanged).
func countCommits(chordStore *store.ChordStore) *commitLog {
	log := newCommitLog(chordStore.LastSeq())
	storeseam.WrapAdopt(chordStore, func(adopt storeseam.Adopt) storeseam.Adopt {
		log.enlist()
		return adopt
	})
	return log
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
