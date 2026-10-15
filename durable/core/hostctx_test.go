// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

type hostRead struct {
	sql    uint16
	params []int64
}

// contextRig is a handler whose reader answers the host's context reads with canned rows and records every read.
func contextRig(answer func(sql uint16, params []abi.Value) ([][]abi.Value, error)) (*handler, *[]hostRead) {
	var reads []hostRead
	h := &handler{mir: newMirror()}
	read := func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
		r := hostRead{sql: sql}
		for _, p := range params {
			r.params = append(r.params, int64(p.Number()))
		}
		reads = append(reads, r)
		return answer(sql, params)
	}
	h.reader = read
	h.mir.lazy = lazyState{read: read}
	return h, &reads
}

func blobRow(text string) [][]abi.Value { return [][]abi.Value{{abi.Blob([]byte(text))}} }

// The core names a context by its bounds and reads what a request preparation needs from the host's summary: the head marker, the
// active entries behind it, the system messages and the estimate of the compaction check.
func TestHostContextReadsTheHostsSummaryOfTheBounds(t *testing.T) {
	h, reads := contextRig(func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
		if sql != store.SQLContextSummary {
			t.Fatalf("read of statement %d, want the context summary", sql)
		}
		return blobRow(`{"tail":9,"head":7,"entries":[[7,"pi.compaction"],[8,"pi.user"],[9,"pi.system"]],"system":[{"role":"system","content":"be brief","timestamp":1}],"tokens":120}`), nil
	})
	c, ok := h.hostContextOf(3, 9, 7, 5, true)
	if !ok {
		t.Fatalf("the summary read failed: %v", h.mir.lazy.err)
	}
	if len(*reads) != 1 || (*reads)[0].sql != store.SQLContextSummary {
		t.Fatalf("reads %+v, want one context summary", *reads)
	}
	// Conversation, tail, head marker, the marker's head, and the estimate wanted.
	if got := (*reads)[0].params; len(got) != 5 || got[0] != 3 || got[1] != 9 || got[2] != 7 || got[3] != 5 || got[4] != 1 {
		t.Fatalf("summary parameters %v, want [3 9 7 5 1]", got)
	}
	pv := c.PromptView()
	if !pv.HasHead || pv.HeadID != 7 || len(pv.Entries) != 3 || pv.Entries[2].Kind != turn.KindSystem || pv.Entries[1].ID != 8 || len(pv.Messages) != 1 {
		t.Fatalf("prompt view %+v", pv)
	}
	if got := jv.Stringify(c.SystemMessages()[0]); !strings.Contains(got, `"be brief"`) {
		t.Fatalf("system messages %s", got)
	}
	// Planned messages add their estimates to the host's: under the thresholds nothing starts, and range selection is not asked.
	policy := history.Policy{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000, BackgroundTokens: 32768}
	if got := c.Threshold([][]byte{[]byte(`{"role":"user","content":"hi","timestamp":1}`)}, 128000, policy); got != history.ThresholdNone {
		t.Fatalf("threshold %v for 121 tokens in a 128000 window", got)
	}
	if got := c.Threshold(nil, 128000, history.Policy{}); got != history.ThresholdNone {
		t.Fatalf("threshold %v with compaction disabled", got)
	}
}

// A head marker the context has not got is a null head with no entries; an empty conversation asks the host nothing.
func TestHostContextWithoutAHeadMarkerAndOfAnEmptyConversation(t *testing.T) {
	h, reads := contextRig(func(uint16, []abi.Value) ([][]abi.Value, error) {
		return blobRow(`{"tail":4,"head":null,"system":[],"tokens":0}`), nil
	})
	c, ok := h.hostContextOf(1, 4, 0, 0, false)
	if !ok || c.PromptView().HasHead || len(c.PromptView().Entries) != 0 {
		t.Fatalf("a context without a marker: ok %v, %+v", ok, c.PromptView())
	}
	empty, ok := h.hostContextOf(1, 0, 0, 0, true)
	if !ok || len(*reads) != 1 || len(empty.PromptView().Messages) != 0 || len(empty.Messages()) != 0 {
		t.Fatalf("an empty conversation read the host: %d reads", len(*reads))
	}
}

// A host that answers for another entry than the one asked, or with something that is not the summary, fails the handle: the
// core does not plan a prompt from a context it did not ask for.
func TestHostContextRejectsAnAnswerForAnotherContext(t *testing.T) {
	for name, answer := range map[string][][]abi.Value{
		"another tail": blobRow(`{"tail":8,"head":null,"system":[],"tokens":0}`),
		"not json":     blobRow(`nope`),
		"no row":       nil,
		"two columns":  {{abi.Blob([]byte(`{}`)), abi.Int(1)}},
	} {
		h, _ := contextRig(func(uint16, []abi.Value) ([][]abi.Value, error) { return answer, nil })
		if _, ok := h.hostContextOf(1, 9, 0, 0, false); ok || h.mir.lazy.err == nil {
			t.Errorf("%s: the summary was accepted", name)
		}
	}
}

// The beforeRequest hooks read the messages from the host, one read, and a hook-free request reads none.
func TestHostContextMessagesAreReadOnDemand(t *testing.T) {
	h, reads := contextRig(func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
		if sql == store.SQLContextMessages {
			return blobRow(`[{"role":"user","content":"a","timestamp":1},{"role":"user","content":"b","timestamp":2}]`), nil
		}
		return blobRow(`{"tail":5,"head":null,"system":[],"tokens":0}`), nil
	})
	c, _ := h.hostContextOf(2, 5, 0, 0, false)
	if len(*reads) != 1 {
		t.Fatalf("the summary read %d times", len(*reads))
	}
	got := c.Messages()
	if len(got) != 2 || len(*reads) != 2 || (*reads)[1].sql != store.SQLContextMessages {
		t.Fatalf("messages %d, reads %+v", len(got), *reads)
	}
}

// A model request whose context the host holds names the context's bounds as a range segment and carries no messages; the host
// derives the list (ABI section 10.2).
func TestHostModelContextPayloadNamesTheRange(t *testing.T) {
	h, _ := contextRig(func(uint16, []abi.Value) ([][]abi.Value, error) { return nil, nil })
	req := &turn.StreamRequest{Conversation: 6, Model: turn.ModelRef{Provider: "faux", ModelID: "m"}, Options: jv.NewObject()}
	var named [][3]uint32
	expand := func(payload []byte) string {
		t.Helper()
		out, err := abi.Expand(payload, func(conv, head, tail uint32) ([]byte, error) {
			named = append(named, [3]uint32{conv, head, tail})
			return []byte(`"RANGE"`), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	with := &hostContext{h: h, conv: 6, tail: 40, headID: 31, headFrom: 12}
	if got := expand(h.modelContextPayload(withContext(req, with))); got != `{"model":{"provider":"faux","modelId":"m"},"conversationId":6,"context":{"messages":["RANGE"]},"options":{}}` || len(named) != 1 || named[0] != [3]uint32{6, 31, 40} {
		t.Fatalf("payload %s, ranges %v", got, named)
	}
	without := &hostContext{h: h, conv: 6, tail: 40}
	named = nil
	expand(h.modelContextPayload(withContext(req, without)))
	if len(named) != 1 || named[0] != [3]uint32{6, 0, 40} {
		t.Fatalf("ranges %v", named)
	}
	// A request whose messages a beforeRequest hook replaced sends them whole: no range.
	replaced := withContext(req, with)
	replaced.Replaced = []*jv.Object{asObject(mustParse(t, `{"role":"user","content":"x","timestamp":1}`))}
	named = nil
	if got := expand(h.modelContextPayload(replaced)); !strings.Contains(got, `"context":{"messages":[{"role":"user"`) || len(named) != 0 {
		t.Fatalf("payload %s", got)
	}
}

func withContext(req *turn.StreamRequest, c turn.RequestContext) *turn.StreamRequest {
	out := *req
	out.Context = c
	return &out
}

func mustParse(t *testing.T, text string) any {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The scheduler dropping a conversation's kept context tells the host, and a core with no reader has no host to tell. The
// release goes through releaseContext, the scheduler's ReleaseContext hook (sched.go), so the hook's call is what is tested.
func TestReleasingAKeptContextTellsTheHost(t *testing.T) {
	h, reads := contextRig(func(uint16, []abi.Value) ([][]abi.Value, error) { return nil, nil })
	h.releaseContext(session.ID(8))
	if len(*reads) != 1 || (*reads)[0].sql != store.SQLContextRelease || len((*reads)[0].params) != 1 || (*reads)[0].params[0] != 8 {
		t.Fatalf("reads %+v, want one release of conversation 8", *reads)
	}
	(&handler{mir: newMirror()}).releaseContext(8)
}

// A summary read while compaction was disabled has no estimate: when a settings change enables compaction before the check, the
// check asks the host again instead of treating the context as empty.
func TestHostContextThresholdAsksForTheEstimateItLacks(t *testing.T) {
	h, reads := contextRig(func(_ uint16, params []abi.Value) ([][]abi.Value, error) {
		if params[4].Number() == 1 {
			return blobRow(`{"tail":9,"head":null,"system":[],"tokens":127000}`), nil
		}
		return blobRow(`{"tail":9,"head":null,"system":[],"tokens":0}`), nil
	})
	c, _ := h.hostContextOf(3, 9, 0, 0, false)
	policy := history.Policy{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000, BackgroundTokens: 32768}
	// Over the blocking threshold, range selection needs the whole view, which this handler cannot build: the check ends there.
	c.Threshold(nil, 128000, policy)
	if c.summary.tokens != 127000 || h.mir.lazy.err == nil {
		t.Fatalf("estimate %v: the check did not reach range selection with the host's estimate", c.summary.tokens)
	}
	if len(*reads) != 2 || (*reads)[1].params[4] != 1 {
		t.Fatalf("reads %+v, want the summary again with the estimate", *reads)
	}
}
