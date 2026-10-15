package storage_test

// pi: packages/durable/src/storage/jsonl/storage.ts (recover, confirmRecord, readLines, parseMainMarker, parseSidecarRecord)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func copyDirectory(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(from, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var seqField = regexp.MustCompile(`"seq":(\d+)`)

// damage applies one seeded corruption to a storage directory and describes it. Every kind is a way a crash, a partial write or a stray edit can leave the files.
func damage(t *testing.T, random *rand.Rand, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	name := names[random.Intn(len(names))]
	if random.Intn(2) == 0 {
		name = "main.jsonl"
	}
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	write := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(lines) == 0 {
		write("garbage\n")
		return name + ": empty file got garbage"
	}
	k := random.Intn(len(lines))
	switch kind := random.Intn(18); kind {
	case 0:
		cut := random.Intn(len(raw) + 1)
		write(string(raw[:cut]))
		return fmt.Sprintf("%s: truncated at byte %d of %d", name, cut, len(raw))
	case 1:
		write(strings.Join(slices.Delete(slices.Clone(lines), k, k+1), ""))
		return fmt.Sprintf("%s: dropped line %d of %d", name, k, len(lines))
	case 2:
		write(strings.Join(slices.Insert(slices.Clone(lines), k, lines[k]), ""))
		return fmt.Sprintf("%s: duplicated line %d", name, k)
	case 3:
		if len(lines) < 2 {
			return damage(t, random, dir)
		}
		j := min(k, len(lines)-2)
		swapped := slices.Clone(lines)
		swapped[j], swapped[j+1] = swapped[j+1], swapped[j]
		write(strings.Join(swapped, ""))
		return fmt.Sprintf("%s: swapped lines %d and %d", name, j, j+1)
	case 4:
		at := random.Intn(len(raw))
		if depth, ok := memberNameDepth(raw, at); ok && depth >= recordMemberDepth(name) {
			// Pi casts a stored record as parsed, so an unknown member survives into the answers; Pig decodes into the typed record and drops it. Record members are not probed here; the commit marker, operation and sidecar envelope members are.
			return damage(t, random, dir)
		}
		replacement := "0\"x{}[,:9 \\\xff"[random.Intn(12)]
		mutated := append([]byte(nil), raw...)
		mutated[at] = replacement
		write(string(mutated))
		return fmt.Sprintf("%s: byte %d set to %q", name, at, replacement)
	case 5:
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return name + ": deleted"
	case 6:
		write(string(raw) + `{"format":1,"type":"commit","seq":`)
		return name + ": partial line appended"
	case 7:
		write(string(raw) + "garbage\n")
		return name + ": garbage line appended"
	case 8:
		write(strings.TrimSuffix(string(raw), "\n"))
		return name + ": final newline removed"
	case 9:
		delta := []int{1, -1, 0, 7}[random.Intn(4)]
		changed := false
		lines[k] = seqField.ReplaceAllStringFunc(lines[k], func(match string) string {
			if changed {
				return match
			}
			changed = true
			n, _ := strconv.Atoi(match[len(`"seq":`):])
			return fmt.Sprintf(`"seq":%d`, max(n+delta, 0))
		})
		write(strings.Join(lines, ""))
		return fmt.Sprintf("%s: seq of line %d shifted by %d", name, k, delta)
	case 10:
		write(strings.Join(slices.Insert(slices.Clone(lines), k, "\n"), ""))
		return fmt.Sprintf("%s: blank line before %d", name, k)
	case 11:
		if err := os.WriteFile(filepath.Join(dir, "task-999.jsonl"), []byte(lines[k]), 0o644); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("stray task-999.jsonl holding %s line %d", name, k)
	case 12:
		statusValue := regexp.MustCompile(`"status":"[a-z]+"`)
		if !statusValue.MatchString(lines[k]) {
			return damage(t, random, dir)
		}
		lines[k] = statusValue.ReplaceAllString(lines[k], `"status":"bogus"`)
		write(strings.Join(lines, ""))
		return fmt.Sprintf("%s: status of line %d replaced", name, k)
	case 13:
		lines[k] = strings.Replace(lines[k], `"format":1`, []string{`"format":2`, `"format":"1"`, `"format":0`}[random.Intn(3)], 1)
		write(strings.Join(lines, ""))
		return fmt.Sprintf("%s: format of line %d changed", name, k)
	case 14:
		write(strings.Join(slices.Replace(slices.Clone(lines), k, k+1, strings.Replace(lines[k], `"payload"`, `"payloadx"`, 1)), ""))
		return fmt.Sprintf("%s: line %d payload member renamed", name, k)
	case 15:
		// TextDecoder drops one leading byte order mark from each line it decodes.
		lines[k] = "\ufeff" + lines[k]
		write(strings.Join(lines, ""))
		return fmt.Sprintf("%s: byte order mark before line %d", name, k)
	case 16:
		// Valid JSON that JSON.stringify would not write: recovery parses it, and any line it rewrites comes out canonical.
		lines[k] = strings.Replace(strings.Replace(lines[k], `":`, `" : `, 1), `,"`, `, "`, 1)
		write(strings.Join(lines, ""))
		return fmt.Sprintf("%s: whitespace inside line %d", name, k)
	default:
		return resurrectSidecar(t, random, dir)
	}
}

// resurrectSidecar leaves the directory as a crash between publishing a commit and reclaiming its sidecars would: a terminal task's sidecar or a current-only document's superseded records are back, so recovery must reclaim them.
func resurrectSidecar(t *testing.T, random *rand.Rand, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "main.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Type    string `json:"type"`
		Id      int64  `json:"id"`
		Ordinal int64  `json:"ordinal"`
		Value   struct {
			Id int64 `json:"id"`
		} `json:"value"`
		Record struct {
			Id    int64 `json:"id"`
			Scope struct {
				Kind string `json:"kind"`
			} `json:"scope"`
			History string `json:"history"`
		} `json:"record"`
	}
	type reference struct{ seq, ordinal int64 }
	taskRecords, docRecords := map[int64][]reference{}, map[int64][]reference{}
	terminal, currentOnly := map[int64]bool{}, map[int64]bool{}
	var lastSeq int64
	for line := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var marker struct {
			Seq    int64       `json:"seq"`
			Writes []operation `json:"writes"`
		}
		if err := json.Unmarshal([]byte(line), &marker); err != nil {
			t.Fatal(err)
		}
		lastSeq = marker.Seq
		for _, op := range marker.Writes {
			switch op.Type {
			case "task":
				terminal[op.Value.Id] = true
			case "task.sidecar":
				taskRecords[op.Id] = append(taskRecords[op.Id], reference{marker.Seq, op.Ordinal})
			case "document.create":
				currentOnly[op.Record.Id] = op.Record.Scope.Kind != "conversation" || op.Record.History == "latest"
				docRecords[op.Record.Id] = append(docRecords[op.Record.Id], reference{marker.Seq, op.Ordinal})
			case "document.change":
				docRecords[op.Id] = append(docRecords[op.Id], reference{marker.Seq, op.Ordinal})
			}
		}
	}
	var candidates []string
	for id := range terminal {
		if len(taskRecords[id]) > 0 {
			candidates = append(candidates, fmt.Sprintf("task-%d.jsonl", id))
		}
	}
	for id, only := range currentOnly {
		if only && len(docRecords[id]) > 1 {
			candidates = append(candidates, fmt.Sprintf("doc-%d.jsonl", id))
		}
	}
	if len(candidates) == 0 {
		return damage(t, random, dir)
	}
	slices.Sort(candidates)
	file := candidates[random.Intn(len(candidates))]
	path := filepath.Join(dir, file)
	kept, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var id int64
	var refs []reference
	var payload func(id int64) string
	if after, ok := strings.CutPrefix(file, "task-"); ok {
		id, _ = strconv.ParseInt(strings.TrimSuffix(after, ".jsonl"), 10, 64)
		refs = taskRecords[id]
		payload = func(id int64) string {
			return fmt.Sprintf(`{"type":"task","value":{"id":%d,"conversationId":1,"kind":"k","version":1,"background":false,"abortRequested":false,"state":{"status":"pending"}}}`, id)
		}
	} else {
		id, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(file, "doc-"), ".jsonl"), 10, 64)
		refs = docRecords[id]
		payload = func(id int64) string {
			return fmt.Sprintf(`{"type":"document","id":%d,"content":{"version":1,"kind":"base","value":{}}}`, id)
		}
	}
	// Records already in the file stay; the ones before them are the superseded records reclamation removed.
	first := int64(-1)
	if len(kept) > 0 {
		var record struct{ Seq int64 }
		if err := json.Unmarshal(kept[:bytes.IndexByte(kept, '\n')], &record); err != nil {
			t.Fatal(err)
		}
		first = record.Seq
	}
	var restored strings.Builder
	mode := random.Intn(3)
	if mode != 1 {
		for _, ref := range refs {
			if first >= 0 && ref.seq >= first {
				break
			}
			fmt.Fprintf(&restored, `{"format":1,"type":"record","seq":%d,"ordinal":%d,"payload":%s}`+"\n", ref.seq, ref.ordinal, payload(id))
		}
	}
	retained := string(kept)
	if random.Intn(2) == 0 {
		retained = strings.ReplaceAll(retained, `":`, `": `)
	}
	// A commit that wrote its sidecar record but crashed before its marker leaves an unconfirmed tail.
	var tail string
	if mode != 0 {
		tail = fmt.Sprintf(`{"format":1,"type":"record","seq":%d,"ordinal":0,"payload":%s}`+"\n", lastSeq+1, payload(id))
	}
	if err := os.WriteFile(path, []byte(restored.String()+retained+tail), 0o644); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s resurrected (%d bytes of superseded records restored before %d bytes kept, %d bytes of unconfirmed tail)", file, restored.Len(), len(retained), len(tail))
}

// recordMemberDepth is the nesting depth of a stored record's own members: commit marker, writes array, operation, record in main.jsonl; record line, payload, record in a task sidecar. Document sidecars hold no typed record.
func recordMemberDepth(file string) int {
	switch {
	case file == "main.jsonl":
		return 4
	case strings.HasPrefix(file, "task-"):
		return 3
	}
	return math.MaxInt
}

// memberNameDepth reports whether the byte at index lies inside a JSON member name (including its quotes) of a line the storage wrote, and the number of objects and arrays enclosing that member.
func memberNameDepth(raw []byte, index int) (int, bool) {
	depth, inString, escaped, nameStart := 0, false, false, -1
	for i, c := range raw {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
				if i+1 < len(raw) && raw[i+1] == ':' && nameStart <= index && index <= i {
					return depth, true
				}
			}
		case c == '"':
			inString, nameStart = true, i
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
		case c == '\n':
			depth = 0
		}
		if i > index && !inString {
			return 0, false
		}
	}
	return 0, false
}

// Both storages open the same damaged directories. Whether the open succeeds, the error text, what a reopened storage then answers and the files recovery leaves behind must agree with Pi.
func TestJsonlRecoveryMatchesPiOnDamagedDirectories(t *testing.T) {
	const programCount, damagesPerProgram = 120, 20
	base := t.TempDir()
	var programs []oracleProgram
	var piDirs []string
	for i := range programCount {
		programs = append(programs, oracleProgramFor(int64(20263030+i), true))
		piDirs = append(piDirs, filepath.Join(base, fmt.Sprintf("seed-%d", i)))
	}
	runPiJSONL(t, "jsonl-write", programs, piDirs)

	random := rand.New(rand.NewSource(20263030))
	var cases []oracleProgram
	var aDirs, bDirs, notes []string
	for i, program := range programs {
		for k := range damagesPerProgram {
			a, b := filepath.Join(base, fmt.Sprintf("pi-%d-%d", i, k)), filepath.Join(base, fmt.Sprintf("pig-%d-%d", i, k))
			copyDirectory(t, piDirs[i], a)
			note := damage(t, random, a)
			copyDirectory(t, a, b)
			cases, aDirs, bDirs, notes = append(cases, program), append(aDirs, a), append(bDirs, b), append(notes, fmt.Sprintf("program %d: %s", i, note))
		}
	}
	piAnswers := runPiJSONL(t, "jsonl-read", cases, aDirs)
	pigAnswers := runPigJSONL(t, cases, bDirs, true)

	differing, opened, typeErrors, rejected := 0, 0, 0, map[string]int{}
	for i := range cases {
		label := notes[i]
		if piAnswers[i][0].Index < 0 {
			message := piAnswers[i][0].Err.Message
			rejected[strings.SplitN(message, " ", 2)[0]]++
		} else {
			opened++
		}
		for _, answer := range piAnswers[i] {
			if answer.Err != nil && answer.Err.Name == "TypeError" {
				typeErrors++
			}
		}
		differing += compareOracleResults(t, label, cases[i], pigAnswers[i], piAnswers[i], differing, map[string]int{}, new(int))
		if !sameFileSet(t, aDirs[i], bDirs[i]) {
			if differing++; differing <= 8 {
				t.Errorf("%s: files after recovery differ (%s, %s)", label, aDirs[i], bDirs[i])
			}
		}
	}
	if differing > 8 {
		t.Errorf("%d answers differ", differing)
	}
	t.Logf("%d damaged directories, %d opened, %d reads where Pi throws a TypeError on a dangling record, rejections by leading word %v", len(cases), opened, typeErrors, rejected)
	if opened < len(cases)/10 || len(cases)-opened < len(cases)/10 {
		t.Errorf("probes are one-sided: %d of %d opened", opened, len(cases))
	}
}

// sameFileSet reports whether both directories hold the same files with the same bytes.
func sameFileSet(t *testing.T, a, b string) bool {
	t.Helper()
	left, _ := os.ReadDir(a)
	right, _ := os.ReadDir(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Name() != right[i].Name() {
			return false
		}
		x, _ := os.ReadFile(filepath.Join(a, left[i].Name()))
		y, _ := os.ReadFile(filepath.Join(b, right[i].Name()))
		if !bytes.Equal(x, y) {
			return false
		}
	}
	return true
}
