package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// appendCodemodeBoundaryProbes adds the codemode card's boundary inputs to the Pi corpus: the cost formatting threshold, zero and negative
// costs in the model-call total, call args at the 80 unit cut (UTF-16 units, a surrogate pair at the cut), partial results with content,
// and image content with images shown and hidden. Every probe runs collapsed and expanded at widths 80 and 28.
func appendCodemodeBoundaryProbes(probes []toolCardProbe, names []string) ([]toolCardProbe, []string) {
	header := "Script completed\nWall time 0.1 seconds\nOutput:\n"
	call := func(name, args string, cost any) map[string]any {
		c := map[string]any{"name": name, "args": args, "status": "ok", "durationMs": 5}
		if cost != nil {
			c["cost"] = cost
		}
		return c
	}
	details := func(items ...map[string]any) map[string]any {
		list := make([]any, len(items))
		for i, item := range items {
			list[i] = item
		}
		return map[string]any{"calls": list}
	}
	add := func(name string, result *toolCardResult, partial bool, noImages bool, images ...string) {
		for _, width := range []int{80, 28} {
			for _, expanded := range []bool{false, true} {
				probes = append(probes, toolCardProbe{Tool: "codemode", Args: map[string]any{"code": "x"}, Result: result, Partial: partial, NoImages: noImages, Images: strings.Join(images, ""), Expanded: expanded, Width: width, CWD: "/tmp", ElapsedMs: 4200})
				names = append(names, fmt.Sprintf("%s width=%d expanded=%v", name, width, expanded))
			}
		}
	}
	ok := func(text string, d map[string]any) *toolCardResult { return &toolCardResult{Content: cardText(header + text), Details: d} }

	// The call card drops every carriage return of the script, not only those before a newline.
	for _, code := range []string{"a\rb", "x = 1;\r\rreturn 2", "\rlead", "a\r\n\tb\r", "wide\r" + strings.Repeat("c", 100)} {
		for _, width := range []int{80, 28} {
			for _, expanded := range []bool{false, true} {
				probes = append(probes, toolCardProbe{Tool: "codemode", Args: map[string]any{"code": code}, Expanded: expanded, Width: width, CWD: "/tmp"})
				names = append(names, fmt.Sprintf("pending code %q width=%d expanded=%v", code, width, expanded))
			}
		}
	}

	// formatCost: toFixed(2) from 0.01 up, toPrecision(2) below it.
	for _, cost := range []float64{0.01, 0.0099, 0.009999999, 0.0100001, 0.1, 1, 12.345, 0.001, 0.00001234, 123456.789, -0.5, -0.001} {
		add(fmt.Sprintf("cost %v", cost), ok("done", details(call("model.a", "{}", cost))), false, false)
	}
	// The total sums the priced calls (truthy cost), shows from two of them, and every one counts when earlier calls are hidden.
	add("cost 0 is not shown", ok("done", details(call("model.a", "{}", 0))), false, false)
	add("cost 0 and one priced call has no total", ok("done", details(call("model.a", "{}", 0), call("model.b", "{}", 0.5))), false, false)
	add("two zero costs and one priced call has no total", ok("done", details(call("model.a", "{}", 0), call("model.b", "{}", 0), call("model.c", "{}", 0.5))), false, false)
	add("cost 0 between two priced calls", ok("done", details(call("model.a", "{}", 0.004), call("model.z", "{}", 0), call("model.b", "{}", 0.006))), false, false)
	add("two priced calls total exactly a cent", ok("done", details(call("model.a", "{}", 0.004), call("model.b", "{}", 0.006))), false, false)
	add("two priced calls total below a cent", ok("done", details(call("model.a", "{}", 0.001), call("model.b", "{}", 0.002))), false, false)
	add("negative and positive cost", ok("done", details(call("model.a", "{}", -0.5), call("model.b", "{}", 0.75))), false, false)
	many := make([]map[string]any, 11)
	for i := range many {
		many[i] = call(fmt.Sprintf("tool%d", i), "{}", nil)
	}
	many[0]["cost"] = 0.25
	many[1]["cost"] = 0.5
	many[10]["cost"] = 0.0
	add("total covers the earlier calls", ok("done", details(many...)), false, false)

	// Call args cut at 80 UTF-16 units: 77 units and "...".
	for _, n := range []int{0, 1, 77, 78, 79, 80, 81, 82, 200} {
		add(fmt.Sprintf("ascii args %d", n), ok("done", details(call("tool", strings.Repeat("a", n), nil))), false, false)
	}
	for _, n := range []int{38, 39, 40} {
		add(fmt.Sprintf("astral args %d code points", n), ok("done", details(call("tool", strings.Repeat("😀", n), nil))), false, false)
	}
	for _, n := range []int{38, 39, 40, 41, 50} {
		add(fmt.Sprintf("a and %d astral code points", n), ok("done", details(call("tool", "a"+strings.Repeat("😀", n), nil))), false, false)
	}
	// A cut inside a pair leaves a lone surrogate, which Pig's main screen widens to U+FFFD after the card is padded (open difference), so the pair
	// ends at or before the cut (leads 75 and below) or starts after it (77 and above).
	for _, lead := range []int{74, 75, 77, 78, 79} {
		add(fmt.Sprintf("surrogate pair at unit %d", lead), ok("done", details(call("tool", strings.Repeat("a", lead)+"😀"+strings.Repeat("b", 10), nil))), false, false)
	}
	add("surrogate pair ends the args at 80 units", ok("done", details(call("tool", strings.Repeat("a", 78)+"😀", nil))), false, false)
	add("wide glyph args", ok("done", details(call("tool", strings.Repeat("漢", 81), nil))), false, false)
	add("args with newline and tab", ok("done", details(call("tool", "a\n\tb"+strings.Repeat("c", 90), nil))), false, false)

	// A partial result hides the output (the header is still dropped and the rows and timer stay).
	partialCalls := details(call("read", `{"path":"a"}`, nil), call("model.a", "{}", 0.5))
	add("partial with content", ok("hello", partialCalls), true, false)
	add("partial with content and no details", ok("hello", nil), true, false)
	add("partial without header", &toolCardResult{Content: cardText("streamed text")}, true, false)
	add("partial empty", &toolCardResult{Content: cardText("")[:0]}, true, false)
	add("partial with full output path", ok("one\ntwo", map[string]any{"fullOutputPath": "/tmp/full.log", "calls": []any{}}), true, false)

	// Image content: the text fallback line per image (the terminal shows no images), with images shown and hidden.
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	image := map[string]any{"type": "image", "data": png, "mimeType": "image/png"}
	for _, noImages := range []bool{false, true} {
		withText := &toolCardResult{Content: append(cardText(header+"shot"), image)}
		add(fmt.Sprintf("text and image noImages=%v", noImages), withText, false, noImages)
		add(fmt.Sprintf("image only noImages=%v", noImages), &toolCardResult{Content: []map[string]any{image}}, false, noImages)
		add(fmt.Sprintf("header and two images noImages=%v", noImages), &toolCardResult{Content: append(cardText(header), image, map[string]any{"type": "image", "data": "", "mimeType": "image/webp"})}, false, noImages)
	}
	// A terminal that shows images: the fallback text appears only when images are hidden.
	for _, noImages := range []bool{false, true} {
		add(fmt.Sprintf("iterm2 text and image noImages=%v", noImages), &toolCardResult{Content: append(cardText(header+"shot"), image)}, false, noImages, "iterm2")
		add(fmt.Sprintf("iterm2 image only noImages=%v", noImages), &toolCardResult{Content: []map[string]any{image}}, false, noImages, "iterm2")
	}
	return probes, names
}

// Pi's getTextOutput names an image without a MIME type "image/unknown" (`img.mimeType ?? "image/unknown"`); a Go ImageContent cannot tell an
// absent type from an empty one, so the oracle probes never use it.
func TestCodemodeTextOutputNamesAnImageWithoutMimeType(t *testing.T) {
	got := codemodeTextOutput([]ai.ToolResultMessageContent{ai.ImageContent{}}, true)
	if want := "[Image: [image/unknown]]"; got != want {
		t.Fatalf("codemodeTextOutput = %q, want %q", got, want)
	}
}
