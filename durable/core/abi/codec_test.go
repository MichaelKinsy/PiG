// SPDX-License-Identifier: MIT

package abi

import (
	"bytes"
	"math"
	"testing"
)

func sampleStep() *Step {
	return &Step{
		Commits: []Commit{
			{Seq: 7, Stmts: []Stmt{
				{SQL: 1, Params: []Value{Null(), Int(-1 << 53), Float(math.Copysign(0, -1)), Text("é\u2028"), Blob([]byte{0, 1, 255}), Text("")}},
				{SQL: 2},
			}},
			{Seq: -1},
			{Seq: 8, Stmts: []Stmt{{SQL: 65535, Params: []Value{Float(math.NaN())}}}},
		},
		Reads:   []Read{{ID: 4, SQL: 9, Params: []Value{Int(3)}}},
		Notices: []Notice{{Kind: NoticeReport, Payload: []byte(`{"a":1}`)}, {Kind: NoticeSidecar}},
		Effects: []Effect{{ID: 1, Kind: EffectTimer, Payload: TimerEffect(1, 12.5, true)}, {ID: 2, Kind: EffectLiveness, Payload: F64Payload(-1)}},
	}
}

func TestStepRoundTrip(t *testing.T) {
	for _, s := range []*Step{{}, sampleStep(), {Status: StatusRejected, Err: []byte(`{"name":"X"}`)}, {Status: StatusFatal, Err: nil}} {
		wire := AppendStep(nil, s)
		got, err := DecodeStep(wire)
		if err != nil {
			t.Fatal(err)
		}
		if again := AppendStep(nil, got); !bytes.Equal(again, wire) {
			t.Fatalf("round trip changed the bytes:\n%x\n%x", wire, again)
		}
		if got.Status != s.Status || len(got.Commits) != len(s.Commits) || len(got.Effects) != len(s.Effects) {
			t.Fatalf("decoded step differs: %+v", got)
		}
	}
}

func TestStepLengthPrefixes(t *testing.T) {
	wire := AppendStep(nil, sampleStep())
	if size := int(wire[0]) | int(wire[1])<<8 | int(wire[2])<<16 | int(wire[3])<<24; size != len(wire)-4 {
		t.Fatalf("size %d, want %d", size, len(wire)-4)
	}
	for i := range wire {
		if _, err := DecodeStep(wire[:i]); err == nil {
			t.Fatalf("a step truncated to %d bytes decoded", i)
		}
	}
	if _, err := DecodeStep(append(wire, 0)); err == nil {
		t.Fatal("trailing byte accepted")
	}
}

func TestRowsRoundTrip(t *testing.T) {
	in := []ReadRows{
		{ID: 1, Cols: 2, Rows: [][]Value{{Int(1), Text("a")}, {Null(), Blob([]byte("b"))}}},
		{ID: 2, Cols: 1},
	}
	got, err := DecodeRows(AppendRows(nil, in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || len(got[0].Rows) != 2 || !got[0].Rows[1][1].Equal(Blob([]byte("b"))) || len(got[1].Rows) != 0 {
		t.Fatalf("rows changed: %+v", got)
	}
}
