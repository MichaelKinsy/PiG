package agent

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

func customOrderJSON(t *testing.T, m AgentMessage) string {
	t.Helper()
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonstringify.Canonicalize(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func decodeCustom(t *testing.T, text string) AgentMessage {
	t.Helper()
	var m AgentMessage
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// {...replacement, content: []} keeps the members in their order and puts content after them (agent-session.ts _emitExtensionEvent).
func TestCustomWithEmptyContentPutsContentLast(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		// node -e 'const r=JSON.parse(process.argv[1]); console.log(JSON.stringify({...r, content: []}))'
		{"canonical members", `{"role":"custom","customType":"c","display":true,"timestamp":1}`, `{"role":"custom","customType":"c","display":true,"timestamp":1,"content":[]}`},
		{"other order", `{"timestamp":1,"display":true,"customType":"c","role":"custom"}`, `{"timestamp":1,"display":true,"customType":"c","role":"custom","content":[]}`},
		{"null content", `{"role":"custom","content":null,"customType":"c"}`, `{"role":"custom","content":[],"customType":"c"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := customOrderJSON(t, decodeCustom(t, tc.in).WithEmptyContent()); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	t.Run("members set in Go use the role's order and content follows", func(t *testing.T) {
		m := AgentMessage{Custom: map[string]any{"role": "custom", "customType": "c", "display": true, "timestamp": float64(1)}}
		want := `{"role":"custom","customType":"c","display":true,"timestamp":1,"content":[]}`
		if got := customOrderJSON(t, m.WithEmptyContent()); got != want {
			t.Fatalf("got %s want %s", got, want)
		}
	})
}

// A replacement applied to one copy of a custom message reaches the member order of every copy, as Pi's in-place assignment reaches every holder of the object.
func TestAdoptMemberOrderReachesEveryCopy(t *testing.T) {
	original := AgentMessage{Custom: map[string]any{"role": "custom", "customType": "c", "content": "x", "display": true, "timestamp": float64(1)}}.WithOrderRecord()
	transcript, event := original, original
	replacement := decodeCustom(t, `{"timestamp":2,"role":"custom","customType":"c","display":false}`).WithEmptyContent()
	for key := range event.Custom {
		delete(event.Custom, key)
	}
	for key, value := range replacement.Custom {
		event.Custom[key] = value
	}
	event.AdoptMemberOrder(replacement)
	want := `{"timestamp":2,"role":"custom","customType":"c","display":false,"content":[]}`
	for name, m := range map[string]AgentMessage{"transcript": transcript, "event": event} {
		if got := customOrderJSON(t, m); got != want {
			t.Errorf("%s\n got %s\nwant %s", name, got, want)
		}
	}
	// A clone is another message.
	clone := transcript.Clone()
	clone.AdoptMemberOrder(decodeCustom(t, `{"role":"custom","customType":"c","timestamp":3,"display":true,"content":[]}`))
	if got := customOrderJSON(t, transcript); got != want {
		t.Errorf("a clone's replacement changed the original: %s", got)
	}
}

// A message without a record (a custom message built by hand, never sent through the loop) keeps its canonical order and ignores a replacement's.
func TestAdoptMemberOrderWithoutARecordChangesNothing(t *testing.T) {
	m := AgentMessage{Custom: map[string]any{"role": "custom", "customType": "c", "content": "x", "display": true, "timestamp": float64(1)}}
	m.AdoptMemberOrder(decodeCustom(t, `{"timestamp":2,"role":"custom","customType":"c","display":false,"content":"y"}`))
	want := `{"role":"custom","customType":"c","content":"x","display":true,"timestamp":1}`
	if got := customOrderJSON(t, m); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

// replaceCustomInPlace is what the session does to the message a message_end replacement reaches: the members of the shared object become the
// replacement's, in its order, in every copy that shares the record.
func replaceCustomInPlace(target, replacement AgentMessage) {
	clear(target.Custom)
	for key, value := range replacement.Custom {
		target.Custom[key] = value
	}
	target.AdoptMemberOrder(replacement)
}

// message_start carries its own envelope, so a message_end replacement of the message the transcript and message_end share must not reach it: the start
// copy keeps its members and their order (Pi's replacement reaches the object message_end shares, and a listener of message_start holds a snapshot here).
func TestStartEventMessageOfACustomMessageKeepsItsMembersAndOrderAcrossAReplacement(t *testing.T) {
	original := decodeCustom(t, `{"role":"custom","customType":"c","content":"x","display":true,"timestamp":1}`).WithOrderRecord()
	start := startEventMessage(original)
	before := customOrderJSON(t, start)
	replaceCustomInPlace(original, decodeCustom(t, `{"timestamp":2,"display":false,"customType":"c","role":"custom","content":"y"}`))
	if got := customOrderJSON(t, start); got != before {
		t.Errorf("the message_start copy after a replacement\n got %s\nwant %s", got, before)
	}
	want := `{"timestamp":2,"display":false,"customType":"c","role":"custom","content":"y"}`
	if got := customOrderJSON(t, original); got != want {
		t.Errorf("the replaced message\n got %s\nwant %s", got, want)
	}
}

// A listener may read the message_start copy while the replacement of the message_end message is adopted; the two share no memory.
func TestStartEventMessageOfACustomMessageIsRaceFreeWithAReplacement(t *testing.T) {
	original := decodeCustom(t, `{"role":"custom","customType":"c","content":"x","display":true,"timestamp":1}`).WithOrderRecord()
	start := startEventMessage(original)
	replacement := decodeCustom(t, `{"timestamp":2,"display":false,"customType":"c","role":"custom","content":"y"}`)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			if _, err := json.Marshal(start); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 200 {
		original.AdoptMemberOrder(replacement)
	}
	wg.Wait()
}
