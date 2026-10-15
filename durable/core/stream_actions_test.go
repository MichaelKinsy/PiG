package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// referenceStreamActions is how the batch was read before streamActions split it instead of parsing it: parse the whole batch,
// build every partial and message as a tree, read the type of each event. It is the specification streamActions is held to.
func referenceStreamActions(payload []byte) ([]streamAction, error) {
	v, err := jv.Parse(payload)
	events, _ := v.([]any)
	if err != nil {
		return nil, err
	}
	var actions []streamAction
	for _, e := range events {
		eo := asObject(e)
		if p := objOf(eo, "partial"); p != nil {
			actions = append(actions, streamAction{kind: actPartial, partial: jv.Append(nil, p)})
		}
		switch strOf(eo, "type") {
		case "done":
			actions = append(actions, streamAction{kind: actDone, message: objOf(eo, "message")})
		case "error":
			actions = append(actions, streamAction{kind: actError, message: objOf(eo, "error")})
		case "fault":
			actions = append(actions, streamAction{kind: actFault, text: strOf(eo, "message")})
		}
	}
	return actions, nil
}

// checkStreamActions holds streamActions to the reference for one batch: the same error, the same actions in the same order, each
// partial and message equal to the reference's value (a partial is compared as the JSON text it prints).
func checkStreamActions(t testing.TB, batch string) {
	t.Helper()
	want, werr := referenceStreamActions([]byte(batch))
	got, gerr := streamActions([]byte(batch))
	if !errors.Is(gerr, werr) && (gerr != nil || werr != nil) {
		t.Fatalf("%q: error %v, want %v", batch, gerr, werr)
	}
	if len(got) != len(want) {
		t.Fatalf("%q: %d actions, want %d", batch, len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.kind != w.kind || g.text != w.text || (g.message == nil) != (w.message == nil) || (g.message != nil && jv.Stringify(g.message) != jv.Stringify(w.message)) {
			t.Fatalf("%q: action %d is %+v, want %+v", batch, i, g, w)
		}
		if w.kind == actPartial {
			parsed, err := jv.Parse(g.partial)
			if err != nil || jv.Stringify(parsed) != string(w.partial) {
				t.Fatalf("%q: partial %d is %q, want %q", batch, i, g.partial, w.partial)
			}
		}
	}
}

func TestStreamActionsAgreeWithTheParsedBatch(t *testing.T) {
	msg := `{"role":"assistant","content":[{"type":"text","text":"hi \u00e9\ud83d\ude00 \"q\""}],"api":"x","provider":"faux","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1.5e12}`
	for _, batch := range []string{
		// pi-ai's event shapes (types.ts AssistantMessageEvent).
		`[{"type":"start","partial":` + msg + `}]`,
		`[{"type":"text_start","contentIndex":0,"partial":` + msg + `},{"type":"text_delta","contentIndex":0,"delta":"hi","partial":` + msg + `}]`,
		`[{"type":"done","reason":"stop","message":` + msg + `}]`,
		`[{"type":"text_delta","partial":` + msg + `},{"type":"done","reason":"stop","message":` + msg + `}]`,
		`[{"type":"error","reason":"error","error":` + msg + `}]`,
		`[{"type":"fault","message":"the host could not serialize"}]`,
		`[{"type":"fault","message":"caf\u00e9"},{"type":"done","message":{}}]`,
		// events that give no action, or fewer than they hold.
		`[]`, ` [ ] `, `[{}]`, `[{"type":"start"}]`, `[{"type":"done"}]`, `[{"type":"done","message":null}]`, `[{"type":"done","message":"x"}]`,
		`[{"type":"error","error":[1]}]`, `[{"type":"fault"}]`, `[{"type":"fault","message":3}]`, `[{"type":"fault","message":null}]`,
		`[{"type":7,"message":{}}]`, `[{"type":null}]`, `[{"type":["done"]}]`, `[{"type":"Done"}]`, `[{"type":"done "}]`, `[{"partial":1}]`,
		`[{"partial":null}]`, `[{"partial":[]}]`, `[{"partial":"x","type":"done"}]`, `[1,"x",null,true,[],[{}],{"type":"done"}]`,
		// a name written twice counts at its last value; a name written with an escape is the name it decodes to.
		`[{"type":"done","type":"start","message":{"a":1}}]`, `[{"type":"start","type":"done","message":{"a":1},"message":{"a":2}}]`,
		`[{"t\u0079pe":"done","m\u0065ssage":{"a":1}}]`, `[{"partial":{"a":1},"partial":2}]`, `[{"partial":2,"partial":{"a":1}}]`,
		`[{"partial":{"content":[1],"content":[]}}]`, `[{"type":"d\u006fne","message":{"k":"v","k":"w"}}]`,
		// objects as the partial, whose members the batch never reads.
		`[{"partial":{"a":{"b":[{"c":"}"}]}},"type":"start"}]`, `[{"partial":{"s":"\\\"}"}}]`, `[{"partial":{"x":1e999,"y":-0,"z":"\ud800"}}]`,
		"[{\"partial\":{\"x\":\"\xed\xa0\x80\xff\"}}]", `[ { "type" : "done" , "message" : { "a" : [ 1 , 2 ] } } ]`,
		// batches that are not JSON, or not arrays: the error is jv.Parse's, or no action.
		``, ` `, `[`, `]`, `[{"type":"done"}`, `[{"type":"done"}]]`, `[{"type":"done",}]`, `[{"type":"done"},]`, `[{'type':'done'}]`, `{"type":"done"}`,
		`{}`, `null`, `1`, `"x"`, `[{"type":"done"}] x`, "[{\"type\":\"done\",\"message\":\"\n\"}]", `[{"type":"done","message":{"a":01}}]`,
		`[{"type":"fault","message":"\x"}]`, `[{"partial":{"a":"\u12"}}]`, `[{"type":"start"` + strings.Repeat(" ", 10) + `]`,
	} {
		checkStreamActions(t, batch)
	}
}

func FuzzStreamActionsAgreeWithTheParsedBatch(f *testing.F) {
	for _, s := range []string{
		`[{"type":"start","partial":{"role":"assistant","content":[]}}]`, `[{"type":"done","message":{"a":1}},{"type":"fault","message":"x"}]`,
		`[{"type":"done","type":"x","message":[]}]`, `[1,{"partial":{}},null]`, `{"type":"done"}`, `[{"type":"error","error":{"e":1},"partial":{"p":"\ud800"}}]`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { checkStreamActions(t, s) })
}
