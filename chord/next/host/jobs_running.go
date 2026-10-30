package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/jobs/running.ts
//
// A job's `running` (§15): a JSON watchable of its unsettled runs, one entry per run in start order, tagged by phase.
// It is a source of its own (keyed state documents publish nothing): the job tells it each change in the commit step,
// and a frame carries exactly the ops of those changes, a run's state ops re-pathed under its entry.
//
// Forced Go differences. `RunningEntry<A, S>` is a type-level description of the JSON entries the list holds (owner
// class a: TypeScript type-level checks): an entry is a *delta.JsonObject `{id, version, key?, args, state?, phase}` of
// a running or finishing run, or `{id, version, phase: "blocked", reason}` of a blocked one, which carries neither args
// nor state (it may be of another version than the code); `jobRun.entryFor` builds it. Entries are told apart by
// pointer identity (JavaScript's `indexOf` on objects). Every member is used inside a section.

// running is the list behind `running`. Every change copies the list (O(unsettled runs)) and, while it is watched,
// records its ops; the batch delivers them as one frame. Unwatched, no op is built.
type running struct {
	state.Source
	// label is the job in errors.
	label string
	// list is nil once its object ended: reads return `released`, and no ended run's args or state are kept.
	list  []any
	ended bool
	// ops are the ops of the current batch's changes, kept only while watched.
	ops []delta.Op
	// listed is whether the current batch listed this source for a frame.
	listed bool
}

var _ state.SourceHandle = (*running)(nil)

func newRunning(batch *state.Batch, label string) *running {
	list := &running{label: label, list: []any{}}
	list.Init(batch, list)
	return list
}

// Value implements state.SourceImpl: a list of JSON entries, shared and never mutated once published.
func (list *running) Value() (delta.JsonValue, error) {
	current, err := list.current()
	if err != nil {
		return nil, err
	}
	return current, nil
}

// add appends the entry of a new run.
func (list *running) add(entry *delta.JsonObject) {
	current, _ := list.current()
	next := append(slices.Clone(current), entry)
	list.list = next
	if list.mark() {
		list.ops = append(list.ops, delta.Op{"p", delta.Path{}, len(next) - 1, 0, []any{entry}})
	}
}

// replace replaces a run's entry before by after (its record changed).
func (list *running) replace(before, after *delta.JsonObject) {
	index := list.replaced(before, after)
	if list.mark() {
		list.ops = append(list.ops, delta.Op{"s", delta.Path{index}, after})
	}
}

// stateChanged replaces a run's entry before by after, whose state document changed by ops.
func (list *running) stateChanged(before, after *delta.JsonObject, ops []delta.Op) {
	index := list.replaced(before, after)
	if list.mark() {
		for _, op := range ops {
			list.ops = append(list.ops, under(index, op))
		}
	}
}

// remove removes a run's entry.
func (list *running) remove(entry *delta.JsonObject) {
	current, _ := list.current()
	index := indexOfRunEntry(current, entry)
	list.list = slices.Delete(slices.Clone(current), index, index+1)
	if list.mark() {
		list.ops = append(list.ops, delta.Op{"p", delta.Path{}, index, 1, []any{}})
	}
}

// Deliver implements state.SourceImpl.
func (list *running) Deliver(ctx context.Context) error {
	ops := list.ops
	list.ops = nil
	list.listed = false
	value, err := list.Value()
	if err != nil {
		return err
	}
	if ops == nil {
		ops = []delta.Op{}
	}
	list.Publish(value, ops, false, ctx)
	return nil
}

// end is: its object ended: every watch ends, and the list goes.
func (list *running) end() {
	list.list = nil
	list.ended = true
	list.ops = nil
	list.EndWatches()
}

func (list *running) current() ([]any, error) {
	if list.ended {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: list.label + ": ended"}, nil)
	}
	return list.list, nil
}

// replaced copies the list with after in the place of before; returns that place.
func (list *running) replaced(before, after *delta.JsonObject) int {
	current, _ := list.current()
	index := indexOfRunEntry(current, before)
	next := slices.Clone(current)
	next[index] = after
	list.list = next
	return index
}

// mark records that the list changed, in the batch running now: derives over it are marked; while watched it is listed
// once per batch for a frame. It returns whether the change's ops are to be recorded (only while watched).
func (list *running) mark() bool {
	if !list.Watched() {
		list.Changed()
		return false
	}
	if !list.listed {
		list.listed = true
		list.Changed()
	}
	return true
}

func indexOfRunEntry(list []any, entry *delta.JsonObject) int {
	for index, each := range list {
		if each == any(entry) {
			return index
		}
	}
	return -1
}

// under is op of a value at [index, "state"] inside a larger one: a replacement becomes a set there.
func under(index int, op delta.Op) delta.Op {
	prefix := delta.Path{index, "state"}
	at := func(path any) delta.Path {
		segments, _ := path.([]any)
		return append(slices.Clone(prefix), segments...)
	}
	switch delta.Verb(op) {
	case "r":
		return delta.Op{"s", prefix, op[1]}
	case "s":
		return delta.Op{"s", at(op[1]), op[2]}
	case "d":
		return delta.Op{"d", at(op[1])}
	case "a":
		return delta.Op{"a", at(op[1]), op[2]}
	case "t":
		return delta.Op{"t", at(op[1]), op[2]}
	case "p":
		return delta.Op{"p", at(op[1]), op[2], op[3], op[4]}
	default:
		return delta.Op{"m", at(op[1]), op[2]}
	}
}
