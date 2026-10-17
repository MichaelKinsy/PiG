package extensionconformance

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// TestConformance_TruncationHelpersAgreeAcrossSDKs: Pi exports formatSize, truncateHead, truncateTail and truncateLine (coding-agent index.ts:361-385, core/tools/truncate.ts) for
// extension tools. The Node SDK runs Pi's own code, so its answer is the reference: every case below runs in each SDK and the results must equal the Node result,
// which this test also pins to the values Pi documents.
func TestConformance_TruncationHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	long := strings.Repeat("0123456789", 60) // 600 UTF-16 units
	lines := func(n int) string {
		var b strings.Builder
		for i := range n {
			b.WriteString("line")
			b.WriteString(string(rune('a' + i%26)))
			b.WriteString("\n")
		}
		return b.String()
	}
	type tcase = map[string]any
	cases := []tcase{
		{"fn": "head", "content": ""},
		{"fn": "head", "content": "one\ntwo\n"},
		{"fn": "head", "content": lines(5), "maxLines": 3},
		{"fn": "head", "content": lines(5), "maxBytes": 12},
		{"fn": "head", "content": lines(5), "maxBytes": 10},
		{"fn": "head", "content": lines(5), "maxBytes": 11},
		{"fn": "head", "content": lines(5), "maxLines": 0},
		{"fn": "head", "content": "no newline at all", "maxBytes": 4},
		{"fn": "head", "content": "héllo\nwörld\n", "maxBytes": 8},
		{"fn": "head", "content": "a\nb\nc", "maxLines": 2, "maxBytes": 3},
		{"fn": "head", "content": "a\r\nb\r\nc\r\n", "maxLines": 2},
		{"fn": "tail", "content": ""},
		{"fn": "tail", "content": lines(5), "maxLines": 3},
		{"fn": "tail", "content": lines(5), "maxBytes": 12},
		{"fn": "tail", "content": lines(5), "maxBytes": 10},
		{"fn": "tail", "content": lines(5), "maxBytes": 11},
		{"fn": "tail", "content": lines(5), "maxLines": 0},
		{"fn": "tail", "content": "short\n" + strings.Repeat("é", 10), "maxBytes": 7},
		{"fn": "tail", "content": "日本語のテキスト", "maxBytes": 7},
		{"fn": "tail", "content": "a\nb\nc\n", "maxLines": 2, "maxBytes": 3},
		{"fn": "tail", "content": "x\n\n\n", "maxLines": 2},
		// The partial last line fills a one-line limit, so Pi's final check reports truncatedBy "lines" although the byte limit cut it.
		{"fn": "tail", "content": "short\n" + strings.Repeat("é", 10), "maxLines": 1, "maxBytes": 7},
		{"fn": "tail", "content": "a\r\nb\r\nc\r\n", "maxLines": 2},
		{"fn": "line", "content": "short"},
		{"fn": "line", "content": strings.Repeat("x", 500)},
		{"fn": "line", "content": strings.Repeat("x", 501)},
		{"fn": "line", "content": long, "maxChars": 20},
		{"fn": "line", "content": strings.Repeat("😀", 10), "maxChars": 20},
		// 10 emoji are 10 code points but 20 UTF-16 units: a limit of 16 cuts after 8 of them, which a code-point count would not.
		{"fn": "line", "content": strings.Repeat("😀", 10), "maxChars": 16},
		{"fn": "line", "content": strings.Repeat("😀", 10), "maxChars": 8},
		// A cut inside a surrogate pair: Pi and Python keep the lone high surrogate, Go and Rust write U+FFFD; Go decodes the escaped lone surrogate to U+FFFD, so every SDK agrees on the host.
		{"fn": "line", "content": strings.Repeat("😀", 10), "maxChars": 7},
		{"fn": "line", "content": "日本語日本語", "maxChars": 3},
		{"fn": "line", "content": "abc", "maxChars": 0},
	}
	for _, bytes := range []int{0, 1, 1023, 1024, 1025, 1075, 1280, 1536, 10240, 52429, 1048575, 1048576, 1310720, 1572864, 5505024, 1073741824} {
		cases = append(cases, tcase{"fn": "size", "bytes": bytes})
	}
	// A seeded corpus of mixed ASCII, CR/LF, two-, three- and four-byte characters, so a byte versus UTF-16 unit boundary mistake in any SDK shows up against Pi's own code.
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{"a", "b", "x", " ", "\n", "\n", "\r\n", "é", "ß", "日", "語", "😀", "𝒜"}
	randomText := func() string {
		var b strings.Builder
		for range rng.IntN(60) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		return b.String()
	}
	for range 40 {
		content := randomText()
		cases = append(cases,
			tcase{"fn": "head", "content": content, "maxLines": rng.IntN(6), "maxBytes": rng.IntN(40)},
			tcase{"fn": "tail", "content": content, "maxLines": rng.IntN(6), "maxBytes": rng.IntN(40)},
			tcase{"fn": "line", "content": content, "maxChars": rng.IntN(30)})
	}
	cases = append(cases, tcase{"fn": "head", "content": lines(2500)}, tcase{"fn": "tail", "content": lines(2500)}, tcase{"fn": "tail", "content": strings.Repeat("y", 60*1024)})

	run := func(t *testing.T, h *harness, c tcase) map[string]any {
		t.Helper()
		args, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "truncate_probe", string(args)) {
			t.Fatal("fixture has no truncate_probe command")
		}
		var reported string
		pollUntilConformance(t, "truncate_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "truncate:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got
	}

	var reference []map[string]any
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node" {
			continue
		}
		h := tc.make(t)
		for _, c := range cases {
			reference = append(reference, run(t, h, c))
		}
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}
	if len(reference) != len(cases) {
		t.Fatalf("the Node reference ran %d of %d cases", len(reference), len(cases))
	}
	// Pi's documented values, so a shared mistake in every SDK cannot pass: formatSize rounds half up (1.25KB is 1.3KB) and truncateTail keeps the end of an oversized last line.
	for i, c := range cases {
		if c["fn"] == "size" && c["bytes"] == 1280 {
			if got := reference[i]["text"]; got != "1.3KB" {
				t.Fatalf("reference formatSize(1280) = %v, want 1.3KB", got)
			}
		}
		if c["fn"] == "tail" && c["content"] == "日本語のテキスト" {
			if got := reference[i]["content"]; got != "スト" || reference[i]["lastLinePartial"] != true {
				t.Fatalf("reference truncateTail of an oversized last line = %v, want the last whole characters that fit and lastLinePartial", reference[i])
			}
		}
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for i, c := range cases {
				got := run(t, h, c)
				if !reflect.DeepEqual(got, reference[i]) {
					args, _ := json.Marshal(c)
					t.Errorf("case %d %.80s: got %v, Node (Pi) reports %v", i, args, got, reference[i])
				}
			}
		})
	}
}
