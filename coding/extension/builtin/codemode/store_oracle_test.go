package codemode

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// execute.ts readCodemodeStore folds the codemode-store entries of a branch, and host.ts serializeStore turns the result into the JSON text the script's
// load() parses. Pi's readCodemodeStore and serializeStore (written out as in host.ts) run on random branches; storeOf and the text codemode sends the
// sandbox must name the same keys, by UTF-16 code unit, with the same JSON text once serialized (the sandbox side is codemode.TestStoreSnapshotMatchesPi).
func TestCodemodeStoreMatchesPi(t *testing.T) {
	keys := []string{`"a"`, `"b"`, `"0"`, `"1"`, `"10"`, `"__proto__"`, `"constructor"`, `"\ud800"`, `"\ud801"`, `"\udc00"`, `"\u00e9"`, `"e\u0301"`, `"\u0000"`, `"x y"`}
	values := []string{`1`, `1.0`, `1e2`, `1E400`, `-0`, `0.1e1`, `12345678901234567890`, `1.5e-7`, `"x"`, `"\ud800"`, `"\u00e9"`, `"\/"`, `[1,2]`, `{"b":1,"a":2}`, `{"a":1,"a":2}`, `{ "a" : [ 1 , 2 ] }`, `null`, `true`, `false`, `{}`, `[]`, `""`}
	rng := rand.New(rand.NewPCG(7, 11))
	pick := func(from []string) string { return from[rng.IntN(len(from))] }
	var branches [][]string
	for range 400 {
		var branch []string
		for range 1 + rng.IntN(4) {
			var data string
			switch rng.IntN(12) {
			case 0:
				data = `{"set":[` + pick(values) + `,` + pick(values) + `],"delete":[]}`
			case 1:
				data = `{"set":{` + pick(keys) + `:1},"delete":[null]}`
			case 2:
				data = `{"set":null,"delete":[]}`
			default:
				var set, del []string
				for range rng.IntN(4) {
					set = append(set, pick(keys)+":"+pick(values))
				}
				for range rng.IntN(3) {
					del = append(del, pick(keys))
				}
				data = `{"set":{` + strings.Join(set, ",") + `},"delete":[` + strings.Join(del, ",") + `]}`
			}
			custom := "codemode-store"
			if rng.IntN(15) == 0 {
				custom = "other"
			}
			branch = append(branch, fmt.Sprintf(`{"type":"custom","customType":%q,"data":%s,"id":"x","parentId":null,"timestamp":"1970-01-01T00:00:00.000Z"}`, custom, data))
		}
		branches = append(branches, branch)
	}
	type unitsText struct {
		Units string `json:"units"`
		Text  string `json:"text"`
	}
	var want [][]unitsText
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/codemode/execute.js");
emit(input.map((branch) => {
	const store = m.readCodemodeStore(branch.map((line) => JSON.parse(line)));
	const serialized = {};
	for (const [key, value] of Object.entries(store)) {
		const json = JSON.stringify(value);
		if (json !== undefined) serialized[key] = json;
	}
	return Object.entries(serialized).map(([key, text]) => ({ units: [...key].length === 0 ? "" : Array.from({ length: key.length }, (_, i) => key.charCodeAt(i).toString(16)).join(" "), text }));
}));
`, branches, &want)
	bad := 0
	for i, branch := range branches {
		var entries []codingagent.SessionEntry
		for _, line := range branch {
			entries = append(entries, sessionentry.DecodeSessionEntry(json.RawMessage(line)))
		}
		got := map[string]string{}
		for key, text := range storeOf(entries) {
			if key == "__proto__" {
				continue // serializeStore's own assignment drops it, as in Pi's serializeStore written out above
			}
			var units []string
			for _, u := range jsstring.ToUTF16(key) {
				units = append(units, fmt.Sprintf("%x", u))
			}
			canonical, err := jsonstringify.Canonicalize(text)
			if err != nil {
				t.Fatalf("storeOf kept invalid JSON %q: %v", text, err)
			}
			got[strings.Join(units, " ")] = string(canonical)
		}
		wantMap := map[string]string{}
		for _, w := range want[i] {
			wantMap[w.Units] = w.Text
		}
		if fmt.Sprint(got) != fmt.Sprint(wantMap) {
			if bad++; bad <= 6 {
				t.Errorf("branch %v\n  PiG %v\n  Pi  %v", branch, got, wantMap)
			}
		}
	}
	if bad > 6 {
		t.Errorf("%d of %d branches differ", bad, len(branches))
	}
}

// A key written with unpaired surrogates reaches the entry as its \u escapes and reads back as the same key: execute.ts appends the writes of
// `store()` as a JS object, which keeps every code unit.
func TestStoreEntryDataKeepsUnpairedSurrogateKeys(t *testing.T) {
	data := StoreEntryData{
		Set:    map[string]json.RawMessage{jsstring.FromUTF16([]uint16{0xd800}): json.RawMessage(`1`), jsstring.FromUTF16([]uint16{0xd801}): json.RawMessage(`2`), "a": json.RawMessage(`3`)},
		Delete: []string{jsstring.FromUTF16([]uint16{0xdc00}), "b"},
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"set":{"a":3,"\ud800":1,"\ud801":2},"delete":["\udc00","b"]}`; string(encoded) != want {
		t.Fatalf("entry data = %s, want %s", encoded, want)
	}
	raw := json.RawMessage(`{"type":"custom","customType":"codemode-store","data":` + string(encoded) + `,"id":"x","parentId":null,"timestamp":"1970-01-01T00:00:00.000Z"}`)
	got := storeOf([]codingagent.SessionEntry{sessionentry.DecodeSessionEntry(raw)})
	if len(got) != 3 || string(got[jsstring.FromUTF16([]uint16{0xd800})]) != "1" || string(got[jsstring.FromUTF16([]uint16{0xd801})]) != "2" {
		t.Fatalf("store = %v", got)
	}
}
