// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
	"strconv"
	"testing"
)

// settledSnapshot is a view's settled messages as a reader saw them when it derived the view.
type settledSnapshot struct {
	step    int
	lineage uint64
	msgs    []string
	idents  []MessageIdent
	stored  []bool
}

// TestViewsOfOneLineageAgreeOnTheirSettledMessages checks View.Settled, which lets a reader of a derived context skip the
// settled prefix it already holds (sqlhost rangeContext). A conversation grows by turns, lone users, a tool call whose result
// comes later, system messages, compaction markers, edits and releases. A derivation that extends the kept context keeps
// its lineage and never shrinks the settled part (so a reader compares only the open part); a derivation that scans the
// range again starts a lineage no earlier view had; and every two views of one lineage agree, message by message, on the
// settled messages both have.
func TestViewsOfOneLineageAgreeOnTheirSettledMessages(t *testing.T) {
	for _, trim := range []bool{false, true} {
		for seed := range uint64(12) {
			t.Run(strconv.FormatBool(trim)+"/"+strconv.FormatUint(seed, 10), func(t *testing.T) {
				checkSettledLineages(t, trim, seed)
			})
		}
	}
}

func checkSettledLineages(t *testing.T, trim bool, seed uint64) {
	state := seed*0x9e3779b97f4a7c15 + 1
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	tr := newTranscript(t, 2)
	st := NewStore()
	if trim {
		st.SetTrimAtHead()
	}
	var users []int64
	var snaps []settledSnapshot
	seen := map[uint64]bool{}
	appended := 0
	pending := ""
	extendedAt, kept := 0, 0
	for step := range 90 {
		s := strconv.Itoa(step)
		switch op := next(12); {
		case pending != "" && op < 6:
			tr.add("pi.tool-result", `{"role":"toolResult","toolCallId":"`+pending+`","toolName":"read","content":[{"type":"text","text":"late `+s+`"}],"isError":false,"timestamp":`+s+`}`, "")
			pending = ""
		case op < 4:
			users = append(users, tr.turn(step))
		case op == 4:
			users = append(users, tr.add("pi.user", `{"role":"user","content":"alone `+s+`","timestamp":`+s+`}`, ""))
		case op == 5 && pending == "":
			pending = "pending_" + s
			tr.add("pi.assistant", `{"role":"assistant","content":[{"type":"toolCall","id":"`+pending+`","name":"read","arguments":{}}],"stopReason":"toolUse","timestamp":`+s+`}`, "")
		case op == 6:
			tr.add("pi.system", `{"role":"system","content":[{"type":"text","text":"system `+s+`"}],"timestamp":`+s+`}`, "")
		case op == 7 && len(users) > 0:
			tr.compact(users[next(len(users))])
		case op == 8 && len(users) > 0:
			tr.add("pi.system", "", `,"edits":[{"target":`+strconv.FormatInt(users[next(len(users))], 10)+`,"action":"omit"}]`)
		case op == 9:
			st.Release(2)
		default:
			users = append(users, tr.turn(step))
		}
		if step > 0 {
			for _, row := range tr.db.entries[appended:] {
				if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
					t.Fatal(err)
				}
			}
		}
		appended = len(tr.db.entries)
		extended, rebuilt := st.extended, st.rebuilt
		v, err := tr.db.context(t, st, 2, 0)
		if err != nil {
			t.Fatal(err)
		}
		lineage, n := v.Settled()
		if lineage == 0 {
			// Only the context of a conversation without entries (emptyDerived) has none.
			if v.NumMessages() != 0 {
				t.Fatalf("step %d: a derived context of %d messages has lineage 0", step, v.NumMessages())
			}
			continue
		}
		if n > v.NumMessages() {
			t.Fatalf("step %d: %d settled of %d messages", step, n, v.NumMessages())
		}
		snap := settledSnapshot{step: step, lineage: lineage}
		for i := range n {
			id, ok := v.MessageIdent(i)
			snap.msgs = append(snap.msgs, string(v.Message(i)))
			snap.idents = append(snap.idents, id)
			snap.stored = append(snap.stored, ok)
		}
		var last *settledSnapshot
		if len(snaps) > 0 {
			last = &snaps[len(snaps)-1]
		}
		switch {
		case st.rebuilt != rebuilt:
			if seen[lineage] {
				t.Fatalf("step %d: a derivation that scanned the range again reuses lineage %d", step, lineage)
			}
		case last != nil && st.extended != extended:
			extendedAt++
			if lineage != last.lineage {
				// Only the baseline leading past settled assistant messages (Pi 5713c950) reorders settled messages an earlier
				// view shared: the extension then starts a lineage no earlier view had.
				if seen[lineage] || !tr.hasBaselineLeading(t, v) {
					t.Fatalf("step %d: a derivation that extended the kept context changed the lineage %d to %d", step, last.lineage, lineage)
				}
			} else if n < len(last.msgs) {
				t.Fatalf("step %d: an extension shrank the settled messages from %d to %d", step, len(last.msgs), n)
			}
			if n > len(last.msgs) {
				kept++
			}
		}
		seen[lineage] = true
		for _, o := range snaps {
			if o.lineage != lineage {
				continue
			}
			for i := range min(len(o.msgs), n) {
				if o.msgs[i] != snap.msgs[i] || o.idents[i] != snap.idents[i] || o.stored[i] != snap.stored[i] {
					t.Fatalf("step %d: lineage %d disagrees with step %d at settled message %d:\n got  %s\n then %s", step, lineage, o.step, i, snap.msgs[i], o.msgs[i])
				}
			}
		}
		snaps = append(snaps, snap)
	}
	// The run must exercise extensions that grow the settled part, or the lineage checks above prove nothing.
	if extendedAt < 10 || kept < 5 {
		t.Fatalf("%d extensions, %d of them grew the settled messages: the run does not exercise the lineage", extendedAt, kept)
	}
}

// hasBaselineLeading reports whether the view's first message is a system message that settled assistant messages precede in
// the contributions: the baseline led past them.
func (tr *transcript) hasBaselineLeading(t *testing.T, v *View) bool {
	t.Helper()
	if v.NumMessages() == 0 || v.NumSystemMessages() == 0 {
		return false
	}
	return bytes.Equal(v.Message(0), v.SystemMessage(0))
}
