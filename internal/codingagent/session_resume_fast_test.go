package codingagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fastTestHeader = `{"type":"session","id":"s1","timestamp":"2025-01-01T00:00:00.000Z","cwd":"/x"}`

func fastTestLines() []string {
	tr := `{"type":"message","id":"a","message":{"role":"toolResult","content":[{"type":"text","text":"out"}]}}`
	return []string{
		tr,
		`{"type":"message","message":{"role":"toolResult","content":"x"},"id":"a"}`,
		` ` + tr + ` `,
		tr + "\r" + tr,
		strings.Replace(tr, `"type":"message"`, `"type":"message","type":"session_info","name":5`, 1),
		strings.Replace(tr, `"role":"toolResult"`, `"role":"toolResult","role":"user"`, 1),
		strings.Replace(tr, `"role":"toolResult"`, `"role":"user","role":"toolResult"`, 1),
		`{"type":"message","message":{"role":"toolResult"}`,
		`{"type":"message","message":{"role":"toolResult",}}`,
		tr + `x`,
		tr[:len(tr)-1],
		`{"type":"message","message":null,"x":"toolResult"}`,
		`{"type":"message","message":[],"x":"toolResult"}`,
		`{"type":"message","message":{"role":"toolResult","content":"\ud800"}}`,
		"{\"type\":\"message\",\"message\":{\"role\":\"toolResult\",\"content\":\"\xff\"}}",
		`{"type":"message","message":{"role":"toolResult"}}`,
		`{"type":"message","message":{"role":"toolResult","n":01}}`,
		`{"type":"message","message":{"role":"toolResult","n":1e5,"a":[1,{"b":"}"}]}}`,
		`{"type":"message","extra":{"message":{"role":"user","content":"hi"}},"message":{"role":"toolResult"}}`,
		`{"type":"message","message":{"role":"toolResult"}} {"a":1}`,
		`[{"type":"message","message":{"role":"toolResult"}}]`,
		`"toolResult"`, `null`, `0`, `true`, `{}`, `{"type":"message"}`, `{"type":"message","message":{"role":"toolResult"}}`,
		`{"type":"message","message":{"role":"user","content":"hello there","timestamp":5},"timestamp":"2025-01-02T00:00:00Z"}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"toolResult"}]}}`,
		`{"type":"session_info","name":" n "}`,
		`not json toolResult`, ``, `   `, `{"type":"message","message":{"role":"toolResult","content":"` + strings.Repeat("a", 200000) + `"}}`,
	}
}

func TestSessionSummaryFastPathMatchesFullDecode(t *testing.T) {
	dir := t.TempDir()
	for i, line := range fastTestLines() {
		for j, withTail := range []bool{false, true} {
			body := fastTestHeader + "\n" + line + "\n"
			if withTail {
				body += `{"type":"message","message":{"role":"user","content":"tail"}}` + "\n"
			}
			p := filepath.Join(dir, fmt.Sprintf("s%d-%d.jsonl", i, j))
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			want, werr := summarizeSessionFileFull(context.Background(), p)
			got, gerr := summarizeSessionFileContext(context.Background(), p)
			if (werr == nil) != (gerr == nil) || !reflect.DeepEqual(want, got) {
				t.Fatalf("line %d tail %v: want %+v/%v got %+v/%v", i, withTail, want, werr, got, gerr)
			}
		}
	}
}

func TestSessionInfoCacheInvalidatesOnChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	if err := os.WriteFile(p, []byte(fastTestHeader+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := summarizeSessionFileContext(context.Background(), p)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"type":"message","message":{"role":"user","content":"more"}}` + "\n")
	_ = f.Close()
	b, _ := summarizeSessionFileContext(context.Background(), p)
	if a.MessageCount == b.MessageCount {
		t.Fatalf("stale cache: %d", b.MessageCount)
	}
}

// TestSessionSummaryParallelAndIncrementalMatchFullDecode holds the chunked scan and the append catch-up to the full serial decode, with tiny chunks so that boundaries fall inside and between every kind of line.
func TestSessionSummaryParallelAndIncrementalMatchFullDecode(t *testing.T) {
	oldMin, oldChunk, oldMax := parallelScanMinBytes, parallelScanChunkBytes, parallelScanMaxWorkers
	parallelScanMinBytes, parallelScanChunkBytes, parallelScanMaxWorkers = 1, 1, 7
	defer func() {
		parallelScanMinBytes, parallelScanChunkBytes, parallelScanMaxWorkers = oldMin, oldChunk, oldMax
	}()
	user := func(s string, ts int) string {
		return fmt.Sprintf(`{"type":"message","message":{"role":"user","content":"%s","timestamp":%d}}`, s, ts)
	}
	asst := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]},"timestamp":"2025-03-01T00:00:00Z"}`
	name := func(n string) string { return `{"type":"session_info","name":"` + n + `"}` }
	pool := append(fastTestLines(), user("first", 5), user("later", 9), asst, name(" a "), name("b"), `{"type":"session_info","name":null}`, "\r", `{"a":`+"\r"+`1}`)
	for seed := range 40 {
		var lines []string
		for i := range 25 {
			lines = append(lines, pool[(seed*7+i*13+i*i)%len(pool)])
		}
		for _, header := range []string{fastTestHeader, "", "null\n" + fastTestHeader, "{}"} {
			body := header + "\n" + strings.Join(lines, "\n")
			for _, tail := range []string{"", "\n", "\n" + user("tail", 99)} {
				p := filepath.Join(t.TempDir(), "p.jsonl")
				if err := os.WriteFile(p, []byte(body+tail), 0o600); err != nil {
					t.Fatal(err)
				}
				want, werr := summarizeSessionFileFull(context.Background(), p)
				got, gerr := summarizeSessionFileContext(context.Background(), p)
				if (werr == nil) != (gerr == nil) || !reflect.DeepEqual(want, got) {
					t.Fatalf("seed %d header %q tail %q: want %+v/%v got %+v/%v", seed, header, tail, want, werr, got, gerr)
				}
			}
		}
	}
	// Append-only growth reuses the cache and still equals a fresh full decode at each step, including an unterminated final fragment.
	p := filepath.Join(t.TempDir(), "g.jsonl")
	_ = os.WriteFile(p, []byte(fastTestHeader+"\n"+user("one", 1)+"\n"), 0o600)
	for i, add := range []string{user("two", 2) + "\n", name("n") + "\n" + asst, "\n", user("three", 3), "\n" + fastTestLines()[0] + "\n"} {
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		_, _ = f.WriteString(add)
		_ = f.Close()
		want, _ := summarizeSessionFileFull(context.Background(), p)
		got, _ := summarizeSessionFileContext(context.Background(), p)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("step %d: want %+v got %+v", i, want, got)
		}
	}
	// A rewrite to different content of larger size is not an append.
	_ = os.WriteFile(p, []byte(strings.Replace(fastTestHeader, "s1", "s2", 1)+"\n"+strings.Repeat(user("zzz", 7)+"\n", 50)), 0o600)
	want, _ := summarizeSessionFileFull(context.Background(), p)
	got, _ := summarizeSessionFileContext(context.Background(), p)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("rewrite: want %+v got %+v", want, got)
	}
}

// summarizeSessionFileFull is the reference summary: it fully decodes every line. Tests hold the fast path to it.
func summarizeSessionFileFull(ctx context.Context, path string) (SessionInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return SessionInfo{}, err
	}
	return summarizeSessionFileWith(ctx, path, st, false)
}
