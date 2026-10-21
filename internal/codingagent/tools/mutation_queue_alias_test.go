package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The edit contract preserves disjoint changes to one file, including when callers use different names for it.
func TestEditPreservesParallelAliasChanges(t *testing.T) {
	for _, kind := range []string{"hardlink", "case", "symlink", "same"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "Target.txt")
			if err := os.WriteFile(target, []byte("alpha\nbeta\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			alias := mutationAlias(t, target, kind)
			synctest.Test(t, func(t *testing.T) {
				tool := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
				ops := tool.operations()
				read := ops.ReadFile
				firstRead, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				ops.ReadFile = func(path string) ([]byte, error) {
					data, err := read(path)
					first := false
					once.Do(func() { first = true })
					if first {
						close(firstRead)
						<-release
					}
					return data, err
				}
				tool.Operations = &ops
				var wg sync.WaitGroup
				for i, path := range []string{target, alias} {
					edit := []editEntry{{"alpha", "ALPHA"}, {"beta", "BETA"}}[i]
					args, err := json.Marshal(editParams{Path: path, Edits: []editEntry{edit}})
					if err != nil {
						t.Fatal(err)
					}
					wg.Go(func() { assertMutationResult(t, tool, t.Context(), args, false) })
					if i == 0 {
						<-firstRead
					}
					synctest.Wait()
				}
				close(release)
				wg.Wait()
			})
			assertMutationFile(t, target, "ALPHA\nBETA\n")
			assertMutationFile(t, alias, "ALPHA\nBETA\n")
		})
	}
}

func TestMutationAliasReservationsKeepDispatchOrder(t *testing.T) {
	for _, initiallyMissing := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "created while queued"}[initiallyMissing], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				target := filepath.Join(dir, "Target.txt")
				if !initiallyMissing {
					if err := os.WriteFile(target, []byte("alpha\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				q := NewFileMutationQueue()
				edit := &EditTool{CWD: dir, Queue: q}
				write := &WriteTool{CWD: dir, Queue: q}
				args := json.RawMessage(`{"path":"Target.txt","edits":[{"oldText":"alpha","newText":"ALPHA"}]}`)
				first, ok := edit.ReserveMutationOrder(args)
				if !ok {
					t.Fatal("edit did not reserve its mutation")
				}
				if initiallyMissing {
					if err := os.WriteFile(target, []byte("alpha\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				alias := mutationAlias(t, target, "hardlink")
				writeArgs, err := json.Marshal(writeParams{Path: alias, Content: "replacement\n"})
				if err != nil {
					t.Fatal(err)
				}
				second, ok := write.ReserveMutationOrder(writeArgs)
				if !ok {
					t.Fatal("write did not reserve its mutation")
				}
				third, ok := edit.ReserveMutationOrder(args)
				if !ok {
					t.Fatal("second edit did not reserve its mutation")
				}
				var order []int
				var mu sync.Mutex
				var wg sync.WaitGroup
				for i, ticket := range []*agent.MutationTicket{second, third} {
					wg.Go(func() {
						ticket.Wait()
						defer ticket.Release()
						mu.Lock()
						order = append(order, i)
						mu.Unlock()
					})
				}
				synctest.Wait()
				if len(order) != 0 {
					t.Error("an alias reservation passed the held edit")
				}
				first.Wait()
				first.Release()
				wg.Wait()
				if !slices.Equal(order, []int{0, 1}) {
					t.Fatalf("reservation order = %v, want write then edit", order)
				}
				if len(q.chains) != 0 || len(q.files) != 0 {
					t.Fatal("drained queue retained alias state")
				}
			})
		})
	}
}

func mutationAlias(t *testing.T, target, kind string) string {
	t.Helper()
	alias := filepath.Join(filepath.Dir(target), "alias.txt")
	switch kind {
	case "hardlink":
		if err := os.Link(target, alias); err != nil {
			t.Fatal(err)
		}
	case "case":
		alias = filepath.Join(filepath.Dir(target), "target.txt")
		info, err := os.Stat(alias)
		if os.IsNotExist(err) {
			t.Skip("the test filesystem distinguishes letter case")
		}
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(original, info) {
			t.Fatal("case alias does not name the target")
		}
	case "symlink":
		testenv.Symlink(t, target, alias)
	case "same":
		alias = target
	default:
		t.Fatalf("unknown alias kind %q", kind)
	}
	return alias
}

// D190's parity allowance. Pinned Pi keys its queue by realpath, so two calls through hard links of one file run at once (both callbacks start while the first is held); PiG starts the second only after the first ends.
func TestHardLinkAliasesDifferFromPi(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mutationAlias(t, filepath.Join(dir, "a"), "hardlink")
	scenario := mutationQueueScenario{
		Dir:      dir,
		Calls:    []mutationQueueCall{{Path: "a", Name: "c0"}, {Path: "alias.txt", Name: "c1"}},
		Releases: []int{0, 1},
	}
	if got := piMutationQueueOutcomes(t, []mutationQueueScenario{scenario})[0].Counts; !slices.Equal(got, []int{2, 3, 4}) {
		t.Fatalf("Pi event counts = %v, want [2 3 4]: both hard-link callbacks start at once", got)
	}
	synctest.Test(t, func(t *testing.T) {
		q := NewFileMutationQueue()
		var mu sync.Mutex
		var log []string
		record := func(event string) {
			mu.Lock()
			defer mu.Unlock()
			log = append(log, event)
		}
		snapshot := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(log)
		}
		gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
		var opened [2]sync.Once
		open := func(i int) { opened[i].Do(func() { close(gates[i]) }) }
		var wg sync.WaitGroup
		// A failed check must not leave a call waiting on its gate: the bubble would report a deadlock over the failure.
		defer func() {
			open(0)
			open(1)
			wg.Wait()
		}()
		for i, call := range scenario.Calls {
			ticket, err := q.Reserve(filepath.Join(dir, call.Path))
			if err != nil {
				t.Fatal(err)
			}
			wg.Go(func() {
				ticket.Wait()
				defer ticket.Release()
				record("start " + call.Name)
				<-gates[i]
				record("end " + call.Name)
			})
		}
		synctest.Wait()
		if !slices.Equal(snapshot(), []string{"start c0"}) {
			t.Fatalf("log with the first call held = %v, want only its start", snapshot())
		}
		open(0)
		synctest.Wait()
		if !slices.Equal(snapshot(), []string{"start c0", "end c0", "start c1"}) {
			t.Fatalf("log after the first call ended = %v", snapshot())
		}
		open(1)
	})
}
