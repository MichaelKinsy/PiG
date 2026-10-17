package history

import "testing"

// A fork's cached context reads erefs into its parent's chain. The parent's chain, loaded only through the fork point,
// is reset when the parent gets a new entry (Store.Append cannot extend a bounded chain); the fork's next context must not
// serve the erefs of the reset chain.
func TestAForksContextSurvivesItsParentsChainBeingReset(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 10 {
		tr.turn(i)
	}
	at := tr.nextID - 1
	child := tr.nextID
	tr.nextID++
	tr.db.convs[child] = AppendConversation(nil, ConvRecord{ID: child, HasParent: true, ParentConv: 2, ParentAt: at})
	ct := &transcript{t: t, db: tr.db, conv: child, nextID: tr.nextID, seq: tr.seq}
	ct.turn(10)
	st := NewStore()
	v, err := tr.db.context(t, st, child, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := messagesOf(v)
	if ch := st.convs[2].chain; ch == nil || ch.To == unbounded {
		t.Fatalf("the parent's chain should be loaded only through the fork point: %+v", ch)
	}
	// The parent continues after the fork.
	pt := &transcript{t: t, db: tr.db, conv: 2, nextID: ct.nextID, seq: ct.seq}
	start := len(tr.db.entries)
	pt.turn(11)
	for _, row := range tr.db.entries[start:] {
		if row.conv != 2 {
			continue
		}
		if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
			t.Fatal(err)
		}
	}
	v, err = tr.db.context(t, st, child, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameMessages(t, messagesOf(v), want)
}
