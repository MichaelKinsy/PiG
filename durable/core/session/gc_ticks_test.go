package session_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// The line probes (testdata/gc_ticks.json) run through the pinned Pi's Session over MemoryStorage (testdata/gc_ticks.mjs) and through this
// package's Loop, and compare every mark: [label, macrotask turn, microtask pass of the turn] of each job's start, end and caller
// resumption, of the writer's call to Storage and the Storage's receipts, and of each publication. The Loop is given only what a host
// gives it (Elapsed, StoreTicks); the line's release, the writer's repump, the publication hop and the fences are the Loop's own (hopcost).

type probeJob struct {
	Label    string `json:"label"`
	Delay    int    `json:"delay"`
	Kind     string `json:"kind"`
	Internal bool   `json:"internal"`
}

type probeSpec struct {
	Name         string     `json:"name"`
	StorageTicks int        `json:"storageTicks"`
	RefuseAt     *int       `json:"refuseAt"`
	RejectAt     *int       `json:"rejectAt"`
	Jobs         []probeJob `json:"jobs"`
}

type mark struct {
	Label string
	Turn  int
	Tick  int
}

func (m *mark) UnmarshalJSON(b []byte) error {
	var raw []any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Label, m.Turn, m.Tick = raw[0].(string), int(raw[1].(float64)), int(raw[2].(float64))
	return nil
}

func (m mark) String() string {
	return m.Label + "@" + strconv.Itoa(m.Turn) + "." + strconv.Itoa(m.Tick)
}

func probeOracle(t *testing.T) map[string][]mark {
	t.Helper()
	cmd := exec.Command("node", "--conditions=source", "testdata/gc_ticks.mjs")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			t.Skipf("node unavailable: %v", err)
		}
		t.Fatalf("oracle: %v\n%s", err, stderr.String())
	}
	marks := map[string][]mark{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var line struct {
			Name  string `json:"name"`
			Marks []mark `json:"marks"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("oracle line %q: %v", sc.Text(), err)
		}
		marks[line.Name] = line.Marks
	}
	return marks
}

// probeStore marks the writer's receipts: each batch the call stores, in order. Once armed it counts the batches it was asked to store, and the one the spec
// names is refused (RefuseAt), or fails the call (RejectAt), after the batches before it in the call are durable.
type probeStore struct {
	*sessiontest.MemStore
	mark           func(string)
	batch          *int
	armed          bool
	stored         int
	refuse, reject *int
}

func (p *probeStore) Commit(writes []session.Write, next session.ID) (session.Seq, error) {
	if p.armed {
		at := p.stored
		p.stored++
		if p.refuse != nil && at == *p.refuse {
			return 0, &session.RefusedError{Err: errors.New("refused")}
		}
		if p.reject != nil && at == *p.reject {
			return 0, errors.New("rejected")
		}
	}
	seq, err := p.MemStore.Commit(writes, next)
	if err == nil {
		p.mark("receive:" + strconv.Itoa(*p.batch))
		*p.batch++
	}
	return seq, err
}

type markResumer func()

func (f markResumer) Resume() { f() }

// spinLimit bounds the passes of one turn, as the oracle's spin does.
const spinLimit = 400

func goProbe(t *testing.T, spec probeSpec) []mark {
	t.Helper()
	mem := sessiontest.New()
	loop := &session.Loop{}
	var marks []mark
	turn, tick, counting := 0, 0, false
	note := func(label string) {
		if counting {
			marks = append(marks, mark{label, turn, tick})
		}
	}
	var spin func()
	spin = func() {
		if counting && tick < spinLimit {
			tick++
			loop.Defer(spin)
		}
	}
	var macros []session.Resumer
	loop.Macro = func(r session.Resumer) bool { macros = append(macros, r); return true }
	batch := 0
	store := &probeStore{MemStore: mem, mark: note, batch: &batch, refuse: spec.RefuseAt, reject: spec.RejectAt}
	sess := session.New(store, mem.NextID(), func() float64 { return 1000 })
	sess.SetHopProfile(hopcost.Memory)
	loop.Bind(sess)
	readBody := 0
	elapsed := sessiontest.Elapsed(hopcost.Memory)
	loop.Elapsed = func(e session.Ended) (int, int) {
		if e.Read {
			return 0, hopcost.ReadReturn(readBody, false)
		}
		return elapsed(e)
	}
	// The probe's Storage wraps MemoryStorage.commit in an async function that awaits StorageTicks ticks (testdata/gc_ticks.mjs): MemoryStorage's
	// receipts come when it resumes, one tick after those, and the wrapper returns, one tick after MemoryStorage's promise settled; its own
	// promise settles one tick after that, and the writer resumes from the Session's guard hopcost.StoreCallResume ticks after the call.
	loop.StoreTicks = func(batches [][]session.Write) (int, int, int) {
		note("call:" + strconv.Itoa(len(batches)))
		batch = 0
		return 0, spec.StorageTicks + 1, hopcost.StoreCallResume(spec.StorageTicks + 2)
	}
	sess.SubscribeCommits(func(p *session.Publication) {
		if p.Memory {
			note("publish:held")
			return
		}
		note("publish:" + strconv.FormatInt(p.Seq, 10))
	})
	drive := func() {
		for {
			if err := loop.Run(); err != nil {
				t.Fatal(err)
			}
			if len(macros) == 0 {
				return
			}
			r := macros[0]
			macros = macros[1:]
			turn++
			tick = 0
			if counting {
				loop.Defer(spin)
			}
			r.Resume()
		}
	}
	var root session.ID
	loop.Line(func() error {
		_, err := sess.Commit(func(tx *session.Tx) error {
			rec, err := tx.CreateRootConversation()
			root = mustID(rec)
			return err
		}, session.TxScope{})
		return err
	}, func(error) {})
	drive()
	marks, turn, tick, counting = nil, 0, 0, true
	store.armed, store.stored = true, 0
	loop.Defer(spin)
	for _, j := range spec.Jobs {
		switch j.Kind {
		case "read":
			loop.ReadLine(func() error {
				note("start:" + j.Label)
				readBody = j.Delay + 1
				loop.Wait(j.Delay+1, markResumer(func() { note("end:" + j.Label) }))
				return nil
			}, func(error) { note("resolved:" + j.Label) })
		default:
			run := func() error {
				_, err := sess.RunCommit(func(tx *session.Tx) error {
					if loop.Consumed() == 0 {
						note("start:" + j.Label)
					}
					tx.Await(j.Delay + 1)
					if j.Kind == "scan-write" {
						if _, err := tx.ScanEntries(session.EntryQuery{ConversationID: root}, 10, 0); err != nil {
							return err
						}
					}
					if j.Kind != "none" {
						if _, err := tx.AppendEntry(root, jv.Clone(obj("kind", "e", "data", float64(1))).(*jv.Object)); err != nil {
							return err
						}
					}
					if j.Kind == "throw" {
						return errors.New("boom")
					}
					return nil
				}, session.TxScope{})
				if err == nil {
					loop.Wait(sess.PeekTicks()-loop.Consumed(), markResumer(func() { note("end:" + j.Label) }))
				}
				return err
			}
			done := func(err error) {
				if err != nil {
					note("rejected:" + j.Label + ":" + err.Error())
					return
				}
				note("resolved:" + j.Label)
			}
			if j.Internal {
				loop.LineInternal(run, done)
			} else {
				loop.LineAfterTail(run, done)
			}
		}
	}
	drive()
	return marks
}

func mustID(rec *jv.Object) session.ID {
	id, _ := rec.Get("id")
	return session.ID(id.(float64))
}

func TestLineTicksMatchPi(t *testing.T) {
	data, err := os.ReadFile("testdata/gc_ticks.json")
	if err != nil {
		t.Fatal(err)
	}
	var specs []probeSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatal(err)
	}
	want := probeOracle(t)
	for _, spec := range specs {
		t.Run(spec.Name, func(t *testing.T) {
			got := goProbe(t, spec)
			w := want[spec.Name]
			if len(w) == 0 {
				t.Fatal("the oracle measured nothing")
			}
			for i := range max(len(got), len(w)) {
				var g, x string
				if i < len(got) {
					g = got[i].String()
				}
				if i < len(w) {
					x = w[i].String()
				}
				if g != x {
					t.Fatalf("mark %d differs\n got  %v\n want %v", i, got, w)
				}
			}
		})
	}
}
