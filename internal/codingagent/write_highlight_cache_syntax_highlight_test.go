//go:build !pig_strip_syntax_highlight

package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

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
