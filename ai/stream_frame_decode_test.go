package ai

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// Events keep their Raw lines when the state moves on to a new slab, and no two events share a line's storage.
func TestAnthropicSSERawSlabRollsOverWithoutSharing(t *testing.T) {
	var state anthropicSSEState
	var events []serverSentEvent
	var want [][]string
	for i := range 3*anthropicRawSlabLines + 5 {
		lines := []string{fmt.Sprintf("event: e%d", i), fmt.Sprintf("data: %d", i)}
		if i%7 == 0 {
			lines = append([]string{": keepalive"}, lines...)
		}
		want = append(want, lines)
		for _, line := range append(lines, "") {
			if event, ok := state.decode(line); ok {
				events = append(events, event)
			}
		}
	}
	for i, event := range events {
		if !reflect.DeepEqual(event.Raw, want[i]) {
			t.Fatalf("event %d Raw = %q, want %q", i, event.Raw, want[i])
		}
		if len(event.Raw) != cap(event.Raw) {
			t.Fatalf("event %d Raw has spare capacity %d: an append to it would overwrite the next event's lines", i, cap(event.Raw)-len(event.Raw))
		}
		if wantData := fmt.Sprint(i); event.Data != wantData {
			t.Fatalf("event %d Data = %q, want %q", i, event.Data, wantData)
		}
	}
}

// The valid-JSON check reads a reused buffer: it agrees with json.Valid for every record, and a later record does not see the bytes of an earlier one.
func TestAnthropicParseJSONWithScratchMatchesTheUnbufferedParse(t *testing.T) {
	var scratch []byte
	records := []string{`{"a":1}`, `{"a":`, `[1,2,`, `{"type":"x","delta":{"text":"\ud83d"}}`, `null`, `{"a":1}   `, `{"k":"v",}`, ``, `{"b":[1,2,{"c":"d"}]}`, `{"unterminated":"str`}
	for _, record := range records {
		text, valid, err := anthropicParseJSONWith(record, &scratch)
		wantText, wantValid, wantErr := anthropicParseJSON(record)
		if text != wantText || valid != wantValid || (err == nil) != (wantErr == nil) || err != nil && err.Error() != wantErr.Error() {
			t.Errorf("%q: scratch parse = (%q, %v, %v), unbuffered = (%q, %v, %v)", record, text, valid, err, wantText, wantValid, wantErr)
		}
	}
}

// ownedStr reads what str reads, and its result is a string of its own.
func TestChunkScannerOwnedStrMatchesStr(t *testing.T) {
	for _, literal := range []string{`"plain"`, `""`, `"a\nb"`, `"\u00e9\ud83d\ude00"`, `"tab\there"`, `"quote\"inside"`, `"é😀"`, `"bad\x"`, `"unterminated`, `"ctl` + "\x01" + `"`, `"\ud800"`} {
		plain := chunkScanner{s: literal}
		owned := chunkScanner{s: literal}
		want, wantOK := plain.str()
		got, gotOK := owned.ownedStr()
		if got != want || gotOK != wantOK || plain.i != owned.i {
			t.Errorf("%s: ownedStr = (%q, %v, at %d), str = (%q, %v, at %d)", literal, got, gotOK, owned.i, want, wantOK, plain.i)
		}
	}
}

// appendJSONStringUnits decodes only what JSON.parse decodes the same way: whenever it answers, jsonParseValue gives the same string; text it leaves is jsonParseValue's.
func TestAppendJSONStringUnitsAgreesWithJSONParse(t *testing.T) {
	pieces := []string{"a", "z", " ", "é", "😀", "\u2028", `\n`, `\t`, `\"`, `\\`, `\/`, `\b`, `\f`, `\r`, `\u0041`, `\u00e9`, `\u0000`, `\ud83d\ude00`, `\ud83d`, `\ude00`, `\x`, `\u12`, "\x01", "\xff", `\`, `"`}
	random := rand.New(rand.NewPCG(1, 2))
	answered := 0
	for range 20000 {
		var raw strings.Builder
		for range random.IntN(8) {
			raw.WriteString(pieces[random.IntN(len(pieces))])
		}
		got, ok := appendJSONStringUnits(nil, raw.String())
		if !ok {
			continue
		}
		answered++
		value, parsed := jsonParseValue(`"` + raw.String() + `"`)
		if !parsed || value.(string) != string(got) {
			t.Fatalf("%q: appendJSONStringUnits = %q, jsonParseValue = %q (%v)", raw.String(), got, value, parsed)
		}
	}
	if answered < 1000 {
		t.Fatalf("only %d of the strings were decoded directly", answered)
	}
}
