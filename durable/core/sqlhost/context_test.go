// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

func (r *rangeDB) contextRead(text string, params ...int64) (string, error) {
	r.t.Helper()
	values := make([]abi.Value, len(params))
	for i, p := range params {
		values[i] = abi.Int(p)
	}
	rows, err := r.h.answerContext(text, values)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		r.t.Fatalf("%s answered %d rows", text, len(rows))
	}
	return string(rows[0][0].Bytes), nil
}

// The Go host answers the context summary as the JS host does (host/cf/test/contextreads.test.ts, the same entries): the
// system message leads the view, the measured assistant usage (120) plus the estimate of the user message after it
// (ceil(70 / 3.5) = 20) is the estimate of the compaction check, and the estimate is computed only when asked for.
func TestTheContextSummaryNamesTheSystemMessagesAndTheEstimate(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(1, "")
	r.next = 2
	usage := `"usage":{"input":100,"output":20,"cacheRead":0,"cacheWrite":0,"totalTokens":120,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	r.entry(1, "pi.user", `{"role":"user","content":"hi","timestamp":1}`, "", 0)
	r.entry(1, "pi.system", `{"role":"system","content":"be brief","timestamp":2}`, "", 0)
	r.entry(1, "pi.assistant", `{"role":"assistant","content":[{"type":"text","text":"ok"}],"api":"faux","provider":"faux","model":"m",`+usage+`,"stopReason":"stop","timestamp":3}`, "", 0)
	r.entry(1, "pi.user", `{"role":"user","content":"`+strings.Repeat("x", 70)+`","timestamp":4}`, "", 0)
	got, err := r.contextRead(store.TextContextSummary, 1, 5, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"tail":5,"head":null,"system":[{"role":"system","content":"be brief","timestamp":2}],"tokens":140}`; got != want {
		t.Fatalf("summary\n%s\nwant\n%s", got, want)
	}
	if got, _ := r.contextRead(store.TextContextSummary, 1, 5, 0, 0, 0); !strings.HasSuffix(got, `"tokens":0}`) {
		t.Fatalf("summary without the estimate %s", got)
	}
}

// Behind a head marker the summary lists the active entries, and the host checks the marker and its head the core named
// against the context it derived: another marker, another head or no marker is a fault, not a context to answer.
func TestTheContextReadsCheckTheHeadMarkerTheCoreNamed(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(1, "")
	r.next = 2
	r.entry(1, "pi.user", `{"role":"user","content":"old","timestamp":1}`, "", 0)
	r.entry(1, "pi.user", `{"role":"user","content":"kept","timestamp":2}`, "", 0)
	r.entry(1, "pi.compaction", `{"role":"user","content":"summary","timestamp":3}`, `,"head":3`, 3)
	got, err := r.contextRead(store.TextContextSummary, 1, 4, 4, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"tail":4,"head":4,"entries":[[4,"pi.compaction"],[3,"pi.user"]],"system":[],"tokens":0}`; got != want {
		t.Fatalf("summary\n%s\nwant\n%s", got, want)
	}
	messages, err := r.contextRead(store.TextContextMessages, 1, 4, 4, 3)
	if err != nil || !strings.Contains(messages, `"summary"`) || !strings.Contains(messages, `"kept"`) || strings.Contains(messages, `"old"`) {
		t.Fatalf("messages %s, %v", messages, err)
	}
	for _, named := range [][2]int64{{4, 2}, {0, 0}, {3, 3}} {
		if _, err := r.contextRead(store.TextContextSummary, 1, 4, named[0], named[1], 0); err == nil || !strings.Contains(err.Error(), "storage has head marker 4 (head 3)") {
			t.Errorf("summary naming marker %d head %d: %v", named[0], named[1], err)
		}
		if _, err := r.contextRead(store.TextContextMessages, 1, 4, named[0], named[1]); err == nil {
			t.Errorf("messages naming marker %d head %d answered", named[0], named[1])
		}
	}
	if _, err := r.contextRead(store.TextContextSummary, 1, 9, 4, 3, 0); err == nil || !strings.Contains(err.Error(), "Entry 9 is not visible") {
		t.Errorf("a summary through an entry storage lacks: %v", err)
	}
}
