// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The partial patch protocol (partial_patch.go): patches of a stream add up to the message Pi's partial would hold.

func patched(t *testing.T, text string) *jv.Object {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jv.Object)
}

func TestPartialPatchesAddUpToTheWholePartial(t *testing.T) {
	var acc partialAcc
	acc.apply(patched(t, `{"role":"assistant","content":[{"type":"text","text":"","$a":"hel"},{"type":"toolCall","id":"c1","name":"w","arguments":{},"$o":[["r",{"path":"a"}]]}]}`))
	acc.apply(patched(t, `{"role":"assistant","content":[{"type":"text","text":"","$a":"lo"},{"type":"toolCall","id":"c1","name":"w","arguments":{},"$o":[["a",["path"],".ts"],["s",["n"],1]]}],"stopReason":"toolUse"}`))
	got := jv.Stringify(acc.message())
	want := `{"role":"assistant","content":[{"type":"text","text":"hello"},{"type":"toolCall","id":"c1","name":"w","arguments":{"path":"a.ts","n":1}}],"stopReason":"toolUse"}`
	if got != want {
		t.Fatalf("message %s\nwant    %s", got, want)
	}
}

func TestPartialWithNoMarkerReplacesWhatWasAccumulated(t *testing.T) {
	var acc partialAcc
	acc.apply(patched(t, `{"content":[{"type":"text","text":"abc","$a":""}]}`))
	acc.apply(patched(t, `{"content":[{"type":"text","text":"whole"},{"type":"toolCall","id":"c","name":"w","arguments":{"k":"v"}}]}`))
	if got, want := jv.Stringify(acc.message()), `{"content":[{"type":"text","text":"whole"},{"type":"toolCall","id":"c","name":"w","arguments":{"k":"v"}}]}`; got != want {
		t.Fatalf("message %s\nwant    %s", got, want)
	}
}

func TestPartialPatchJoinsASurrogatePairSplitAcrossPatches(t *testing.T) {
	var acc partialAcc
	acc.apply(patched(t, `{"content":[{"type":"text","text":"","$a":"a\ud83d"}]}`))
	acc.apply(patched(t, `{"content":[{"type":"text","text":"","$a":"\ude00b"}]}`))
	if got, want := jv.Stringify(acc.message()), `{"content":[{"type":"text","text":"a😀b"}]}`; got != want {
		t.Fatalf("message %s\nwant    %s", got, want)
	}
}

func TestPartialPatchBlocksFollowTheContentLength(t *testing.T) {
	var acc partialAcc
	acc.apply(patched(t, `{"content":[{"type":"text","text":"","$a":"x"},{"type":"text","text":"","$a":"y"}]}`))
	acc.apply(patched(t, `{"content":[{"type":"text","text":"","$a":"z"}]}`))
	if got, want := jv.Stringify(acc.message()), `{"content":[{"type":"text","text":"xz"}]}`; got != want {
		t.Fatalf("message %s\nwant    %s", got, want)
	}
}
