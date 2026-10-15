package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/tools/edit.ts

// The edit tool against pinned Pi over seeded files (LF, CRLF and mixed endings, a BOM, smart quotes, dashes, trailing whitespace, non-breaking
// spaces, repeated lines) and edits (exact, fuzzy, absent, empty, repeated, overlapping, several): the result text, its details (diff, patch,
// first changed line), the error when the edit is refused, and the file left behind.
func TestEditToolMatchesPiOverSeededFiles(t *testing.T) {
	rng := rand.New(rand.NewSource(163))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	lines := []string{"hello world", "  indented line", "const x = 1;", "“smart” quotes", "dash — here and – there", "trailing   ", "tab\there", "", "é ü ñ", "😀 emoji", "same", "same", "non\u00a0breaking", "‘single’ quotes", "func main() {", "}", "x"}
	type probe struct {
		Content string         `json:"content"`
		Args    map[string]any `json:"args"`
	}
	var probes []probe
	for range 600 {
		var picked []string
		for _, index := range rng.Perm(len(lines))[:1+rng.Intn(8)] {
			picked = append(picked, lines[index])
		}
		ending := pick("\n", "\n", "\r\n")
		text := strings.Join(picked, ending)
		if rng.Intn(3) > 0 {
			text += ending
		}
		if rng.Intn(6) == 0 {
			text = strings.Replace(text, ending, pick("\n", "\r\n"), 1)
		}
		if rng.Intn(8) == 0 {
			text = "\ufeff" + text
		}
		var edits []any
		for range 1 + min(rng.Intn(4), 2)/2*rng.Intn(3) {
			line := picked[rng.Intn(len(picked))]
			old := line
			switch rng.Intn(18) {
			case 0:
				old = strings.Replace(strings.ReplaceAll(strings.ReplaceAll(line, "“", `"`), "”", `"`), "—", "-", 1)
			case 1:
				old = strings.TrimRight(line, " ")
			case 2:
				old = "absent text"
			case 3:
				old = ""
			case 4:
				old = line + ending + lines[rng.Intn(len(lines))]
			case 5:
				if len(picked) > 1 {
					old = strings.Join(picked[:1+rng.Intn(len(picked))], "\n")
				}
			case 6:
				if len(line) > 2 {
					old = line[1 : len(line)-1]
				}
			}
			edits = append(edits, map[string]any{"oldText": old, "newText": pick("replaced", "", "a\nb", "x\r\ny", line+" more", "“q”")})
		}
		args := map[string]any{"path": "f.txt", "edits": edits}
		switch rng.Intn(14) {
		case 0:
			args = map[string]any{"path": "f.txt", "oldText": edits[0].(map[string]any)["oldText"], "newText": "legacy"}
		case 1:
			args = map[string]any{"path": "missing.txt", "edits": edits}
		case 2:
			args = map[string]any{"path": "f.txt", "edits": []any{}}
		}
		probes = append(probes, probe{Content: base64.StdEncoding.EncodeToString([]byte(text)), Args: args})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/edit_tool_seeded.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Text    string          `json:"text"`
		Details json.RawMessage `json:"details"`
		Thrown  string          `json:"thrown"`
		File    string          `json:"file"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures, edited := 0, 0
	for i, p := range probes {
		dir := t.TempDir()
		content, _ := base64.StdEncoding.DecodeString(p.Content)
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), content, 0o644); err != nil {
			t.Fatal(err)
		}
		tool := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
		args, _ := json.Marshal(p.Args)
		result, execErr := tool.Execute(context.Background(), "", args, nil)
		var got struct {
			Text, Details, Thrown, File string
		}
		switch {
		case execErr != nil:
			got.Thrown = execErr.Error()
		case result.IsError:
			got.Thrown = result.Text()
		default:
			got.Text = result.Text()
			details, _ := json.Marshal(result.Details)
			got.Details = string(details)
		}
		file, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
		got.File = base64.StdEncoding.EncodeToString(file)
		expected := want[i]
		if expected.Thrown == "" {
			edited++
		}
		var gotDetails, wantDetails any
		_ = json.Unmarshal([]byte(got.Details), &gotDetails)
		_ = json.Unmarshal(expected.Details, &wantDetails)
		gotJSON, _ := json.Marshal([]any{got.Text, gotDetails, got.Thrown, got.File})
		wantJSON, _ := json.Marshal([]any{expected.Text, wantDetails, expected.Thrown, expected.File})
		if !bytes.Equal(gotJSON, wantJSON) {
			if failures++; failures <= 4 {
				t.Errorf("probe %d: content %q args %s\n PiG %.600s\n Pi  %.600s", i, content, args, gotJSON, wantJSON)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if edited*10 < len(probes)*3 {
		t.Fatalf("Pi applied the edit in only %d of %d probes", edited, len(probes))
	}
}
