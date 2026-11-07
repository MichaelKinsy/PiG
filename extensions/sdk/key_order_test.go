package sdk

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// keyOrderFixture runs ext against a mock host and returns a function that sends one event request and reports the result text of its response.
func keyOrderFixture(t *testing.T, ext *Extension) (dispatch func(handlerID int, event, args string) string) {
	t.Helper()
	host := newMockHost(t)
	t.Cleanup(host.close)
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: t.TempDir(), Width: 80}})
	sequence := 0
	t.Cleanup(func() {
		host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
		<-done
	})
	return func(handlerID int, event, args string) string {
		t.Helper()
		sequence++
		id := "order-" + strconv.Itoa(sequence)
		host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "event", Event: event, HandlerID: handlerID, Args: []byte(args)}})
		resp := host.readEnvelope(t)
		if resp.Type != msgResponse || resp.ID != id || resp.Response == nil || resp.Response.Error != nil {
			t.Fatalf("%s response = %+v", event, resp)
		}
		return string(resp.Response.Result)
	}
}

// The objects of an event that a handler hands back keep the member order the host wrote them in, as Pi's objects do: the entries of a turn boundary are
// stored as they come back and the messages of the context are sent to the provider as they come back.
func TestHandedBackEventObjectsKeepTheirMemberOrder(t *testing.T) {
	const entry = `{"type":"custom","customType":"other","data":{"z":1,"a":2,"m":{"y":1,"b":[{"q":1,"c":2},{"d":3,"a":4}]}}}`
	const second = `{"type":"custom_message","customType":"x","content":"c","display":true,"details":{"k":1,"b":2}}`
	const boundary = `{"type":"turn_end","turnIndex":1,"entries":[` + entry + `,` + second + `],"continue":false,"context":{"pendingMessages":[]},"outcome":"completed"}`

	ext := New("key-order")
	// 1: leaves the entries alone.
	ext.OnEvent(EventTurnEnd, func(Context, map[string]any) (any, error) { return nil, nil })
	// 2: appends an entry through the event.
	ext.OnEvent(EventTurnEnd, func(_ Context, data map[string]any) (any, error) {
		data["entries"] = append(data["entries"].([]any), map[string]any{"type": "custom", "customType": "mine", "data": map[string]any{"b": 1, "a": 2}})
		return nil, nil
	})
	// 3: returns the entries it received, in a list of its own, with one more.
	ext.OnEvent(EventTurnEnd, func(_ Context, data map[string]any) (any, error) {
		kept := append([]any{}, data["entries"].([]any)...)
		return map[string]any{"entries": append(kept, map[string]any{"type": "custom", "customType": "mine"})}, nil
	})
	// 4: edits a member of an entry in place: a retained member keeps its place, a new member follows, a deleted one is gone.
	ext.OnEvent(EventTurnEnd, func(_ Context, data map[string]any) (any, error) {
		inner := data["entries"].([]any)[0].(map[string]any)["data"].(map[string]any)
		inner["new"] = 3
		inner["z"] = 9
		delete(inner, "a")
		return nil, nil
	})
	// 5: replaces an entry with a copy of its own, which is another object.
	ext.OnEvent(EventTurnEnd, func(_ Context, data map[string]any) (any, error) {
		entries := data["entries"].([]any)
		data["entries"] = []any{map[string]any{"type": "custom", "customType": "other", "data": entries[0].(map[string]any)["data"]}}
		return nil, nil
	})
	// 6: drops the first entry.
	ext.OnEvent(EventTurnEnd, func(_ Context, data map[string]any) (any, error) {
		data["entries"] = data["entries"].([]any)[1:]
		return nil, nil
	})
	dispatch := keyOrderFixture(t, ext)

	for _, test := range []struct{ name, handler, want string }{
		{"untouched", "1", `{"_pigBoundaryEntries":[` + entry + `,` + second + `],"_pigBoundaryResult":null}`},
		{"appended", "2", `{"_pigBoundaryEntries":[` + entry + `,` + second + `,{"customType":"mine","data":{"a":2,"b":1},"type":"custom"}],"_pigBoundaryResult":null}`},
		{"returned", "3", `{"_pigBoundaryEntries":[` + entry + `,` + second + `],"_pigBoundaryResult":{"entries":[` + entry + `,` + second + `,{"customType":"mine","type":"custom"}]}}`},
		{"edited in place", "4", `{"_pigBoundaryEntries":[{"type":"custom","customType":"other","data":{"z":9,"m":{"y":1,"b":[{"q":1,"c":2},{"d":3,"a":4}]},"new":3}},` + second + `],"_pigBoundaryResult":null}`},
		{"rebuilt around the same data", "5", `{"_pigBoundaryEntries":[{"customType":"other","data":{"z":1,"a":2,"m":{"y":1,"b":[{"q":1,"c":2},{"d":3,"a":4}]}},"type":"custom"}],"_pigBoundaryResult":null}`},
		{"dropped", "6", `{"_pigBoundaryEntries":[` + second + `],"_pigBoundaryResult":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := strconv.Atoi(test.handler)
			if got := dispatch(handler, EventTurnEnd, boundary); got != test.want {
				t.Errorf("reply\n got %s\nwant %s", got, test.want)
			}
		})
	}
}

func TestHandedBackContextMessagesKeepTheirMemberOrder(t *testing.T) {
	const user = `{"role":"user","content":"hello","timestamp":1}`
	const assistant = `{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a.txt","offset":1,"limit":2,"extra":{"z":1,"a":2}}},{"type":"text","text":"t"}],"stopReason":"toolUse","timestamp":2}`
	const result = `{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":3}`
	const args = `{"type":"context","messages":[` + user + `,` + assistant + `,` + result + `]}`

	ext := New("key-order-context")
	// 1: drops the first message.
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		return map[string]any{"messages": data["messages"].([]any)[1:]}, nil
	})
	// 2: reorders the messages.
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		m := data["messages"].([]any)
		return map[string]any{"messages": []any{m[2], m[1], m[0]}}, nil
	})
	// 3: edits a tool call's argument in place and drops the user message.
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		m := data["messages"].([]any)
		call := m[1].(map[string]any)["content"].([]any)[0].(map[string]any)["arguments"].(map[string]any)
		call["offset"] = 5
		call["added"] = true
		return map[string]any{"messages": m[1:]}, nil
	})
	// 4: sets the list in place.
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		data["messages"] = data["messages"].([]any)[1:]
		return nil, nil
	})
	dispatch := keyOrderFixture(t, ext)

	for _, test := range []struct {
		name    string
		handler int
		event   string
		want    string
	}{
		{"dropped", 1, EventContext, `{"_pigContextUnchanged":false,"messages":[` + assistant + `,` + result + `]}`},
		{"reordered", 2, EventContext, `{"_pigContextUnchanged":false,"messages":[` + result + `,` + assistant + `,` + user + `]}`},
		{"edited argument", 3, EventContext, `{"_pigContextUnchanged":false,"messages":[` + strings.Replace(assistant, `"offset":1,"limit":2,"extra":{"z":1,"a":2}`, `"offset":5,"limit":2,"extra":{"z":1,"a":2},"added":true`, 1) + `,` + result + `]}`},
		{"in place", 4, EventContext, `{"_pigContextUnchanged":false,"messages":[` + assistant + `,` + result + `]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := dispatch(test.handler, test.event, args); got != test.want {
				t.Errorf("reply\n got %s\nwant %s", got, test.want)
			}
		})
	}
}

func TestHandedBackEventMembersKeepTheirMemberOrder(t *testing.T) {
	ext := New("key-order-members")
	ext.OnEvent(EventBeforeProviderRequest, func(_ Context, data map[string]any) (any, error) { return data["payload"], nil })
	ext.OnEvent("tool_result", func(_ Context, data map[string]any) (any, error) {
		return map[string]any{"content": data["content"], "details": data["details"]}, nil
	})
	ext.OnEvent("message_end", func(_ Context, data map[string]any) (any, error) {
		return map[string]any{"message": data["message"]}, nil
	})
	dispatch := keyOrderFixture(t, ext)

	const payload = `{"model":"m","messages":[{"role":"user","content":"x"}],"stream":true,"zeta":{"z":1,"a":2}}`
	if got := dispatch(1, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":`+payload+`}`); got != payload {
		t.Errorf("payload\n got %s\nwant %s", got, payload)
	}
	const content = `[{"type":"text","text":"ok"}]`
	const details = `{"z":1,"a":{"y":1,"b":2}}`
	if got, want := dispatch(2, "tool_result", `{"type":"tool_result","toolCallId":"c","toolName":"read","input":{},"content":`+content+`,"details":`+details+`,"isError":false}`), `{"content":`+content+`,"details":`+details+`}`; got != want {
		t.Errorf("tool_result\n got %s\nwant %s", got, want)
	}
	const message = `{"role":"custom","customType":"x","content":"c","display":true,"details":{"z":1,"a":2},"timestamp":1}`
	if got, want := dispatch(3, "message_end", `{"type":"message_end","message":`+message+`}`), `{"message":`+message+`}`; got != want {
		t.Errorf("message_end\n got %s\nwant %s", got, want)
	}
}

// A handler reads the keys of an object of its event in the order the host wrote them, as Object.keys gives them in Pi; a map it built, or a member it added,
// has no host order.
func TestObjectKeysAreReadInTheOrderTheHostWroteThem(t *testing.T) {
	ext := New("key-order-read")
	ext.OnEvent(EventBeforeProviderRequest, func(ctx Context, data map[string]any) (any, error) {
		payload := data["payload"].(map[string]any)
		zeta := payload["zeta"].(map[string]any)
		before := ctx.ObjectKeys(payload)
		payload["extra"] = 1
		delete(payload, "stream")
		return map[string]any{"before": before, "after": ctx.ObjectKeys(payload), "zeta": ctx.ObjectKeys(zeta), "built": ctx.ObjectKeys(map[string]any{"b": 1, "a": 2}), "nil": ctx.ObjectKeys(nil)}, nil
	})
	dispatch := keyOrderFixture(t, ext)
	got := dispatch(1, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":{"model":"m","messages":[],"stream":true,"zeta":{"z":1,"a":2}}}`)
	const want = `{"after":["model","messages","zeta","extra"],"before":["model","messages","stream","zeta"],"built":["a","b"],"nil":[],"zeta":["z","a"]}`
	if got != want {
		t.Errorf("keys\n got %s\nwant %s", got, want)
	}
}

// An array a handler reorders in place keeps the member order of the objects in it: the blocks of a message (a tool call's arguments), the tools of a
// request (their schemas), the messages of a list, the entries of a boundary.
func TestObjectsMovedInPlaceKeepTheirMemberOrder(t *testing.T) {
	const call = `{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"p","offset":1}}`
	const text = `{"type":"text","text":"t"}`
	const user = `{"role":"user","content":"u","timestamp":1}`
	const assistant = `{"role":"assistant","content":[` + call + `,` + text + `],"stopReason":"toolUse","timestamp":2}`
	const swapped = `{"role":"assistant","content":[` + text + `,` + call + `],"stopReason":"toolUse","timestamp":2}`
	const result = `{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":3}`
	const tools = `[{"name":"zeta","input_schema":{"type":"object","properties":{"yy":{"type":"string"},"bb":{"type":"string"}}}},{"name":"alpha","input_schema":{"type":"object","properties":{"zz":{},"aa":{}}}}]`
	const sorted = `[{"name":"alpha","input_schema":{"type":"object","properties":{"zz":{},"aa":{}}}},{"name":"zeta","input_schema":{"type":"object","properties":{"yy":{"type":"string"},"bb":{"type":"string"}}}}]`
	const entryA = `{"type":"custom","customType":"a","data":{"z":1,"a":2}}`
	const entryB = `{"type":"custom","customType":"b","data":{"y":1,"b":2}}`

	ext := New("key-order-moved")
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		content := data["messages"].([]any)[1].(map[string]any)["content"].([]any)
		content[0], content[1] = content[1], content[0]
		return nil, nil
	})
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		m := data["messages"].([]any)
		m[0], m[2] = m[2], m[0]
		return nil, nil
	})
	ext.OnEvent(EventBeforeProviderRequest, func(_ Context, data map[string]any) (any, error) {
		list := data["payload"].(map[string]any)["tools"].([]any)
		slices.SortFunc(list, func(a, b any) int {
			return strings.Compare(a.(map[string]any)["name"].(string), b.(map[string]any)["name"].(string))
		})
		return data["payload"], nil
	})
	ext.OnEvent(EventTurnEnd, func(_ Context, data map[string]any) (any, error) {
		entries := data["entries"].([]any)
		entries[0], entries[1] = entries[1], entries[0]
		return nil, nil
	})
	ext.OnEvent("context_with_system", func(_ Context, data map[string]any) (any, error) {
		return map[string]any{"messages": data["messages"].([]any)[1:]}, nil
	})
	ext.OnEvent("agent_before_settle", func(Context, map[string]any) (any, error) { return nil, nil })
	dispatch := keyOrderFixture(t, ext)

	contextArgs := `{"type":"context","messages":[` + user + `,` + assistant + `,` + result + `]}`
	for _, test := range []struct {
		name, got, want string
	}{
		{"blocks of a message", dispatch(1, EventContext, contextArgs), `{"_pigContextUnchanged":true,"messages":[` + user + `,` + swapped + `,` + result + `]}`},
		{"messages of the list", dispatch(2, EventContext, contextArgs), `{"_pigContextUnchanged":false,"messages":[` + result + `,` + assistant + `,` + user + `]}`},
		{"tools of a request", dispatch(3, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":{"model":"m","tools":`+tools+`}}`), `{"model":"m","tools":` + sorted + `}`},
		{"entries of a boundary", dispatch(4, EventTurnEnd, `{"type":"turn_end","entries":[`+entryA+`,`+entryB+`],"continue":false}`), `{"_pigBoundaryEntries":[` + entryB + `,` + entryA + `],"_pigBoundaryResult":null}`},
		{"context with system", dispatch(5, "context_with_system", `{"type":"context_with_system","messages":[`+user+`,`+assistant+`]}`), `{"_pigContextUnchanged":false,"messages":[` + assistant + `]}`},
		{"agent before settle", dispatch(6, "agent_before_settle", `{"type":"agent_before_settle","entries":[`+entryA+`,`+entryB+`],"continue":false}`), `{"_pigBoundaryEntries":[` + entryA + `,` + entryB + `],"_pigBoundaryResult":null}`},
	} {
		if test.got != test.want {
			t.Errorf("%s\n got %s\nwant %s", test.name, test.got, test.want)
		}
	}
}

// Array-index keys come first, ascending, as Object.keys gives them, whether the host wrote them so or the handler added them.
func TestObjectKeysPutArrayIndexKeysFirst(t *testing.T) {
	ext := New("key-order-index")
	ext.OnEvent(EventBeforeProviderRequest, func(ctx Context, data map[string]any) (any, error) {
		payload := data["payload"].(map[string]any)
		before := ctx.ObjectKeys(payload)
		payload["0"] = 1
		payload["01"] = 1
		return map[string]any{"before": before, "after": ctx.ObjectKeys(payload), "built": ctx.ObjectKeys(map[string]any{"10": 1, "9": 1, "a": 1, "4294967295": 1, "4294967294": 1})}, nil
	})
	dispatch := keyOrderFixture(t, ext)
	got := dispatch(1, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":{"10":1,"b":2,"9":3,"a":4}}`)
	const want = `{"after":["0","9","10","b","a","01"],"before":["9","10","b","a"],"built":["9","10","4294967294","4294967295","a"]}`
	if got != want {
		t.Errorf("keys\n got %s\nwant %s", got, want)
	}
}

// The order of an object that was read is not handed to a map that later takes the address the object had. The objects here are nested ones the handler drops
// from the payload and nothing else holds, which the order must keep alive: a freed address cannot be taken while the order names it.
func TestObjectKeysDoNotLeakTheOrderOfAFreedObject(t *testing.T) {
	const nested = 500
	var payload strings.Builder
	payload.WriteString(`{"keep":1`)
	for i := range nested {
		fmt.Fprintf(&payload, `,"n%d":{"z":1,"y":2}`, i)
	}
	payload.WriteString(`}`)

	ext := New("key-order-reuse")
	var failed string
	ext.OnEvent(EventBeforeProviderRequest, func(ctx Context, data map[string]any) (any, error) {
		object := data["payload"].(map[string]any)
		for i := range nested {
			key := fmt.Sprintf("n%d", i)
			if keys := ctx.ObjectKeys(object[key].(map[string]any)); keys[0] != "z" {
				failed = "a nested object is not read in the host's order: " + strings.Join(keys, ",")
			}
			delete(object, key)
		}
		runtime.GC()
		runtime.GC()
		fresh := make([]map[string]any, 0, 4*nested)
		for range 4 * nested {
			fresh = append(fresh, map[string]any{"y": 1, "z": 2})
		}
		for _, m := range fresh {
			if keys := ctx.ObjectKeys(m); keys[0] != "y" {
				failed = "a new map got the order of a freed object: " + strings.Join(keys, ",")
				break
			}
		}
		return nil, nil
	})
	dispatch := keyOrderFixture(t, ext)
	dispatch(1, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":`+payload.String()+`}`)
	if failed != "" {
		t.Error(failed)
	}
}

// A Context a handler kept holds neither the payload nor its order once the reply is sent: it reads byte order from then on.
func TestAKeptContextReadsByteOrderAfterTheReply(t *testing.T) {
	ext := New("key-order-kept")
	var kept Context
	var object map[string]any
	ext.OnEvent(EventBeforeProviderRequest, func(ctx Context, data map[string]any) (any, error) {
		kept, object = ctx, data["payload"].(map[string]any)
		if got := ctx.ObjectKeys(object); got[0] != "z" {
			t.Errorf("during the request: %v", got)
		}
		return nil, nil
	})
	dispatch := keyOrderFixture(t, ext)
	dispatch(1, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":{"z":1,"a":2}}`)
	// The request releases its payload after it has answered.
	deadline := time.Now().Add(5 * time.Second)
	for {
		kept.req.keyMu.Lock()
		released := kept.req.keyOrder == nil && kept.req.Args == nil
		kept.req.keyMu.Unlock()
		if released {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the request still holds its payload and the order of its objects after the reply")
		}
		time.Sleep(time.Millisecond)
	}
	if got := kept.ObjectKeys(object); !slices.Equal(got, []string{"a", "z"}) {
		t.Errorf("after the reply ObjectKeys = %v, want byte order", got)
	}
}
