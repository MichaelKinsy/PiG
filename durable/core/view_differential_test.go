// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// referenceView is the view watch that holds the whole value: it appends each entry to the entries it holds and, past its max,
// slices them down to its min, finding `before` in the value itself. It is the oracle the id-keeping watch is compared with.
type referenceView struct {
	conv     session.ID
	min, max int
	value    *jv.Object
	docs     map[string]viewDoc
}

func (w *referenceView) advance(pub *session.Publication) ([]delta.Op, error) {
	var docOps []delta.Op
	var appended []any
	for _, c := range pub.Changes {
		switch c.Kind {
		case session.ChangeTable:
			if numOf(c.Record, "conversationId") == float64(w.conv) && c.Table == session.WriteEntry {
				appended = append(appended, c.Record)
			}
		case session.ChangeDocument:
			if c.ConversationID != w.conv {
				continue
			}
			kind := strOf(c.Record, "kind")
			if !isMounted(kind) || c.Record.Has("key") {
				continue
			}
			id := session.ID(numOf(c.Record, "id"))
			mounted, isMountedDoc := w.docs[kind]
			path := delta.Path{delta.Key("docs"), delta.Key(kind)}
			switch {
			case c.Value == nil:
				if !isMountedDoc || mounted.id != id {
					continue
				}
				delete(w.docs, kind)
				docOps = append(docOps, delta.Op{Verb: delta.Delete, Path: path})
			case isMountedDoc && mounted.id == id && mounted.version == c.Version:
				for _, op := range c.Ops {
					docOps = append(docOps, prefixed(op, kind))
				}
			default:
				w.docs[kind] = viewDoc{id: id, version: c.Version}
				docOps = append(docOps, delta.Op{Verb: delta.Set, Path: path, Value: c.Value})
			}
		}
	}
	ops := docOps
	if len(appended) > 0 {
		list, _ := w.value.Get("entries")
		held, _ := list.([]any)
		ops = append(ops, delta.Op{Verb: delta.Splice, Path: delta.Path{delta.Key("entries")}, Index: len(held), Items: appended})
		held = append(append([]any{}, held...), appended...)
		if len(held) > w.max {
			drop := len(held) - w.min
			before := numOf(held[drop-1].(*jv.Object), "id")
			ops = append(ops, delta.Op{Verb: delta.Splice, Path: delta.Path{delta.Key("entries")}, Remove: drop}, delta.Op{Verb: delta.Set, Path: delta.Path{delta.Key("before")}, Value: before})
		}
	}
	if len(ops) > 0 {
		next, err := delta.Apply(w.value, ops)
		if err != nil {
			return nil, err
		}
		w.value = next.(*jv.Object)
	}
	return ops, nil
}

// Random commits of entries (heads included, which are entries like any other) and mounted-document changes give the same ops and
// the same value from the watch that keeps only the window's IDs as from the watch that holds the whole view.
func TestViewAdvanceMatchesTheWatchThatHeldTheValue(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		slim := newView(t)
		ref := &referenceView{conv: 1, min: slim.min, max: slim.max, value: parsed(t, jv.Stringify(slim.value)), docs: map[string]viewDoc{"pi.live": {id: 2, version: 1}}}
		nextID, docID := 4, 2
		for step := range 200 {
			var changes []session.Change
			for range 1 + rng.Intn(3) {
				switch rng.Intn(6) {
				case 0, 1, 2:
					changes = append(changes, entryChange(t, `{"id":`+itoa(int64(nextID))+`,"conversationId":1,"kind":"m"}`))
				case 3:
					head := `null`
					if rng.Intn(4) != 0 {
						head = itoa(int64(1 + rng.Intn(nextID)))
					}
					changes = append(changes, entryChange(t, `{"id":`+itoa(int64(nextID))+`,"conversationId":1,"kind":"pi.head","head":`+head+`}`))
				case 4:
					changes = append(changes, entryChange(t, `{"id":`+itoa(int64(nextID))+`,"conversationId":2,"kind":"other"}`))
				default:
					kind := []string{"pi.live", "pi.agent", "app.x"}[rng.Intn(3)]
					change := session.Change{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":`+itoa(int64(docID))+`,"kind":"`+kind+`"}`), Version: 1}
					switch rng.Intn(3) {
					case 0:
						change.Value = parsed(t, `{"n":`+itoa(int64(step))+`}`)
					case 1:
						change.Value = parsed(t, `{"n":1}`)
						change.Ops = []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("n")}, Value: float64(step)}}
					}
					if rng.Intn(4) == 0 {
						docID++
					}
					changes = append(changes, change)
				}
				nextID++
			}
			pub := &session.Publication{Changes: changes}
			want, wantErr := ref.advance(pub)
			got, gotErr := slim.advance(pub)
			if (wantErr != nil) != (gotErr != nil) {
				t.Fatalf("seed %d step %d: error %v vs %v", seed, step, gotErr, wantErr)
			}
			if wantErr != nil {
				break
			}
			if jv.Stringify(delta.OpsValue(got)) != jv.Stringify(delta.OpsValue(want)) {
				t.Fatalf("seed %d step %d: ops %s, want %s", seed, step, jv.Stringify(delta.OpsValue(got)), jv.Stringify(delta.OpsValue(want)))
			}
			if jv.Stringify(slim.value) != jv.Stringify(ref.value) {
				t.Fatalf("seed %d step %d: value differs", seed, step)
			}
		}
	}
}
