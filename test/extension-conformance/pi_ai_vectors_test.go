package extensionconformance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The expected answers of these rows are the vectors under test/parity/sdk-vectors/<module>/<export>.json: what Pi's own function returned at the pin for each case
// (README.md there). The SDK fixtures read one file that lists every call, so piVectorsFile writes that file from the vectors into the test's temporary directory and returns
// its path; nothing in it is written by hand.

type piVectorCase struct {
	Name   string            `json:"name"`
	Args   []json.RawMessage `json:"args"`
	Result json.RawMessage   `json:"result"`
}

func piVectorCases(t *testing.T, module, export string) []piVectorCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "parity", "sdk-vectors", module, export+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []piVectorCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("%s/%s: %v", module, export, err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s/%s has no cases", module, export)
	}
	return file.Cases
}

func piUndefined(raw json.RawMessage) bool {
	var compact bytes.Buffer
	return len(raw) == 0 || (json.Compact(&compact, raw) == nil && compact.String() == `{"$":"undefined"}`)
}

// piNullIfUndefined is an argument as the fixtures read it: undefined is null.
func piNullIfUndefined(raw json.RawMessage) json.RawMessage {
	if piUndefined(raw) {
		return json.RawMessage("null")
	}
	return raw
}

func piVectorsFile(t *testing.T) string {
	t.Helper()
	var exports []string
	files, err := filepath.Glob(filepath.Join("..", "parity", "sdk-vectors", "pi-ai", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no pi-ai vectors: %v", err)
	}
	for _, file := range files {
		exports = append(exports, strings.TrimSuffix(filepath.Base(file), ".json"))
	}
	sort.Strings(exports)

	var cases []map[string]any
	for _, export := range exports {
		if export == "envApiKeyAuth" || export == "createAssistantMessageEventStream" {
			continue
		}
		for _, c := range piVectorCases(t, "pi-ai", export) {
			args := make([]json.RawMessage, len(c.Args))
			for i, arg := range c.Args {
				args[i] = piNullIfUndefined(arg)
			}
			entry := map[string]any{"fn": export, "args": args}
			if !piUndefined(c.Result) {
				entry["result"] = c.Result
			}
			cases = append(cases, entry)
		}
	}

	var timing []map[string]any
	for _, c := range piVectorCases(t, "pi-ai", "createAssistantMessageEventStream") {
		var scenario map[string]json.RawMessage
		var answer struct {
			Present    bool `json:"present"`
			Kept       bool `json:"kept"`
			LaterTimed bool `json:"laterTimed"`
		}
		_ = json.Unmarshal(c.Args[0], &scenario)
		_ = json.Unmarshal(c.Result, &answer)
		value := json.RawMessage("null")
		if answer.Kept {
			value = scenario["preset"]
		}
		timing = append(timing, map[string]any{"kind": scenario["kind"], "timestamp": scenario["timestamp"], "preset": scenario["preset"], "present": answer.Present, "value": value, "laterTimed": answer.LaterTimed})
	}

	var authCases []map[string]any
	var login any
	for _, c := range piVectorCases(t, "pi-ai", "envApiKeyAuth") {
		var answer struct {
			Result  json.RawMessage `json:"result"`
			Seen    []string        `json:"seen"`
			Prompts json.RawMessage `json:"prompts"`
		}
		_ = json.Unmarshal(c.Result, &answer)
		if string(c.Args[2]) == `"$login"` {
			login = map[string]any{"prompts": answer.Prompts, "result": answer.Result}
			continue
		}
		entry := map[string]any{"name": c.Args[0], "envVars": c.Args[1], "env": c.Args[3], "seen": answer.Seen}
		if !piUndefined(c.Args[2]) {
			entry["credential"] = c.Args[2]
		}
		if string(answer.Result) != "null" {
			entry["result"] = answer.Result
		}
		authCases = append(authCases, entry)
	}

	// A diff case and its patch case share their number ("diff 007", "patch 007"); the case that only has a patch is flagged.
	patches := map[string]piVectorCase{}
	for _, c := range piVectorCases(t, "pi-coding-agent", "generateUnifiedPatch") {
		patches[strings.TrimPrefix(c.Name, "patch ")] = c
	}
	var diffs []map[string]any
	seenPatches := map[string]bool{}
	appendPatch := func(number string, withDiff *piVectorCase) {
		patch := patches[number]
		var patchText string
		_ = json.Unmarshal(patch.Result, &patchText)
		entry := map[string]any{"path": patch.Args[0], "oldContent": patch.Args[1], "newContent": patch.Args[2], "patch": patchText}
		if len(patch.Args) > 3 {
			entry["contextLines"] = patch.Args[3]
		}
		if withDiff == nil {
			entry["patchOnly"] = true
		} else {
			var result struct {
				Diff             string          `json:"diff"`
				FirstChangedLine json.RawMessage `json:"firstChangedLine"`
			}
			_ = json.Unmarshal(withDiff.Result, &result)
			entry["diff"], entry["firstChangedLine"] = result.Diff, piNullIfUndefined(result.FirstChangedLine)
		}
		diffs = append(diffs, entry)
		seenPatches[number] = true
	}
	for _, c := range piVectorCases(t, "pi-coding-agent", "generateDiffString") {
		number := strings.TrimPrefix(c.Name, "diff ")
		appendPatch(number, &c)
	}
	var patchOnly []string
	for number := range patches {
		if !seenPatches[number] {
			patchOnly = append(patchOnly, number)
		}
	}
	sort.Strings(patchOnly)
	for _, number := range patchOnly {
		appendPatch(number, nil)
	}

	var entries []map[string]any
	for _, c := range piVectorCases(t, "pi-coding-agent", "sessionEntryToContextMessages") {
		entries = append(entries, map[string]any{"entry": c.Args[0], "messages": c.Result})
	}
	var images []map[string]any
	for _, export := range []string{"convertToPng", "resizeImage"} {
		op := map[string]string{"convertToPng": "convert", "resizeImage": "resize"}[export]
		for _, c := range piVectorCases(t, "pi-coding-agent", export) {
			var answer struct {
				Image json.RawMessage `json:"image"`
			}
			_ = json.Unmarshal(c.Result, &answer)
			entry := map[string]any{"op": op, "name": c.Name, "data": c.Args[0], "mimeType": c.Args[1], "result": answer.Image}
			if len(c.Args) > 2 {
				entry["options"] = c.Args[2]
			}
			images = append(images, entry)
		}
	}

	encoded, err := json.Marshal(map[string]any{"cases": cases, "timing": timing, "auth": map[string]any{"cases": authCases, "login": login}, "diffs": diffs, "entries": entries, "images": images})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pi-vectors.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
