package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func highlightLines(code string) []string {
	return tui.HighlightCode(code, tui.LanguageFromPath("sample.go"))
}

func writeSource(lines int) string {
	var source strings.Builder
	source.WriteString("package sample\r\n\r\n/* a block\r\ncomment */\r\n")
	for i := range lines {
		fmt.Fprintf(&source, "func f%d(x int) string {\r\n\treturn \"value %d\" // note\r\n}\r\n", i, i)
	}
	return source.String()
}

// Upstream's cache holds the normalized lines and one highlighted line for each: the first 50 as a block, the rest each on its own.
func TestWriteHighlightCacheHoldsWhatUpstreamHolds(t *testing.T) {
	for seed := range int64(40) {
		random := rand.New(rand.NewSource(seed))
		content := writeSource(10 + random.Intn(60))
		var cache *writeHighlightCache
		for end := 0; end < len(content); {
			end = min(len(content), end+1+random.Intn(300))
			for end < len(content) && content[end]&0xc0 == 0x80 {
				end++
			}
			cache = updateWriteHighlightCacheIncremental(cache, "sample.go", content[:end])
			if cache == nil {
				t.Fatal("no cache for a .go path")
			}
			normalized := replaceTabs(strings.ReplaceAll(content[:end], "\r", ""))
			if got, want := strings.Join(cache.normalizedLines, "\n"), normalized; got != want {
				t.Fatalf("seed %d end %d: normalized lines\n got %q\nwant %q", seed, end, got, want)
			}
			if len(cache.highlightedLines) != len(cache.normalizedLines) {
				t.Fatalf("seed %d end %d: %d highlighted lines for %d lines", seed, end, len(cache.highlightedLines), len(cache.normalizedLines))
			}
			prefix := min(writePartialFullHighlightLines, len(cache.normalizedLines))
			block := highlightLines(strings.Join(cache.normalizedLines[:prefix], "\n"))
			for i := range cache.normalizedLines {
				want := highlightSingleLine(cache.normalizedLines[i], cache.lang)
				if i < prefix && i < len(block) {
					want = block[i]
				}
				if got := cache.line(i); got != want {
					t.Fatalf("seed %d end %d line %d (prefix %d): got %q want %q", seed, end, i, prefix, got, want)
				}
			}
		}
		// The call's final arguments rebuild: every line highlighted as one block.
		full := rebuildWriteHighlightCacheFull("sample.go", content)
		if strings.Join(full.lines(), "\n") != strings.Join(highlightLines(replaceTabs(strings.ReplaceAll(content, "\r", ""))), "\n") {
			t.Fatal("the full rebuild is not the whole-file highlight")
		}
	}
}

func TestWriteHighlightCacheRebuildsWhenTheContentIsNotAnExtension(t *testing.T) {
	cache := updateWriteHighlightCacheIncremental(nil, "a.go", "package a\nvar x = 1\n")
	if same := updateWriteHighlightCacheIncremental(cache, "a.go", "package a\nvar x = 1\n"); same != cache {
		t.Fatal("unchanged content must return the cache")
	}
	changed := updateWriteHighlightCacheIncremental(cache, "a.go", "package b\nvar x = 1\n")
	if changed == cache || changed.rawContent != "package b\nvar x = 1\n" || changed.normalizedLines[0] != "package b" {
		t.Fatalf("a rewrite must rebuild: %+v", changed)
	}
	otherPath := updateWriteHighlightCacheIncremental(changed, "b.py", "package b\nvar x = 1\n")
	if otherPath == nil || otherPath.lang == changed.lang || otherPath.rawPath != "b.py" {
		t.Fatalf("a new path must rebuild: %+v", otherPath)
	}
	if updateWriteHighlightCacheIncremental(changed, "notes.unknownext", "text") != nil || updateWriteHighlightCacheIncremental(changed, "", "text") != nil {
		t.Fatal("no language, no cache")
	}
}

// Streaming a file in many arguments updates costs what the new text costs. Highlighting the whole file on every update
// cost the file again each time.
//
// The baseline is what Pi pays per update, a rebuild of a text no earlier run extended. A full rebuild of the same growing file resumes the highlighter and costs little more than an incremental update, so this test does not tell the two apart; TestScalingStreamedWriteContent and TestWriteHighlightCacheRebuildsWhenTheContentIsNotAnExtension fail when updates rebuild.
func TestWriteHighlightCacheStreamingCostDoesNotGrowWithTheFile(t *testing.T) {
	content := writeSource(500)
	const updates = 40
	step := len(content) / updates
	measure := func(update func(cache *writeHighlightCache, content string) *writeHighlightCache) uint64 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		var cache *writeHighlightCache
		for i := 1; i <= updates; i++ {
			end := i * step
			for end < len(content) && content[end]&0xc0 == 0x80 {
				end++
			}
			cache = update(cache, content[:end])
		}
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	incremental := measure(func(cache *writeHighlightCache, text string) *writeHighlightCache {
		return updateWriteHighlightCacheIncremental(cache, "sample.go", text)
	})
	// The highlighter resumes a block from the state a longer-lived run left for a text it extends, so rebuilding the same growing file would no longer cost the file each time. A first line that differs per update gives every rebuild a text that extends no earlier one, the cost Pi pays for each rebuild.
	rebuilds := 0
	whole := measure(func(_ *writeHighlightCache, text string) *writeHighlightCache {
		rebuilds++
		return rebuildWriteHighlightCacheFull("sample.go", fmt.Sprintf("// rebuild %d\n", rebuilds)+text)
	})
	t.Logf("allocated while %d updates stream in a %d-byte file: %d bytes incrementally, %d bytes rebuilding", updates, len(content), incremental, whole)
	if incremental*4 > whole {
		t.Fatalf("incremental updates allocated %d bytes against %d for rebuilding", incremental, whole)
	}
}

// The card draws from the cache while its arguments stream and from a rebuild once they are complete; for a file inside the
// block-highlighted prefix the two read the same.
func TestWriteRenderCallStreamsThroughTheCardState(t *testing.T) {
	content := writeSource(12)
	state := map[string]any{}
	var streamed string
	for end := 0; end < len(content); {
		end = min(len(content), end+37)
		for end < len(content) && content[end]&0xc0 == 0x80 {
			end++
		}
		args, _ := json.Marshal(map[string]string{"path": "sample.go", "content": content[:end]})
		component := writeRenderCall(args, nil, extension.ToolRenderContext{State: state, Cwd: "/work", IsPartial: true, Expanded: true})
		streamed = strings.Join(component.Render(200), "\n")
		if state[builtInRenderStateKey].(*builtInRenderState).writeHighlight == nil {
			t.Fatal("the card state holds no cache while the content streams")
		}
	}
	args, _ := json.Marshal(map[string]string{"path": "sample.go", "content": content})
	final := strings.Join(writeRenderCall(args, nil, extension.ToolRenderContext{State: state, Cwd: "/work", ArgsComplete: true, Expanded: true}).Render(200), "\n")
	if streamed != final {
		t.Fatalf("the streamed card differs from the final card:\n%s\n----\n%s", streamed, final)
	}
	// Complete arguments rebuild on every call, so the settled card keeps no copy of the content.
	if state[builtInRenderStateKey].(*builtInRenderState).writeHighlight != nil {
		t.Fatal("the settled card still holds the cache")
	}
}

// writeSourceWithLateComment has a block comment past the block-highlighted prefix, where a line highlighted on its own reads differently from the same line highlighted with the file.
func writeSourceWithLateComment() string {
	var source strings.Builder
	source.WriteString("package sample\n")
	for i := range writePartialFullHighlightLines + 10 {
		fmt.Fprintf(&source, "var v%d = %d\n", i, i)
	}
	source.WriteString("/*\nfunc inside(comment int) string { return \"x\" }\n*/\nvar last = 1\n")
	return source.String()
}

// Upstream renderCall rebuilds the cache from the whole content once the arguments are complete (write.ts renderCall: context.argsComplete ? rebuildWriteHighlightCacheFull), so a construct past line 50 that streamed in line by line ends block-highlighted.
func TestWriteRenderCallRebuildsTheCacheWhenTheArgumentsComplete(t *testing.T) {
	content := writeSourceWithLateComment()
	const inside = "func inside(comment int) string { return \"x\" }"
	block := highlightLines(content)
	blockInside := block[slices.Index(strings.Split(content, "\n"), inside)]
	if single := highlightSingleLine(inside, "go"); slices.Contains(block, single) {
		t.Fatal("the fixture must highlight the comment line differently on its own")
	}
	state := map[string]any{}
	render := func(text string, complete bool) string {
		args, _ := json.Marshal(map[string]string{"path": "sample.go", "content": text})
		return strings.Join(writeRenderCall(args, nil, extension.ToolRenderContext{State: state, Cwd: "/work", IsPartial: !complete, ArgsComplete: complete, Expanded: true}).Render(400), "\n")
	}
	lines := strings.SplitAfter(content, "\n")
	var streamed string
	for end := range lines {
		streamed = render(strings.Join(lines[:end+1], ""), false)
	}
	if cache := state[builtInRenderStateKey].(*builtInRenderState).writeHighlight; cache == nil || slices.Equal(cache.lines(), block) || strings.Contains(streamed, blockInside) {
		t.Fatal("while streaming, lines past the prefix are highlighted one at a time")
	}
	if final := render(content, true); !strings.Contains(final, blockInside) {
		t.Fatal("complete arguments must rebuild the cache from the whole content")
	}
}

// A new path rebuilds even when its language is the same and the content extends the cached content (write.ts updateWriteHighlightCacheIncremental: cache.rawPath !== rawPath).
func TestWriteHighlightCacheRebuildsForANewPathOfTheSameLanguage(t *testing.T) {
	content := writeSourceWithLateComment()
	lines := strings.SplitAfter(content, "\n")
	var cache *writeHighlightCache
	for end := range len(lines) - 1 {
		cache = updateWriteHighlightCacheIncremental(cache, "a.go", strings.Join(lines[:end+1], ""))
	}
	moved := updateWriteHighlightCacheIncremental(cache, "b.go", content)
	if moved == nil || moved.rawPath != "b.go" || !slices.Equal(moved.lines(), highlightLines(content)) {
		t.Fatalf("a new path must rebuild from the whole content: %+v", moved)
	}
}
