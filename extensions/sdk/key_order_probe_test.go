package sdk

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A handler that moves, edits or copies the objects of an event in place leaves each of them in the member order the host wrote it. Each row is a way the
// objects were once given another object's order, or sorted: the order of an object is identified by the object itself, not by what it holds or where it stands.
// Every row runs on the wire of an isolated extension and on the decode a fused extension shares.
func TestObjectsKeepTheirOrderWhateverTheHandlerDoesToTheirPlace(t *testing.T) {
	const toolZeta = `{"type":"function","function":{"name":"zeta","parameters":{"type":"object","properties":{"yy":{"type":"string"},"bb":{"type":"string"}}}}}`
	const toolAlpha = `{"type":"function","function":{"name":"alpha","parameters":{"type":"object","properties":{"zz":{},"aa":{}}}}}`
	const shared = `"a":"s","b":"s","c":"s","d":"s"`
	reverse := func(list []any) { slices.Reverse(list) }
	member := func(payload map[string]any, key string) []any { return payload[key].([]any) }

	for _, test := range []struct {
		name    string
		payload string
		edit    func(payload map[string]any)
		want    string
	}{
		{
			"OpenAI tools sorted by function name",
			`{"model":"m","tools":[` + toolZeta + `,` + toolAlpha + `]}`,
			func(p map[string]any) {
				slices.SortFunc(member(p, "tools"), func(a, b any) int {
					name := func(tool any) string { return tool.(map[string]any)["function"].(map[string]any)["name"].(string) }
					return strings.Compare(name(a), name(b))
				})
			},
			`{"model":"m","tools":[` + toolAlpha + `,` + toolZeta + `]}`,
		},
		{
			"numeric-only objects reversed",
			`{"list":[{"x":1,"y":2},{"y":3,"x":4}]}`,
			func(p map[string]any) { reverse(member(p, "list")) },
			`{"list":[{"y":3,"x":4},{"x":1,"y":2}]}`,
		},
		{
			"a string edited in place, nothing moved",
			`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"}]}`,
			func(p map[string]any) { member(p, "messages")[0].(map[string]any)["content"] = "[redacted]" },
			`{"messages":[{"role":"user","content":"[redacted]"},{"role":"assistant","content":"yo"}]}`,
		},
		{
			"siblings that share every leading string, swapped",
			`{"calls":[{"type":"t","args":{"a":1,"b":2}},{"type":"t","args":{"b":1,"a":2}}]}`,
			func(p map[string]any) { reverse(member(p, "calls")) },
			`{"calls":[{"type":"t","args":{"b":1,"a":2}},{"type":"t","args":{"a":1,"b":2}}]}`,
		},
		{
			"swapped, and the moved element edited",
			`{"messages":[{"role":"user","content":"a","meta":1},{"role":"assistant","content":"b","meta":2}]}`,
			func(p map[string]any) {
				messages := member(p, "messages")
				reverse(messages)
				messages[0].(map[string]any)["content"] = "edited"
			},
			`{"messages":[{"role":"assistant","content":"edited","meta":2},{"role":"user","content":"a","meta":1}]}`,
		},
		{
			"the string that tells siblings apart is the fifth",
			`{"items":[{` + shared + `,"id":"x","o":{"a":1,"z":2}},{` + shared + `,"id":"y","o":{"z":1,"a":2}}]}`,
			func(p map[string]any) { reverse(member(p, "items")) },
			`{"items":[{` + shared + `,"id":"y","o":{"z":1,"a":2}},{` + shared + `,"id":"x","o":{"a":1,"z":2}}]}`,
		},
		{
			"strings with escapes",
			`{"items":[{"k":"line\n1","meta":{"a":1,"z":2}},{"k":"line\n2","meta":{"z":1,"a":2}}]}`,
			func(p map[string]any) { reverse(member(p, "items")) },
			`{"items":[{"k":"line\n2","meta":{"z":1,"a":2}},{"k":"line\n1","meta":{"a":1,"z":2}}]}`,
		},
		{
			"one object listed twice",
			`{"items":[{"b":1,"a":2},{"d":1,"c":2}]}`,
			func(p map[string]any) { items := member(p, "items"); items[1] = items[0] },
			`{"items":[{"b":1,"a":2},{"b":1,"a":2}]}`,
		},
		{
			"a copy of an object is another object",
			`{"items":[{"b":1,"a":2}]}`,
			func(p map[string]any) {
				items := member(p, "items")
				copied := map[string]any{}
				for key, value := range items[0].(map[string]any) {
					copied[key] = value
				}
				p["copy"] = copied
			},
			`{"items":[{"b":1,"a":2}],"copy":{"a":2,"b":1}}`,
		},
	} {
		t.Run(test.name+"/isolated", func(t *testing.T) {
			ext := New("key-order-probe")
			ext.OnEvent(EventBeforeProviderRequest, func(_ Context, data map[string]any) (any, error) {
				payload := data["payload"].(map[string]any)
				test.edit(payload)
				return payload, nil
			})
			dispatch := keyOrderFixture(t, ext)
			if got := dispatch(1, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":`+test.payload+`}`); got != test.want {
				t.Errorf("reply\n got %s\nwant %s", got, test.want)
			}
		})
		t.Run(test.name+"/fused", func(t *testing.T) {
			var memo eventArgsMemo
			data, _, order, release := memo.decodeWithMaster([]byte(`{"type":"before_provider_request","payload":` + test.payload + `}`))
			defer release()
			payload := data["payload"].(map[string]any)
			test.edit(payload)
			reply, ok := handedBack(EventBeforeProviderRequest, payload, order).(orderedReply)
			if !ok {
				t.Fatal("the reply is not written in the host's order")
			}
			got, err := json.MarshalJSOrdered(reply.value, reply.order)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Errorf("reply\n got %s\nwant %s", got, test.want)
			}
		})
	}
}

// A handler that edits a block of a context message in place, or every message, leaves the order of the objects it did not move: the tool call beside an
// edited text block, and each of 3000 messages. (TestRedactingEveryMessageTakesLinearTime bounds the time.)
func TestObjectsEditedInPlaceKeepTheirOrderInTheContext(t *testing.T) {
	const call = `{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"p","offset":1}}`
	const text = `{"type":"text","text":"t"}`
	const edited = `{"type":"text","text":"edited"}`
	ext := New("key-order-edited")
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		data["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] = "edited"
		return nil, nil
	})
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		for _, message := range data["messages"].([]any) {
			message.(map[string]any)["content"] = "[redacted]"
		}
		return nil, nil
	})
	dispatch := keyOrderFixture(t, ext)

	assistant := func(content string) string {
		return `{"role":"assistant","content":` + content + `,"stopReason":"toolUse","timestamp":2}`
	}
	if got, want := dispatch(1, EventContext, `{"type":"context","messages":[`+assistant(`[`+text+`,`+call+`]`)+`]}`),
		`{"_pigContextUnchanged":true,"messages":[`+assistant(`[`+edited+`,`+call+`]`)+`]}`; got != want {
		t.Errorf("an edited text block\n got %s\nwant %s", got, want)
	}

	args, want := redactAllArgs(3000)
	if got := dispatch(2, EventContext, args); got != want {
		t.Errorf("redacting every message changed the order of their members (reply of %d bytes)", len(got))
	}
}

// redactAllArgs is a context of count messages and the reply of a handler that sets the content of each in place.
func redactAllArgs(count int) (args, reply string) {
	var a, r strings.Builder
	a.WriteString(`{"type":"context","messages":[`)
	r.WriteString(`{"_pigContextUnchanged":true,"messages":[`)
	for i := range count {
		if i > 0 {
			a.WriteByte(',')
			r.WriteByte(',')
		}
		fmt.Fprintf(&a, `{"role":"user","content":"message number %d","timestamp":%d,"meta":{"z":1,"a":2}}`, i, i)
		fmt.Fprintf(&r, `{"role":"user","content":"[redacted]","timestamp":%d,"meta":{"z":1,"a":2}}`, i)
	}
	a.WriteString(`]}`)
	r.WriteString(`]}`)
	return a.String(), r.String()
}

// askedOrder is a KeyOrder that counts the questions the reply's encoder asks of the order of an event.
type askedOrder struct {
	order json.KeyOrder
	asked int
}

func (a *askedOrder) Keys(m uintptr) ([]string, bool) { a.asked++; return a.order.Keys(m) }

// The reply of a context handler that returned nothing holds no object of the event, and a map of one member has no order to choose: the reply must not look
// up the order, whose first lookup sorts every object of the event.
func TestAnUnchangedContextReplyReadsNoOrder(t *testing.T) {
	_, order, err := json.UnmarshalObject([]byte(contextArgs(300)))
	if err != nil {
		t.Fatal(err)
	}
	asked := &askedOrder{order: order}
	reply := handedBack(EventContext, map[string]any{"_pigContextUnchanged": true}, order).(orderedReply)
	reply.order = asked
	if _, err := encodeResponse("id", reply, nil); err != nil {
		t.Fatal(err)
	}
	if asked.asked != 0 {
		t.Errorf("an unchanged context reply asked for the order %d times", asked.asked)
	}
}

// A fused extension's copy of an object that the handler dropped does not hand its order to a map that takes its address: the order keeps the copies.
func TestFusedCopiesDoNotLeakTheOrderOfAFreedObject(t *testing.T) {
	const nested = 500
	var text strings.Builder
	text.WriteString(`{"payload":{"keep":1`)
	for i := range nested {
		fmt.Fprintf(&text, `,"n%d":{"z":1,"y":2}`, i)
	}
	text.WriteString(`}}`)
	var memo eventArgsMemo
	data, _, order, release := memo.decodeWithMaster([]byte(text.String()))
	defer release()
	payload := data["payload"].(map[string]any)
	// The first lookup reads every copy; the handler then drops them all.
	if keys, ok := order.KeysOf(payload["n0"].(map[string]any)); !ok || keys[0] != "z" {
		t.Fatalf("n0 = %v, %v", keys, ok)
	}
	for i := range nested {
		delete(payload, fmt.Sprintf("n%d", i))
	}
	// The freed copies are swept and their size class is allocated again, round after round, until nothing could be left to reuse.
	for range 20 {
		runtime.GC()
		fresh := make([]map[string]any, 0, 8*nested)
		for range 8 * nested {
			fresh = append(fresh, map[string]any{"y": 1, "z": 2})
		}
		for _, m := range fresh {
			if keys, ok := order.KeysOf(m); ok {
				t.Fatalf("a new map got the order of a freed copy: %v", keys)
			}
		}
	}
}
