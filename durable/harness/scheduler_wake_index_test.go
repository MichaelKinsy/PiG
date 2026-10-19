package harness

import (
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// wakeWalkLocked is the Wake computation before the wake index: a walk of every live task.
func (s *TaskScheduler) wakeWalkLocked() Wake {
	now := s.options.Now()
	active := len(s.invocations) > 0 || s.reconcilingWork > 0 || s.reconcileWorkLocked()
	var at *float64
	var snapshot durable.RegistrySnapshot
	snapshotFailed := false
	for _, record := range s.liveValuesLocked() {
		state := record.State
		if state.Status == durable.TaskCompleting {
			continue
		}
		if state.Status == durable.TaskWaiting && !record.AbortRequested && slices.ContainsFunc(state.On, func(id durable.TaskId) bool { _, live := s.live[id]; return live }) {
			continue
		}
		if state.Status == durable.TaskSleeping && !record.AbortRequested && state.Until != nil && *state.Until > now {
			if at == nil || *state.Until < *at {
				until := *state.Until
				at = &until
			}
			continue
		}
		if active {
			continue
		}
		if s.parkedLocked(record.AbortRequested, record.Restarts) {
			continue
		}
		if record.AbortRequested && len(s.waitingOnLocked(record, true)) > 0 {
			continue
		}
		if !record.AbortRequested || record.AbortReason != "" {
			if snapshot == nil && !snapshotFailed {
				snapshot = s.snapshotOrReportLocked()
				snapshotFailed = snapshot == nil
			}
			if snapshot != nil && s.fitLocked(record.Id, record.Version, snapshot.Task(record.Kind)).reason != "" {
				continue
			}
		}
		active = true
	}
	return Wake{Active: active, At: at}
}

func (s *TaskScheduler) expectWakeMatchesWalk(t *testing.T, stage string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	got, want := s.wakeLocked(), s.wakeWalkLocked()
	if !equalWake(got, want) {
		t.Fatalf("%s: wake %+v, walk %+v", stage, got, want)
	}
}

// The wake index agrees with the walk over every live task through a mix of held, waiting, abort-marked, and ended tasks.
func TestWakeIndexMatchesWalk(t *testing.T) {
	fixtures := newOwnFixtures()
	opened := ownOpenHarness(t, fixtures)
	scheduler := opened.harness.(*harnessImpl).tasks
	const holds = 40
	type made struct{ holds, waiters []durable.TaskId }
	tasks, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
		var out made
		for n := range holds {
			id, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("h%d", n)}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return out, err
			}
			out.holds = append(out.holds, id)
		}
		for n := range holds {
			id, err := durable.CreateTask(tx, ownWaiter(), waiterInput{On: []durable.TaskId{out.holds[n], out.holds[(n+1)%holds]}}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return out, err
			}
			out.waiters = append(out.waiters, id)
		}
		return out, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler.expectWakeMatchesWalk(t, "created")
	random := rand.New(rand.NewSource(1))
	for step, n := range random.Perm(holds) {
		switch step % 3 {
		case 0:
			fixtures.open(fmt.Sprintf("h%d", n), "completed")
		case 1:
			if _, err := opened.harness.AbortTask(testContext, tasks.holds[n]); err != nil {
				t.Fatal(err)
			}
		default:
			if _, err := opened.harness.AbortTask(testContext, tasks.waiters[n]); err != nil {
				t.Fatal(err)
			}
		}
		scheduler.expectWakeMatchesWalk(t, fmt.Sprintf("step %d", step))
		flush()
		scheduler.expectWakeMatchesWalk(t, fmt.Sprintf("step %d flushed", step))
	}
	for n := range holds {
		fixtures.open(fmt.Sprintf("h%d", n), "completed")
	}
	scaleWaitUntil(t, func() bool { return !opened.harness.Wake().Active })
	scheduler.expectWakeMatchesWalk(t, "drained")
	mustClose(t, opened.harness)
}

// The sleep index keeps its entries sorted by (until, id) and answers the earliest not yet due like a scan.
func TestWakeIndexSleepers(t *testing.T) {
	index := newWakeIndex()
	random := rand.New(rand.NewSource(2))
	live := map[durable.TaskId]float64{}
	for step := range 5000 {
		id := durable.TaskId(random.Intn(200) + 1)
		if random.Intn(3) == 0 {
			index.remove(id)
			delete(live, id)
		} else {
			index.remove(id)
			until := float64(random.Intn(50))
			entry := sleeper{until, id}
			at, _ := slices.BinarySearchFunc(index.sleepers, entry, compareSleepers)
			index.sleepers = slices.Insert(index.sleepers, at, entry)
			index.untils[id] = until
			live[id] = until
		}
		now := float64(random.Intn(55))
		var want *float64
		for _, until := range live {
			if until > now && (want == nil || until < *want) {
				want = &until
			}
		}
		got := index.nextSleep(now)
		if (got == nil) != (want == nil) || got != nil && *got != *want {
			t.Fatalf("step %d: nextSleep(%v) = %v, want %v", step, now, got, want)
		}
		if len(index.sleepers) != len(live) || !slices.IsSortedFunc(index.sleepers, compareSleepers) {
			t.Fatalf("step %d: sleepers unsorted or sized %d, want %d", step, len(index.sleepers), len(live))
		}
	}
}

// Wake costs the same with twice the live tasks that wait on live work.
func TestWakeCostIsFlatInLiveTasks(t *testing.T) {
	measure := func(n int) float64 {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		scheduler := opened.harness.(*harnessImpl).tasks
		_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (struct{}, error) {
			held, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "held"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return struct{}{}, err
			}
			for range n {
				if _, err := durable.CreateTask(tx, ownWaiter(), waiterInput{On: []durable.TaskId{held}}, durable.TaskOptions{Ownership: conversationOwned}); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		scaleWaitUntil(t, func() bool {
			scheduler.mu.Lock()
			defer scheduler.mu.Unlock()
			return scheduler.wakeIndex.ready.Len() == 1
		})
		const calls = 200
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for range calls {
			scheduler.Wake()
		}
		runtime.ReadMemStats(&after)
		fixtures.open("held", "completed")
		mustClose(t, opened.harness)
		return float64(after.Mallocs-before.Mallocs) / calls
	}
	small, large := measure(1000), measure(4000)
	if large > small*1.3+8 {
		t.Fatalf("allocations per Wake: %.1f at 1000 waiting tasks, %.1f at 4000", small, large)
	}
}

// BenchmarkWakeCreateAndComplete is one task created and completed with OnWake set, over n live waiting tasks.
func BenchmarkWakeCreateAndComplete(b *testing.B) {
	for _, n := range []int{540, 1040, 2040, 4040} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			t := &testing.T{}
			fixtures := newOwnFixtures()
			harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{fixtures.hold.Erased(), ownWaiter().Erased()}, openTasksOptions{onWake: func(Wake) {}})
			opened := ownOpened{harness: harness, root: mustRoot(t, harness, nil)}
			harness.Resume()
			_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (struct{}, error) {
				held, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "held"}, durable.TaskOptions{Ownership: conversationOwned})
				if err != nil {
					return struct{}{}, err
				}
				for range n {
					if _, err := durable.CreateTask(tx, ownWaiter(), waiterInput{On: []durable.TaskId{held}}, durable.TaskOptions{Ownership: conversationOwned}); err != nil {
						return struct{}{}, err
					}
				}
				return struct{}{}, nil
			})
			if err != nil {
				b.Fatal(err)
			}
			// The waiting tasks are settled once the held task is the only invocation and stays so.
			scheduler := harness.(*harnessImpl).tasks
			for stable := 0; stable < 10; {
				time.Sleep(100 * time.Millisecond)
				if scheduler.invocationCount() == 1 {
					stable++
				} else {
					stable = 0
				}
			}
			b.ResetTimer()
			for i := range b.N {
				name := fmt.Sprintf("x%d", i)
				id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
					return durable.CreateTask(tx, fixtures.hold, holdInput{Name: name}, durable.TaskOptions{Ownership: conversationOwned})
				})
				if err != nil {
					b.Fatal(err)
				}
				fixtures.open(name, "completed")
				for {
					if rec, err := opened.harness.GetTask(testContext, id); err == nil && rec != nil && rec.State.Status == durable.TaskTerminal {
						break
					}
				}
			}
		})
	}
}

// BenchmarkWakeSleepers is the sorted sleeper list's insert and remove with n sleeping tasks, ids and untils random.
func BenchmarkWakeSleepers(b *testing.B) {
	for _, n := range []int{4_000, 16_000, 64_000, 256_000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			index := newWakeIndex()
			random := rand.New(rand.NewSource(3))
			add := func(id durable.TaskId) {
				entry := sleeper{float64(random.Intn(1 << 30)), id}
				at, _ := slices.BinarySearchFunc(index.sleepers, entry, compareSleepers)
				index.sleepers = slices.Insert(index.sleepers, at, entry)
				index.untils[id] = entry.until
			}
			for id := 1; id <= n; id++ {
				add(durable.TaskId(id))
			}
			b.ResetTimer()
			for i := range b.N {
				id := durable.TaskId(i%n + 1)
				index.remove(id)
				add(id)
			}
		})
	}
}
