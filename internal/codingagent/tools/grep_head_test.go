package tools

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

func randomGrepLine(random *rand.Rand) string {
	pieces := []string{"", "a", "match", "é", "日本", "😀", "\xff", "\n", " ", strings.Repeat("x", 40), strings.Repeat("😀", 30)}
	var b strings.Builder
	for range random.IntN(6) {
		b.WriteString(pieces[random.IntN(len(pieces))])
	}
	return b.String()
}

// headJoin keeps only the head of a joined output; its Result must be TruncateHead of the joined text for every input and limit, including lines with newlines, a first line over the limit, a trailing newline and line limits.
func TestHeadJoinEqualsTruncateHeadOfTheJoinedText(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 5))
	for iteration := range 20000 {
		maxBytes, maxLines := random.IntN(60), 1+random.IntN(8)
		if random.IntN(4) == 0 {
			maxLines = 1<<53 - 1
		}
		options := truncationLimits(maxBytes, maxLines)
		join := newHeadJoin(options)
		var lines []string
		for range random.IntN(10) {
			line := randomGrepLine(random)
			if join.full() && !strings.Contains(line, "\n") && random.IntN(2) == 0 {
				join.AddLength(len(line))
			} else {
				join.Add(line)
			}
			lines = append(lines, line)
		}
		got, want := join.Result(), TruncateHead(strings.Join(lines, "\n"), options)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d, maxBytes %d, maxLines %d, lines %q:\n got %+v\nwant %+v", iteration, maxBytes, maxLines, lines, got, want)
		}
	}
}

func TestTruncatedLineBytesEqualsTruncateLine(t *testing.T) {
	random := rand.New(rand.NewPCG(9, 1))
	for iteration := range 20000 {
		line := randomGrepLine(random) + randomGrepLine(random)
		maxChars := random.IntN(12)
		n, truncated := truncatedLineBytes(line, maxChars)
		want := TruncateLine(line, maxChars)
		if n != len(want.Text) || truncated != want.WasTruncated {
			t.Fatalf("iteration %d: truncatedLineBytes(%q, %d) = %d, %v; TruncateLine = %q (%d), %v", iteration, line, maxChars, n, truncated, want.Text, len(want.Text), want.WasTruncated)
		}
	}
	for _, n := range []int{0, 1, 9, 10, 99, 100, 123456} {
		if got, want := decimalBytes(n), len(jsNumber(float64(n))); got != want {
			t.Fatalf("decimalBytes(%d) = %d, jsNumber writes %d", n, got, want)
		}
	}
}
