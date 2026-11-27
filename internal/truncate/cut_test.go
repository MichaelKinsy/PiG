package truncate

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// splitJoinTail is truncateTail as truncate.ts writes it: split into lines, walk back collecting the ones that fit, join. TruncateTail must return the same
// result field by field without building the lines.
func splitJoinTail(content string, maxBytes, maxLines int) Result {
	totalBytes := len(content)
	var lines []string
	if content != "" {
		lines = strings.Split(content, "\n")
		if strings.HasSuffix(content, "\n") {
			lines = lines[:len(lines)-1]
		}
	}
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Result{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes}
	}
	var collected []string
	outputBytes := 0
	truncatedBy := "lines"
	lastLinePartial := false
	for i := len(lines) - 1; i >= 0 && len(collected) < maxLines; i-- {
		line := lines[i]
		extra := 0
		if len(collected) > 0 {
			extra = 1
		}
		if outputBytes+len(line)+extra > maxBytes {
			truncatedBy = "bytes"
			remainingBytes := maxBytes - outputBytes - extra
			allBlank := true
			for _, prev := range collected {
				if jsstring.Trim(prev) != "" {
					allBlank = false
					break
				}
			}
			if len(collected) == 0 || (jsstring.Trim(line) != "" && allBlank && remainingBytes > 0) {
				start := min(len(line)-remainingBytes, len(line))
				for start < len(line) && line[start]&0xC0 == 0x80 {
					start++
				}
				collected = append(collected, line[start:])
				outputBytes += extra + (len(line) - start)
				lastLinePartial = true
			}
			break
		}
		collected = append(collected, line)
		outputBytes += len(line) + extra
	}
	slices.Reverse(collected)
	if len(collected) >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	out := strings.Join(collected, "\n")
	return Result{Content: out, Truncated: true, TruncatedBy: truncatedBy, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: len(collected), OutputBytes: len(out), LastLinePartial: lastLinePartial}
}

// splitJoinHead is truncateHead as truncate.ts writes it.
func splitJoinHead(content string, maxBytes, maxLines int) Result {
	totalBytes := len(content)
	var lines []string
	if content != "" {
		lines = strings.Split(content, "\n")
		if strings.HasSuffix(content, "\n") {
			lines = lines[:len(lines)-1]
		}
	}
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Result{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes}
	}
	if len(lines) > 0 && len(lines[0]) > maxBytes {
		return Result{Truncated: true, TruncatedBy: "bytes", TotalLines: totalLines, TotalBytes: totalBytes, FirstLineExceedsLimit: true}
	}
	var collected []string
	outputBytes := 0
	truncatedBy := "lines"
	for i := 0; i < len(lines) && len(collected) < maxLines; i++ {
		extra := 0
		if i > 0 {
			extra = 1
		}
		if outputBytes+len(lines[i])+extra > maxBytes {
			truncatedBy = "bytes"
			break
		}
		collected = append(collected, lines[i])
		outputBytes += len(lines[i]) + extra
	}
	if len(collected) >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	out := strings.Join(collected, "\n")
	return Result{Content: out, Truncated: true, TruncatedBy: truncatedBy, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: len(collected), OutputBytes: len(out)}
}

func randomLines(rng *rand.Rand) string {
	pieces := []string{"", "a", "bc", "\n", "\n\n", "é", "日本", "😀", "line of text\n", "xxxxxxx", "y\ny\ny\n", strings.Repeat("z", 40), "\r\n"}
	var b strings.Builder
	for range rng.IntN(24) {
		b.WriteString(pieces[rng.IntN(len(pieces))])
	}
	return b.String()
}

// Cutting once must equal splitting and joining on every content and limit, including zero and negative limits, an oversized first or last line, and a
// cut inside a multi-byte character. The []byte form of the tail cut must agree with the string form.
func TestTruncationCutMatchesSplitJoin(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	limits := []int{-3, -1, 0, 1, 2, 3, 4, 5, 7, 8, 13, 20, 41, 100}
	for run := range 4000 {
		content := randomLines(rng)
		maxBytes, maxLines := limits[rng.IntN(len(limits))], limits[rng.IntN(len(limits))]
		if got, want := TruncateTail(content, maxBytes, maxLines), splitJoinTail(content, maxBytes, maxLines); got != want {
			t.Fatalf("run %d TruncateTail(%q, %d, %d) = %+v, split/join %+v", run, content, maxBytes, maxLines, got, want)
		}
		if got, want := TruncateHead(content, maxBytes, maxLines), splitJoinHead(content, maxBytes, maxLines); got != want {
			t.Fatalf("run %d TruncateHead(%q, %d, %d) = %+v, split/join %+v", run, content, maxBytes, maxLines, got, want)
		}
		s, b := CutTail(content, maxBytes, maxLines), CutTail([]byte(content), maxBytes, maxLines)
		if s != b {
			t.Fatalf("run %d CutTail(%q, %d, %d): string %+v, bytes %+v", run, content, maxBytes, maxLines, s, b)
		}
	}
}

// Deterministic tests for Pi 9e9609216 (#12355) blank-line partial-tail edge cases:
// multi-byte whitespace split across the scan window, over-long all-blank line (!isBlankLine conjunct),
// and over-long non-blank line with a trailing whitespace suffix (back-scan).
func TestTruncateTailOverLongBlankAndWhitespaceLineEdgeCases(t *testing.T) {
	cases := []struct {
		name            string
		content         string
		maxBytes        int
		maxLines        int
		wantContent     string
		wantOutputLines int
		wantOutputBytes int
		wantPartial     bool
		wantBy          string
	}{
		{
			name:            "multi-byte whitespace line split by scan window",
			content:         "\u00a0\u00a0\u00a0\n\n",
			maxBytes:        5,
			maxLines:        2000,
			wantContent:     "",
			wantOutputLines: 1,
			wantOutputBytes: 0,
			wantPartial:     false,
			wantBy:          "bytes",
		},
		{
			name:            "over-long ASCII blank line before blank line (!isBlankLine conjunct)",
			content:         "    \n\n",
			maxBytes:        3,
			maxLines:        2000,
			wantContent:     "",
			wantOutputLines: 1,
			wantOutputBytes: 0,
			wantPartial:     false,
			wantBy:          "bytes",
		},
		{
			name:            "over-long non-blank line with whitespace suffix before blank line (back-scan)",
			content:         "x    \n\n",
			maxBytes:        3,
			maxLines:        2000,
			wantContent:     "  \n",
			wantOutputLines: 2,
			wantOutputBytes: 3,
			wantPartial:     true,
			wantBy:          "bytes",
		},
		{
			name:            "over-long U+3000 whitespace line before blank line",
			content:         strings.Repeat("\u3000", 5) + "\n\n",
			maxBytes:        5,
			maxLines:        2000,
			wantContent:     "",
			wantOutputLines: 1,
			wantOutputBytes: 0,
			wantPartial:     false,
			wantBy:          "bytes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateTail(tc.content, tc.maxBytes, tc.maxLines)
			if got.Content != tc.wantContent || got.OutputLines != tc.wantOutputLines || got.OutputBytes != tc.wantOutputBytes || got.LastLinePartial != tc.wantPartial || got.TruncatedBy != tc.wantBy {
				t.Fatalf("TruncateTail(%q, %d, %d) = %+v, want content=%q outputLines=%d outputBytes=%d partial=%v by=%q",
					tc.content, tc.maxBytes, tc.maxLines, got, tc.wantContent, tc.wantOutputLines, tc.wantOutputBytes, tc.wantPartial, tc.wantBy)
			}
			gotBytes := TruncateTailBytes([]byte(tc.content), tc.maxBytes, tc.maxLines)
			if gotBytes != got {
				t.Fatalf("TruncateTailBytes != TruncateTail: %+v vs %+v", gotBytes, got)
			}
		})
	}
}
