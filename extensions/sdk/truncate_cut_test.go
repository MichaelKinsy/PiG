package sdk

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// splitJoinHead and splitJoinTail are TruncateHead and TruncateTail as truncate.ts writes them (split into lines, collect, join), the form this package
// used before it cut once.
func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func splitJoinHead(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	if len(lines[0]) > maxBytes {
		return TruncationResult{Truncated: true, TruncatedBy: "bytes", TotalLines: totalLines, TotalBytes: totalBytes, FirstLineExceedsLimit: true, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	count, outputBytes := 0, 0
	truncatedBy := "lines"
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		count++
		outputBytes += lineBytes
	}
	if count >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	output := strings.Join(lines[:count], "\n")
	return TruncationResult{Content: output, Truncated: true, TruncatedBy: truncatedBy, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: count, OutputBytes: len(output), MaxLines: maxLines, MaxBytes: maxBytes}
}

func splitJoinTail(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	var kept []string
	outputBytes := 0
	truncatedBy := "lines"
	lastLinePartial := false
	for i := len(lines) - 1; i >= 0 && len(kept) < maxLines; i-- {
		lineBytes := len(lines[i])
		if len(kept) > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(kept) == 0 {
				line := lines[i]
				start := min(len(line)-maxBytes, len(line))
				for start < len(line) && !utf8.RuneStart(line[start]) {
					start++
				}
				kept = append(kept, line[start:])
				outputBytes = len(line) - start
				lastLinePartial = true
			}
			break
		}
		kept = append(kept, lines[i])
		outputBytes += lineBytes
	}
	if len(kept) >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	output := strings.Join(kept, "\n")
	return TruncationResult{Content: output, Truncated: true, TruncatedBy: truncatedBy, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: len(kept), OutputBytes: len(output), LastLinePartial: lastLinePartial, MaxLines: maxLines, MaxBytes: maxBytes}
}

// Cutting once must equal splitting and joining on every content and limit, including zero and negative limits, an oversized first or last line and a
// cut inside a multi-byte character.
func TestTruncationCutMatchesSplitJoin(t *testing.T) {
	pieces := []string{"", "a", "bc", "\n", "\n\n", "é", "日本", "😀", "line of text\n", "xxxxxxx", "y\ny\ny\n", strings.Repeat("z", 40), "\r\n"}
	limits := []int{-3, -1, 0, 1, 2, 3, 4, 5, 7, 8, 13, 20, 41, 100}
	rng := rand.New(rand.NewPCG(7, 11))
	for run := range 4000 {
		var b strings.Builder
		for range rng.IntN(24) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		content := b.String()
		maxBytes, maxLines := limits[rng.IntN(len(limits))], limits[rng.IntN(len(limits))]
		options := TruncationOptions{MaxBytes: &maxBytes, MaxLines: &maxLines}
		if got, want := TruncateTail(content, options), splitJoinTail(content, options); got != want {
			t.Fatalf("run %d TruncateTail(%q, bytes %d, lines %d) = %+v, split/join %+v", run, content, maxBytes, maxLines, got, want)
		}
		if content == "" {
			continue // the split/join head indexes the first of no lines past a negative limit, where Pi throws
		}
		if got, want := TruncateHead(content, options), splitJoinHead(content, options); got != want {
			t.Fatalf("run %d TruncateHead(%q, bytes %d, lines %d) = %+v, split/join %+v", run, content, maxBytes, maxLines, got, want)
		}
	}
}
