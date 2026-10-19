package tools

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// referenceReplacementLineRange is the linear scan getReplacementLineRange replaced.
func referenceReplacementLineRange(lines []lineSpan, r matchedEdit) (int, int, error) {
	replacementStart := r.matchIndex
	replacementEnd := r.matchIndex + r.matchLength
	startLine := -1
	for i, line := range lines {
		if replacementStart >= line.start && replacementStart < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, errReplacementOutsideBase
	}
	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, errReplacementOutsideBase
	}
	return startLine, endLine + 1, nil
}

func TestReplacementLineRangeMatchesLinearScan(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	for range 3000 {
		var text strings.Builder
		for range random.Intn(8) {
			text.WriteString(strings.Repeat("x", random.Intn(4)))
			text.WriteString("\n")
		}
		text.WriteString(strings.Repeat("y", random.Intn(3)))
		lines := getLineSpans(text.String())
		for index := -1; index <= text.Len()+1; index++ {
			for size := 0; size <= text.Len()+2; size++ {
				r := matchedEdit{matchIndex: index, matchLength: size}
				gs, ge, gerr := getReplacementLineRange(lines, r)
				ws, we, werr := referenceReplacementLineRange(lines, r)
				if gs != ws || ge != we || !errors.Is(gerr, werr) {
					t.Fatalf("%q range [%d,+%d): got %d,%d,%v want %d,%d,%v", text.String(), index, size, gs, ge, gerr, ws, we, werr)
				}
			}
		}
	}
}
