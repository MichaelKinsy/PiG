// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// hopClock plays the host's side of hops chains (ABI effect 14, event 18) on a virtual microtask clock: a chain of n hops
// started at tick t delivers `hopped` at t+n; chains due at one tick are delivered in the order they started, before host
// events the test schedules at that tick.
type hopClock struct {
	h        *handler
	now      int
	seen     map[uint32]bool
	due      []hopDue
	macros   []uint32 // the macrotasks (effect 15) queued and not yet run, in queue order
	ran      int      // the macrotasks run so far
	commitAt int      // the tick the first commit statements were applied (in a step, or by a chain), or -1
}

type hopDue struct {
	at int
	id uint32 // a hops chain; 0 for a host event
	fn func()
}

func newHopClock(h *handler) *hopClock { return &hopClock{h: h, seen: map[uint32]bool{}, commitAt: -1} }

func (c *hopClock) collect() {
	step := c.h.b.Step()
	if c.commitAt < 0 && len(step.Commits) > 0 {
		c.commitAt = c.now
	}
	for _, e := range step.Effects {
		if e.Kind == abi.EffectHops && !c.seen[e.ID] {
			c.seen[e.ID] = true
			chain, err := abi.DecodeHops(e.Payload)
			if err != nil {
				panic(err)
			}
			if c.commitAt < 0 && len(chain.Commits) > 0 {
				c.commitAt = c.now + int(chain.At) // the host applies a chain's statements in the microtask of hop At
			}
			c.due = append(c.due, hopDue{at: c.now + int(chain.N), id: e.ID})
		}
		if e.Kind == abi.EffectMacrotask && !c.seen[e.ID] {
			c.seen[e.ID] = true
			c.macros = append(c.macros, e.ID)
		}
	}
}

// at delivers fn as a host event at tick t.
func (c *hopClock) at(t int, fn func()) { c.due = append(c.due, hopDue{at: t, fn: fn}) }

func (c *hopClock) run(t *testing.T) {
	t.Helper()
	for {
		if c.h.loop != nil {
			if err := c.h.loop.Run(); err != nil {
				t.Fatal(err)
			}
		}
		c.collect()
		if len(c.due) == 0 {
			if len(c.macros) == 0 {
				return
			}
			// A macrotask runs once the microtask queue, every hops chain included, drained.
			id := c.macros[0]
			c.macros = c.macros[1:]
			c.ran++
			if err := c.h.onHopped(&abi.Event{Kind: abi.EventHopped, ID: id}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		next := 0
		for i, d := range c.due {
			if d.at < c.due[next].at || (d.at == c.due[next].at && c.due[next].id == 0 && d.id != 0) {
				next = i
			}
		}
		d := c.due[next]
		c.due = append(c.due[:next], c.due[next+1:]...)
		c.now = d.at
		if d.id == 0 {
			d.fn()
			continue
		}
		if err := c.h.onHopped(&abi.Event{Kind: abi.EventHopped, ID: d.id}); err != nil {
			t.Fatal(err)
		}
	}
}

// runtime.env's caller resumes three hops after the call when buildEnv settles at once, as it does without an `env` option:
// `#read` returns buildEnv's settled promise, and its adoption (resolve-thenable job, reaction) and the caller's await take
// one hop each (scheduler.ts :1161, :1269; harness.ts :226). Measured on the reference: env call at T, generation resumes at T+3.
func TestEnvCallerResumesThreeHopsAfterASynchronousAnswer(t *testing.T) {
	h := &handler{b: abi.NewBuilder(), loop: &session.Loop{}}
	c := newHopClock(h)
	resumed := -1
	h.completion(abi.EffectEnv, jv.NewObject(), nil, func(any, error) { resumed = c.now })
	var id uint32
	for _, e := range h.b.Step().Effects {
		if e.Kind == abi.EffectEnv {
			id = e.ID
		}
	}
	answer := func() {
		if err := h.onCompletion(&abi.Event{Kind: abi.EventHookDone, ID: id, Payload: []byte("null")}); err != nil {
			t.Fatal(err)
		}
	}
	answer()
	c.run(t)
	if resumed != 3 {
		t.Fatalf("the caller resumed at %d, want 3", resumed)
	}

	// An answer that takes longer than the adoption: the caller resumes two hops after it (the reaction and the await).
	h = &handler{b: abi.NewBuilder(), loop: &session.Loop{}}
	c = newHopClock(h)
	resumed = -1
	h.completion(abi.EffectEnv, jv.NewObject(), nil, func(any, error) { resumed = c.now })
	for _, e := range h.b.Step().Effects {
		if e.Kind == abi.EffectEnv {
			id = e.ID
		}
	}
	c.at(5, answer)
	c.run(t)
	if resumed != 7 {
		t.Fatalf("the caller resumed at %d, want 7", resumed)
	}
}

// A built-in task's runtime.context returns through readContextFrom: a range the kept one serves needs no scan and the caller
// resumes three hops after the bounds job (Pi: rcf return at the job's end, the caller at +3); a range it cannot serve is
// scanned off the line and returns through scanRange as well (the scan's hops, then four).
func TestContextReturnHopsOfAKeptAndAScannedRange(t *testing.T) {
	kept := turn.KeptRange{Head: 7, Tail: 40, Ok: true}
	const scan = 11
	for _, c := range []struct {
		name       string
		prev       turn.KeptRange
		head, tail session.ID
		want       int
	}{
		{"the kept range, same tail", kept, 7, 40, 3},
		{"the kept range cut at an earlier entry", kept, 7, 30, 3},
		{"a tail past the kept one", kept, 7, 41, scan + 4},
		{"another head marker", kept, 9, 40, scan + 4},
		{"no kept range", turn.KeptRange{}, 7, 40, scan + 4},
	} {
		if got := contextReturnHops(c.prev, c.head, c.tail, scan); got != c.want {
			t.Errorf("%s: %d hops, want %d", c.name, got, c.want)
		}
	}
}

// lineRig is a handler whose Session line runs the Loop of startLine over an in-memory store, with the host clock of hopClock.
func lineRig(t *testing.T, profile string) (*handler, *hopClock) {
	t.Helper()
	mem := sessiontest.New()
	h := &handler{b: abi.NewBuilder(), mir: newMirror(), open: parsed(t, `{"storageProfile":"`+profile+`"}`)}
	h.sess = session.New(mem, mem.NextID(), func() float64 { return 0 })
	h.startLine()
	return h, newHopClock(h)
}

// noteJob queues a commit job that creates one conversation: the jobs touch no common key, so none waits for another's publication.
func noteJob(h *handler, done func(error)) {
	h.loop.Line(func() error {
		_, err := h.sess.RunCommit(func(tx *session.Tx) error {
			_, err := tx.CreateConversation(session.Ownership{})
			return err
		}, session.TxScope{})
		return err
	}, done)
}

// The writer sends the parts admitted while a Storage call is in flight as one call, and publishes the parts of a call a macrotask apart
// (ABI.md section 5.5): the host delivers what the first publication resolves before the macrotask that publishes the second.
func TestTwoPartsOfOneCallArePublishedAMacrotaskApart(t *testing.T) {
	h, c := lineRig(t, "gate-capsql")
	// A Storage call that outlasts the jobs behind it.
	h.loop.StoreTicks = func([][]session.Write) (read, receive, resume int) { return 0, 60, 70 }
	type published struct{ tick, macros int }
	var pubs []published
	h.sess.SubscribeCommits(func(*session.Publication) { pubs = append(pubs, published{c.now, c.ran}) })
	// The first part goes out alone; the next two are admitted while its call is in flight, so they leave together.
	for range 3 {
		noteJob(h, func(error) {})
	}
	c.run(t)
	if len(pubs) != 3 {
		t.Fatalf("%d parts published, want 3", len(pubs))
	}
	if pubs[2].macros != pubs[1].macros+1 {
		t.Fatalf("the two parts of one call were published in the same macrotask or %d apart: %+v", pubs[2].macros-pubs[1].macros, pubs)
	}
	// The macrotask runs once every microtask queued, chains of other jobs included, drained.
	if pubs[2].tick < pubs[1].tick {
		t.Fatalf("published at %d and %d", pubs[1].tick, pubs[2].tick)
	}
}

// What a hops chain keeps between steps does not grow with the number of jobs: the arena stays one hopArenaSize buffer and no parked
// chain outlives its job.
func TestHopsStateIsFlatAcrossManyCommitJobs(t *testing.T) {
	h, c := lineRig(t, "gate-capsql")
	arenaCap := -1
	for i := range 500 {
		noteJob(h, func(error) {})
		c.run(t)
		h.StepStart()
		if i == 0 {
			arenaCap = cap(h.hopArena)
		}
		if len(h.hops) != 0 || len(c.due) != 0 || len(c.macros) != 0 {
			t.Fatalf("job %d left %d parked chains, %d due and %d macrotasks", i, len(h.hops), len(c.due), len(c.macros))
		}
	}
	if arenaCap != hopArenaSize || cap(h.hopArena) != hopArenaSize || len(h.hopArena) != 0 {
		t.Fatalf("arena capacity %d after the first job and %d (length %d) after 500, want %d and empty", arenaCap, cap(h.hopArena), len(h.hopArena), hopArenaSize)
	}
	if cap(h.hops) > 8 {
		t.Fatalf("the parked-chain list kept capacity %d", cap(h.hops))
	}
}

// open.storageProfile "node-sqlite" is what the host's openNodeSqliteStorage (SqliteStorage over node.ts, the adapter Pi runs
// on Node) names, so it is charged the Node adapter's await structure, one tick per statement; the capsql facade's four
// belong to the gate profiles and cloudflare-do (no table of its own). Charging a Node store the capsql table makes every
// line job 2-3x Pi's hops, which reorders a background chain behind the turn (async-context.test.ts: six long turns compact
// three times where Pi compacts twice).
// mutation-checked: a hopProfile without the node-sqlite case answers GateCapsql and fails the node-sqlite row.
func TestHopProfileOfTheHostsNodeAdapterIsTheNodeTable(t *testing.T) {
	for name, want := range map[string]hopcost.Profile{
		"node-sqlite": hopcost.Node, "node": hopcost.Node, "memory": hopcost.Memory, "gate": hopcost.Gate,
		"gate-capsql": hopcost.GateCapsql, "cloudflare-do": hopcost.GateCapsql, "": hopcost.GateCapsql,
	} {
		h := &handler{open: parsed(t, `{"storageProfile":"`+name+`"}`)}
		if got := h.hopProfile(); got != want {
			t.Errorf("storageProfile %q -> %q, want %q", name, got, want)
		}
	}
	h := &handler{open: parsed(t, `{"storageProfile":"node-sqlite"}`)}
	if node, capsql := h.readHops(hopcost.ReadRow), (&handler{open: parsed(t, `{"storageProfile":"gate-capsql"}`)}).readHops(hopcost.ReadRow); node >= capsql {
		t.Errorf("a node-sqlite row read costs %d ticks, the capsql facade's %d: the Node adapter must cost less", node, capsql)
	}
}
