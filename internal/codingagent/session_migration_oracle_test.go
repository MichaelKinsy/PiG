package codingagent

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

var generatedSessionID = regexp.MustCompile(`"[0-9a-f]{8}"`)

// renumberIDs names the generated 8-hex ids by order of appearance, so two migrations that generate different random ids compare equal when they link and order their members the same way.
func renumberIDs(text string) string {
	seen := map[string]string{}
	return generatedSessionID.ReplaceAllStringFunc(text, func(match string) string {
		if _, ok := seen[match]; !ok {
			seen[match] = `"gen` + string(rune('A'+len(seen))) + `"`
		}
		return seen[match]
	})
}

// TestMigrateSessionEntriesMatchesPi runs migrateSessionEntries of Pi 1.0.4 and Pig over version 1 and 2 session files: the id and parent chain, compaction index conversion and the hookMessage role rename, with member order compared.
func TestMigrateSessionEntriesMatchesPi(t *testing.T) {
	h := func(version string) string {
		return `{"type":"session",` + version + `"id":"s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	}
	user := func(text string) string {
		return `{"type":"message","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"` + text + `"}}`
	}
	files := [][]string{
		{h(""), user("a"), user("b")},
		{h(`"version":1,`), user("a"), `{"type":"compaction","timestamp":"t","summary":"s","firstKeptEntryIndex":1,"tokensBefore":5}`, user("c")},
		{h(""), user("a"), `{"type":"compaction","timestamp":"t","summary":"s","firstKeptEntryIndex":0,"tokensBefore":5}`},
		{h(""), user("a"), `{"type":"compaction","timestamp":"t","summary":"s","firstKeptEntryIndex":9,"tokensBefore":5}`},
		{h(""), user("a"), `{"type":"compaction","timestamp":"t","summary":"s","firstKeptEntryIndex":-1,"tokensBefore":5}`},
		{h(""), user("a"), `{"type":"compaction","timestamp":"t","summary":"s","firstKeptEntryIndex":"1","tokensBefore":5}`},
		{h(""), user("a"), `{"type":"compaction","timestamp":"t","summary":"s","tokensBefore":5,"firstKeptEntryIndex":1.5}`},
		{h(""), user("a"), `{"type":"compaction","timestamp":"t","summary":"s","firstKeptEntryIndex":null,"tokensBefore":5}`},
		{h(`"version":2,`), `{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"t","message":{"role":"hookMessage","customType":"x","content":"c","display":true}}`, `{"type":"message","id":"bbbbbbbb","parentId":"aaaaaaaa","timestamp":"t","message":{"role":"user","content":"u"}}`},
		{h(`"version":3,`), `{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"t","message":{"role":"hookMessage"}}`},
		{h(`"version":2,`), `{"type":"custom","id":"aaaaaaaa","parentId":null,"timestamp":"t","customType":"x"}`, `{"type":"message","id":"bbbbbbbb","parentId":"aaaaaaaa","timestamp":"t"}`},
		{h(`"version":0,`), user("zero")},
		{user("no header"), user("second")},
	}
	var want [][]any
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
emit(input.map((file) => { const entries = file.map((l) => JSON.parse(l)); mod.migrateSessionEntries(entries); return entries.map((e) => JSON.stringify(e)); }));`, files, &want)
	for i, file := range files {
		var entries []FileEntry
		for _, line := range file {
			entries = append(entries, sessionentry.DecodeFileEntry(json.RawMessage(line)))
		}
		if err := MigrateSessionEntries(entries); err != nil {
			t.Errorf("file %d: %v", i, err)
			continue
		}
		for j, raw := range entries {
			var compact bytes.Buffer
			if err := json.Compact(&compact, raw.Raw()); err != nil {
				t.Fatal(err)
			}
			wantEntry, _ := want[i][j].(string)
			if got, w := renumberIDs(compact.String()), renumberIDs(wantEntry); got != w {
				t.Errorf("file %d entry %d:\n  Pig %s\n  Pi  %s", i, j, got, w)
			}
		}
	}
}
