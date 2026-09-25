package latex

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// goldenCase is one line of internal/latex/testdata/golden.jsonl, captured from
// pi's own renderLatex by parity/testdata/latex-pi.mjs. A null inline/display is
// pi's `undefined`, i.e. RenderLatex must return ok=false.
type goldenCase struct {
	Input   string  `json:"input"`
	Inline  *string `json:"inline"`
	Display *string `json:"display"`
}

// TestRenderLatexMatchesUpstream diffs the Go port against pi byte-for-byte over
// the captured corpus, in both inline and display modes. This is the port's
// oracle: a case fails if Go's output or its supported/undefined decision
// differs from pi's.
func TestRenderLatexMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total, mismatches int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var gc goldenCase
		if err := json.Unmarshal(line, &gc); err != nil {
			t.Fatalf("bad golden line %q: %v", line, err)
		}
		total++
		mismatches += checkMode(t, gc.Input, false, gc.Inline)
		mismatches += checkMode(t, gc.Input, true, gc.Display)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan goldens: %v", err)
	}
	if total == 0 {
		t.Fatal("no goldens loaded")
	}
	t.Logf("checked %d inputs (%d mode-assertions); %d mismatches", total, total*2, mismatches)
}

func checkMode(t *testing.T, input string, display bool, want *string) int {
	t.Helper()
	got, ok := RenderLatex(input, RenderLatexOptions{Display: display})
	mode := "inline"
	if display {
		mode = "display"
	}
	if want == nil {
		if ok {
			t.Errorf("[%s] %q: got %q (ok), want undefined", mode, input, got)
			return 1
		}
		return 0
	}
	if !ok {
		t.Errorf("[%s] %q: got undefined, want %q", mode, input, *want)
		return 1
	}
	if got != *want {
		t.Errorf("[%s] %q: got %q, want %q", mode, input, got, *want)
		return 1
	}
	return 0
}
