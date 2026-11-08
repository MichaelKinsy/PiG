package tools

// pi: packages/coding-agent/test/edit-diff-window.test.ts, test/edit-tool-diff-before-write.test.ts and the line-ending cases of
// test/tools.test.ts "edit tool CRLF handling" (d448c51e2, #11751).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// applyUnifiedPatch applies a patch from GenerateUnifiedPatch to content, failing when a hunk's context or removed lines do not match.
func applyUnifiedPatch(content, patch string) (string, error) {
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "--- ") || !strings.HasPrefix(lines[1], "+++ ") {
		return "", errors.New("missing file headers")
	}
	tokens := strings.SplitAfter(content, "\n")
	if tokens[len(tokens)-1] == "" {
		tokens = tokens[:len(tokens)-1]
	}
	var out []string
	position := 0
	for i := 2; i < len(lines); {
		var oldStart, oldCount, newStart, newCount int
		if _, err := fmt.Sscanf(lines[i], "@@ -%d,%d +%d,%d @@", &oldStart, &oldCount, &newStart, &newCount); err != nil {
			return "", fmt.Errorf("bad hunk header %q", lines[i])
		}
		i++
		if oldCount > 0 {
			oldStart--
		}
		if oldStart < position || oldStart > len(tokens) {
			return "", fmt.Errorf("hunk at %d overlaps or is outside the content", oldStart)
		}
		out = append(out, tokens[position:oldStart]...)
		position = oldStart
		for ; i < len(lines) && !strings.HasPrefix(lines[i], "@@ "); i++ {
			line := lines[i]
			if line == `\ No newline at end of file` {
				continue
			}
			noNewline := i+1 < len(lines) && lines[i+1] == `\ No newline at end of file`
			token := line[1:] + "\n"
			if noNewline {
				token = line[1:]
			}
			switch line[0] {
			case ' ', '-':
				if position >= len(tokens) || tokens[position] != token {
					return "", fmt.Errorf("hunk line %q does not match the content", line)
				}
				position++
				if line[0] == ' ' {
					out = append(out, token)
				}
			case '+':
				out = append(out, token)
			default:
				return "", fmt.Errorf("bad hunk line %q", line)
			}
		}
	}
	out = append(out, tokens[position:]...)
	return strings.Join(out, ""), nil
}

func applyTestReplacements(content string, replacements []matchedEdit) string {
	var b strings.Builder
	position := 0
	for _, r := range replacements {
		b.WriteString(content[position:r.matchIndex])
		b.WriteString(r.newText)
		position = r.matchIndex + r.matchLength
	}
	b.WriteString(content[position:])
	return b.String()
}

// randomEditCase is random content and sorted, disjoint, non-empty replacements; distinct makes every line unique.
func randomEditCase(random *rand.Rand, distinct bool) (string, []matchedEdit, string) {
	lines := make([]string, random.IntN(400))
	for i := range lines {
		if distinct {
			lines[i] = "line " + strconv.Itoa(i)
		} else {
			lines[i] = []string{"a", "b", "", "}"}[random.IntN(4)]
		}
	}
	content := strings.Join(lines, "\n")
	if random.IntN(2) == 0 {
		content += "\n"
	}
	var replacements []matchedEdit
	position := 0
	count := 1 + random.IntN(4)
	for i := 0; i < count && position < len(content); i++ {
		matchIndex := position + random.IntN(min(len(content)-position, 300))
		if matchIndex >= len(content) {
			break
		}
		matchLength := 1 + random.IntN(min(len(content)-matchIndex, 120))
		newLines := make([]string, random.IntN(4))
		for j := range newLines {
			newLines[j] = fmt.Sprintf("new %d %d", i, j)
		}
		newText := strings.Join(newLines, "\n")
		if random.IntN(3) == 0 {
			newText = ""
		} else if random.IntN(2) == 0 {
			newText += "\n"
		}
		replacements = append(replacements, matchedEdit{matchIndex: matchIndex, matchLength: matchLength, newText: newText})
		position = matchIndex + matchLength
	}
	return content, replacements, applyTestReplacements(content, replacements)
}

func TestGenerateEditDiffMatchesTheWholeFileDiffAndPatchWhenLinesAreDistinct(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 1))
	for run := range 2000 {
		content, replacements, newContent := randomEditCase(random, true)
		if content == newContent {
			continue
		}
		windowed := generateEditDiff("f.txt", content, newContent, replacements, 4)
		whole := GenerateDiffString(content, newContent)
		wholeFirst := 0
		if whole.FirstChangedLine != nil {
			wholeFirst = *whole.FirstChangedLine
		}
		want := editDiff{diff: whole.Diff, patch: GenerateUnifiedPatch("f.txt", content, newContent), firstChangedLine: wholeFirst}
		if windowed != want {
			t.Fatalf("run %d:\n got  %+v\n want %+v", run, windowed, want)
		}
	}
}

func TestGenerateEditDiffProducesAPatchThatAppliesWhenLinesRepeat(t *testing.T) {
	random := rand.New(rand.NewPCG(2, 2))
	for run := range 2000 {
		content, replacements, newContent := randomEditCase(random, false)
		if content == newContent {
			continue
		}
		patch := generateEditDiff("f.txt", content, newContent, replacements, 4).patch
		got, err := applyUnifiedPatch(content, patch)
		if err != nil || got != newContent {
			t.Fatalf("run %d: patch gives %q, %v; want %q\n%s", run, got, err, newContent, patch)
		}
	}
}

func TestGenerateEditDiffHandlesAReplacementOfManyLines(t *testing.T) {
	newText := strings.Repeat("x\n", 150000)
	replacements := []matchedEdit{{matchIndex: 0, matchLength: 2, newText: newText}}
	patch := generateEditDiff("f.txt", "a\nb\n", newText+"b\n", replacements, 4).patch
	if got, err := applyUnifiedPatch("a\nb\n", patch); err != nil || got != newText+"b\n" {
		t.Fatalf("patch applied: %v", err)
	}
}

func TestGenerateEditDiffOfASmallChangeInA20MBFileAllocatesLittle(t *testing.T) {
	filler := strings.Repeat("x", 99) + "\n"
	half := strings.Repeat(filler, 100000)
	base := half + "TARGET = 1\n" + half
	changed := half + "TARGET = 2\n" + half
	replacements := []matchedEdit{{matchIndex: len(half), matchLength: 10, newText: "TARGET = 2"}}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result := generateEditDiff("big.txt", base, changed, replacements, 4)
	runtime.ReadMemStats(&after)
	var changes []string
	for line := range strings.SplitSeq(result.diff, "\n") {
		if line != "" && (line[0] == '+' || line[0] == '-') {
			changes = append(changes, line)
		}
	}
	if got := strings.Join(changes, "\n"); got != "-100001 TARGET = 1\n+100001 TARGET = 2" {
		t.Fatalf("changed lines = %q", got)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
		t.Fatalf("diffing a small change in a 20 MB file allocated %d MB", allocated>>20)
	}
}

func TestEditToolComputesTheDiffBeforeWritingSoADiffFailureLeavesTheFileUnchanged(t *testing.T) {
	original := computeEditDiff
	computeEditDiff = func(string, string, string, []matchedEdit, int) editDiff { panic("diff failed") }
	t.Cleanup(func() { computeEditDiff = original })
	wrote := false
	tool := &EditTool{CWD: "/virtual", Queue: NewFileMutationQueue(), Operations: &EditOperations{
		Access:    func(string) error { return nil },
		ReadFile:  func(string) ([]byte, error) { return []byte("a\nb\n"), nil },
		WriteFile: func(string, string) error { wrote = true; return nil },
	}}
	args, _ := json.Marshal(map[string]any{"path": "f.txt", "edits": []editEntry{{OldText: "b", NewText: "B"}}})
	func() {
		defer func() {
			if r := recover(); r == nil || fmt.Sprint(r) != "diff failed" {
				t.Fatalf("recovered %v, want the diff failure", r)
			}
		}()
		_, _ = tool.Execute(context.Background(), "", args, nil)
	}()
	if wrote {
		t.Fatal("the file was written before the diff failed")
	}
}

// editBytes edits a file holding content and returns the bytes written and the patch of the result.
func editBytes(t *testing.T, content string, edits ...editEntry) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	result := runFileTool(t, tool, t.Context(), map[string]any{"path": path, "edits": edits})
	if result.IsError {
		t.Fatalf("edit failed: %s", result.Text())
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	details := result.Details.(*EditToolDetails)
	return string(written), details.Patch
}

func TestEditToolWritesTheLineEndingOfTheLineAnEditStartsIn(t *testing.T) {
	tests := []struct {
		name, content, want string
		edits               []editEntry
	}{
		{"CRLF for inserted lines in CRLF files", "first\r\nsecond\r\nthird", "first\r\n2a\r\n2b\r\nthird!", []editEntry{{"second\nthird", "2a\r\n2b\nthird!"}}},
		{"LF for inserted lines in LF files", "first\nsecond\nthird", "first\n2a\n2b\nthird!", []editEntry{{"second\nthird", "2a\r\n2b\nthird!"}}},
		{"CRLF lines kept when the first line ends with LF (#8544)", "a\nb\r\nX = 1\r\n", "a\nb\r\nX = 2\r\n", []editEntry{{"X = 1", "X = 2"}}},
		{"LF lines kept when the first line ends with CRLF (#8544)", "a\r\nb\nc", "a\r\nb\nC", []editEntry{{"c", "C"}}},
		{"a lone CR kept in an untouched line (#8544)", "a\rb\nX = 1\n", "a\rb\nX = 2\n", []editEntry{{"X = 1", "X = 2"}}},
		{"inserted lines take the ending of the line they replace in multi-edit mode (#8544)", "lf1\nlf2\ncr1\r\ncr2\r\n", "A\nB\nlf2\nC\r\nD\r\ncr2\r\n", []editEntry{{"lf1\n", "A\nB\n"}, {"cr1\n", "C\nD\n"}}},
		{"untouched endings kept when fuzzy matching widens the edit to whole lines (#8544)", "a\r\nfoo   \r\nb\n", "a\r\nFOO\r\nb\n", []editEntry{{"foo\n", "FOO\n"}}},
		{"BOM and CRLF", "\uFEFFfirst\r\nsecond\r\nthird\r\nfourth\r\n", "\uFEFFfirst\r\nSECOND\r\nthird\r\nFOURTH\r\n", []editEntry{{"second", "SECOND"}, {"fourth", "FOURTH"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := editBytes(t, tc.content, tc.edits...); got != tc.want {
				t.Fatalf("written %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEditToolDoesNotMergeAKeptLoneCRWithALineFeedAfterIt(t *testing.T) {
	cases := []struct {
		edit            editEntry
		written, diffed string
	}{
		// Inserted text starting with LF after a kept lone CR.
		{editEntry{"b", "\nB"}, "a\r\n\nB\nc", "a\n\nB\nc"},
		// Deleted text between a kept lone CR and a kept LF.
		{editEntry{"b", ""}, "a\r\n\nc", "a\n\nc"},
	}
	for i, tc := range cases {
		written, patch := editBytes(t, "a\rb\nc", tc.edit)
		if written != tc.written {
			t.Fatalf("case %d: written %q, want %q", i, written, tc.written)
		}
		if got, err := applyUnifiedPatch("a\nb\nc", patch); err != nil || got != tc.diffed {
			t.Fatalf("case %d: patch gives %q, %v; want %q", i, got, err, tc.diffed)
		}
	}
}

func TestApplyReplacementsToOriginalDoesNotMergeAnInsertedCRWithAKeptLineFeed(t *testing.T) {
	// Only files without LF give inserted text CR line endings, so check the seam directly.
	written := applyReplacementsToOriginal("a\rb\nc", []matchedEdit{{matchIndex: 2, matchLength: 1, newText: "\r"}})
	if got := normalizeToLF(written); got != "a\n\n\nc" {
		t.Fatalf("normalized = %q", got)
	}
}

func TestApplyReplacementsToOriginalNormalizesToTheDiffedContentForRandomMixedLineEndings(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 1))
	endings := []string{"\n", "\r\n", "\r", ""}
	for run := range 3000 {
		var original strings.Builder
		for line := random.IntN(12) + 1; line > 0; line-- {
			original.WriteString("w" + strconv.Itoa(random.IntN(100)))
			if random.IntN(5) == 0 {
				original.WriteString("  ")
			}
			original.WriteString(endings[random.IntN(4)])
		}
		normalized := normalizeToLF(original.String())
		if normalized == "" {
			continue
		}
		start := random.IntN(len(normalized))
		oldText := normalized[start:min(start+1+random.IntN(12), len(normalized))]
		newText := []string{"", "\n", "X\n", "\nY", "\r", "Z"}[random.IntN(6)]
		applied, err := applyEditsToNormalizedContent(normalized, []editEntry{{OldText: oldText, NewText: newText}}, "f.txt")
		if err != nil {
			continue // duplicate, not found or no change
		}
		if got := normalizeToLF(applyReplacementsToOriginal(original.String(), applied.replacements)); got != applied.newContent {
			t.Fatalf("run %d: original %q, edit %q -> %q: written normalizes to %q, diffed %q", run, original.String(), oldText, newText, got, applied.newContent)
		}
	}
}
