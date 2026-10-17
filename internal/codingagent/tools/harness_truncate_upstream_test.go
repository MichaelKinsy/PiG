package tools

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func TestHarnessTruncate(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:65
	t.Run("counts UTF-8 bytes without Node Buffer", func(t *testing.T) {
		r := TruncateHead("aé🙂\nb", truncationLimits(100, 10))
		if r.Truncated || r.TotalBytes != 9 || r.OutputBytes != 9 {
			t.Fatalf("%+v", r)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:75
	t.Run("does not count a trailing newline as an extra line", func(t *testing.T) {
		for _, r := range []TruncationResult{TruncateHead("line\nline\nline\n", truncationLimits(100, 3)), TruncateTail("line\nline\nline\n", truncationLimits(100, 3))} {
			if r.Truncated || r.TotalLines != 3 || r.OutputLines != 3 {
				t.Fatalf("%+v", r)
			}
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:84
	t.Run("truncates head on UTF-8 byte limits without partial lines", func(t *testing.T) {
		r := TruncateHead("éé\nabc", truncationLimits(4, 10))
		if r.Content != "éé" || !r.Truncated || r.TruncatedBy != "bytes" || r.OutputBytes != 4 || r.FirstLineExceedsLimit {
			t.Fatalf("%+v", r)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:95
	t.Run("reports head truncation when the first line exceeds the byte limit", func(t *testing.T) {
		r := TruncateHead("éé\nabc", truncationLimits(3, 10))
		if r.Content != "" || !r.Truncated || r.TruncatedBy != "bytes" || !r.FirstLineExceedsLimit {
			t.Fatalf("%+v", r)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:104
	t.Run("truncates tail on UTF-8 boundaries when only a partial last line fits", func(t *testing.T) {
		r := TruncateTail("aé🙂b", truncationLimits(5, 10))
		if r.Content != "🙂b" || !r.Truncated || r.TruncatedBy != "bytes" || !r.LastLinePartial || r.OutputBytes != 5 {
			t.Fatalf("%+v", r)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:114
	t.Run("truncates an oversized single line with a trailing newline", func(t *testing.T) {
		r := TruncateTail(strings.Repeat("X", 300_000)+"\n", truncationLimits(1024, 100))
		if r.Content != strings.Repeat("X", 1024) || r.OutputBytes != 1024 || r.OutputLines != 1 || !r.LastLinePartial || r.TruncatedBy != "bytes" {
			t.Fatalf("%+v", r)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:125
	t.Run("drops an oversized trailing character when it cannot fit in tail byte limit", func(t *testing.T) {
		r := TruncateTail("abc🙂", truncationLimits(3, 10))
		if r.Content != "" || !r.Truncated || r.TruncatedBy != "bytes" || !r.LastLinePartial || r.OutputBytes != 0 {
			t.Fatalf("%+v", r)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:135
	t.Run("matches Buffer tail truncation semantics for surrogate edge cases", func(t *testing.T) {
		inputs := [][]uint16{{'a', 0xd83d}, {0xde42, 'b'}, {'a', 0xde42, 'b'}, {0xd83d, 0xd83d, 0xde42}, {0xd83d, 0xde42, 0xde42}, utf16.Encode([]rune("👩‍💻"))}
		for _, units := range inputs {
			input := string(utf16.Decode(units))
			for limit := range len(input) + 5 {
				assertHarnessBufferTail(t, input, limit)
			}
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/truncate.test.ts:140
	t.Run("matches Buffer tail truncation semantics across deterministic fuzz cases", func(t *testing.T) {
		alphabet := [][]uint16{{'a'}, {0x7f}, {0x80}, {'é'}, {0x7ff}, {0x800}, {'中'}, {0xd7ff}, {0xd800}, {0xd83d}, {0xdc00}, {0xde42}, {0xd83d, 0xde42}, {0xe000}, {0xffff}}
		check := func(units []uint16) {
			input := string(utf16.Decode(units))
			n := len(input)
			limits := []int{0, 1, 2, 3, 4, 5, 8, n/2 - 1, n / 2, n/2 + 1, n - 8, n - 5, n - 4, n - 3, n - 2, n - 1, n, n + 1, n + 4}
			slices.Sort(limits)
			for _, limit := range slices.Compact(limits) {
				if limit >= 0 {
					assertHarnessBufferTail(t, input, limit)
				}
			}
		}
		var exhaustive func([]uint16, int)
		exhaustive = func(prefix []uint16, depth int) {
			check(prefix)
			if depth == 0 {
				return
			}
			for _, units := range alphabet {
				exhaustive(append(slices.Clone(prefix), units...), depth-1)
			}
		}
		exhaustive(nil, 3)
		seed := uint32(0x12345678)
		random := func() float64 { seed = seed*1664525 + 1013904223; return float64(seed) / 0x100000000 }
		for range 1000 {
			var units []uint16
			n := int(random() * 80)
			for range n {
				units = append(units, alphabet[int(random()*float64(len(alphabet)))]...)
			}
			check(units)
		}
	})
}

// Go text is UTF-8: utf16.Decode above performs the same unpaired-surrogate
// replacement as Buffer.from(input, "utf8") at the language boundary. The
// oracle walks complete runes backwards, independently of the byte-mask cut.
func assertHarnessBufferTail(t *testing.T, input string, limit int) {
	t.Helper()
	start := len(input)
	for start > 0 {
		_, size := utf8.DecodeLastRuneInString(input[:start])
		if len(input)-start+size > limit {
			break
		}
		start -= size
	}
	got := TruncateTail(input, truncationLimits(limit, 10))
	if got.Content != input[start:] || len(got.Content) > limit || !utf8.ValidString(got.Content) {
		t.Fatal(fmt.Sprintf("input=%q maxBytes=%d expected=%q actual=%+v", input, limit, input[start:], got))
	}
}
