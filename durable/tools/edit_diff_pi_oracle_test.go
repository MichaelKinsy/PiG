package tools

// pi: packages/durable/src/tools/edit-diff.ts (detectLineEnding, normalizeToLF, restoreLineEndings, stripBom, normalizeForFuzzyMatch, fuzzyFindText, applyEditsToNormalizedContent)

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type editDiffOp struct {
	Op      string `json:"op"`
	Text    string `json:"text"`
	Ending  string `json:"ending"`
	Content string `json:"content"`
	OldText string `json:"oldText"`
	Edits   []Edit `json:"edits"`
	Path    string `json:"path"`
}

func editDiffOps() []editDiffOp {
	random := rand.New(rand.NewSource(20261115))
	pieces := []string{"a", "b", "foo", "bar", " ", "  ", "\t", "\n", "\n", "\r\n", "\r", "é", "e\u0301", "ﬁ", "Ａ", "①", "“", "”", "‘", "’", "‚", "‛", "„", "‟", "–", "—", "‐", "‑", "−", "\u00a0", "\u2002", "\u2003", "\u2004", "\u2005", "\u2006", "\u2007", "\u2008", "\u2009", "\u200a", "\u205f", "\u202f", "\u3000", "\u2028", "\ufeff", "―", "‒", "\u200b", "\u0085", "\u1680", "日", "😀", "x=1;", "'", "\"", "-"}
	text := func(limit int) string {
		var b strings.Builder
		for range random.Intn(limit) {
			b.WriteString(pieces[random.Intn(len(pieces))])
		}
		return b.String()
	}
	substring := func(content string) string {
		runes := []rune(content)
		if len(runes) == 0 {
			return ""
		}
		start := random.Intn(len(runes))
		return string(runes[start : start+random.Intn(len(runes)-start)+1])
	}
	var ops []editDiffOp
	for range 5000 {
		content := text(30)
		ops = append(ops,
			editDiffOp{Op: "lineEnding", Text: content},
			editDiffOp{Op: "toLF", Text: content},
			editDiffOp{Op: "bom", Text: content},
			editDiffOp{Op: "fuzzyNormalize", Text: content},
			editDiffOp{Op: "restore", Text: content, Ending: []string{"\r\n", "\n"}[random.Intn(2)]},
		)
		old := substring(content)
		switch random.Intn(3) {
		case 0:
			old = text(6)
		case 1:
			old = normalizeToLF(old)
		}
		ops = append(ops, editDiffOp{Op: "fuzzyFind", Content: normalizeToLF(content), OldText: old})
		var edits []Edit
		for range random.Intn(3) + 1 {
			oldText := substring(content)
			if random.Intn(4) == 0 {
				oldText = text(5)
			}
			edits = append(edits, Edit{OldText: oldText, NewText: text(6)})
		}
		ops = append(ops, editDiffOp{Op: "apply", Content: normalizeToLF(content), Edits: edits, Path: "f.txt"})
	}
	return ops
}

func utf16Length(text string) int { return len(utf16.Encode([]rune(text))) }

// TestEditDiffHelpersMatchPi runs Pi's edit-diff.ts helpers and the Go port over seeded texts with CRLF/CR, BOMs, smart quotes, dashes, special spaces, NFKC-compatible and combining characters, and astral characters.
func TestEditDiffHelpersMatchPi(t *testing.T) {
	ops := editDiffOps()
	payload, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/edit_diff_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		OK  any     `json:"ok"`
		Err *string `json:"err"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	differing, errors := 0, 0
	for index, op := range ops {
		var got any
		var gotErr *string
		switch op.Op {
		case "lineEnding":
			got = detectLineEnding(op.Text)
		case "toLF":
			got = normalizeToLF(op.Text)
		case "bom":
			bom, text := stripBom(op.Text)
			got = map[string]any{"bom": bom, "text": text}
		case "fuzzyNormalize":
			got = normalizeForFuzzyMatch(op.Text)
		case "restore":
			got = restoreLineEndings(op.Text, op.Ending)
		case "fuzzyFind":
			result := fuzzyFindText(&fuzzyContent{text: op.Content}, op.OldText)
			// Pi's offsets count UTF-16 units; Go's count bytes.
			// A miss leaves Go's index at zero where Pi answers -1; no caller reads it (internal result).
			indexUnits, lengthUnits := -1, 0
			if result.found {
				indexUnits = utf16Length(result.contentForReplacement[:result.index])
				lengthUnits = utf16Length(result.contentForReplacement[result.index : result.index+result.matchLength])
			}
			got = map[string]any{"found": result.found, "index": indexUnits, "matchLength": lengthUnits, "usedFuzzyMatch": result.usedFuzzyMatch, "contentForReplacement": result.contentForReplacement}
		default:
			result, err := applyEditsToNormalizedContent(op.Content, op.Edits, op.Path)
			if err != nil {
				gotErr = new(err.Error())
			} else {
				got = map[string]any{"baseContent": result.baseContent, "newContent": result.newContent}
			}
		}
		want := expected[index]
		if want.Err != nil {
			errors++
		}
		encoded, _ := json.Marshal(got)
		var gotJSON any
		_ = json.Unmarshal(encoded, &gotJSON)
		if (gotErr == nil) != (want.Err == nil) || (gotErr != nil && *gotErr != *want.Err) || (gotErr == nil && !reflect.DeepEqual(gotJSON, want.OK)) {
			differing++
			if differing <= 6 {
				opJSON, _ := json.Marshal(op)
				wantJSON, _ := json.Marshal(want)
				gotErrText := ""
				if gotErr != nil {
					gotErrText = *gotErr
				}
				t.Errorf("op %d %s\n got  %s err %q\n want %s", index, opJSON, encoded, gotErrText, wantJSON)
			}
		}
	}
	t.Logf("%d ops, %d Pi errors, %d differing", len(ops), errors, differing)
	if differing > 0 {
		t.Fatalf("%d ops differ from Pi", differing)
	}
}
