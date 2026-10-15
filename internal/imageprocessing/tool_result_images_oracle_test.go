package imageprocessing

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type toolBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// normalizeToolResultImages with autoResizeImages false against Pi's (utils/tool-result-images.js): the declared type is normalised with JS trim and
// toLowerCase, base64 text is decoded with Buffer.from's lenient decoder and re-encoded, and a block whose text, type and hints are unchanged keeps its
// original data (a non-canonical base64 string is therefore replaced). Unsupported declared types go through the PNG conversion, which fails on random bytes
// and keeps the block.
func TestNormalizeToolResultImagesMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	mimes := []string{"image/png", "IMAGE/PNG", "image/jpeg", "image/jpg", "image/JPG", " image/gif ", "image/webp; q=1", "image/png;charset=x", "\ufeffimage/png", "image/png\u00a0",
		"image/png\u0085", "İmage/png", "image/bmp", "image/svg+xml", "text/plain", "", "image/PNG ;", "image/tiff", "\u212a", "image/jpeg\u2028"}
	datas := []string{"", "AAAA", "AAA", "AA", "A", "AAAA=", "AAA=", "AA==", "AA=", "AAAAAA", "iVBORw0KGgo=", "iVBORw0KGgo", "iVBO Rw0K Ggo=", "iVBO\nRw0K\r\nGgo=", "-_-_", "+/+/", "-_+/", "AA==AA==",
		"AAA\u00e9AAA", "\u0141AAA", "AAAA\u0100", "====", "A=A=", "AAAA AAAA", "/9j/4AAQ", "R0lGODlh", "UklGRg==", "AB.CD", "  AAAA  ", "AAAAAAAA\t", "ab+/cd-_", "QUJD", "QUJDRA", "QUJDRA=", "QUJDRA==", "QUJDRA===", "QQ=", "Q"}
	var probes [][]toolBlock
	for range 600 {
		var content []toolBlock
		for range 1 + rng.Intn(4) {
			if rng.Intn(4) == 0 {
				content = append(content, toolBlock{Type: "text", Text: pick("hi", "", "x\ny")})
			} else {
				content = append(content, toolBlock{Type: "image", Data: pick(datas...), MimeType: pick(mimes...)})
			}
		}
		probes = append(probes, content)
	}
	var want [][]toolBlock
	pioracle.Run(t, `
const { normalizeToolResultImages } = await load("pi-coding-agent/utils/tool-result-images.js");
const out = [];
for (const content of input) {
	const result = await normalizeToolResultImages(content, { autoResizeImages: false });
	out.push(result.map((b) => (b.type === "image" ? { type: "image", data: b.data, mimeType: b.mimeType } : { type: "text", text: b.text })));
}
emit(out);
`, probes, &want)
	bad := 0
	for i, content := range probes {
		var blocks []ai.ToolResultMessageContent
		for _, b := range content {
			if b.Type == "text" {
				blocks = append(blocks, ai.TextContent{Text: b.Text})
			} else {
				blocks = append(blocks, ai.ImageContent{Data: b.Data, MimeType: b.MimeType})
			}
		}
		result := NormalizeToolResultImagesWithOptions(agent.AgentToolResult{Content: blocks}, false, nil)
		var got []toolBlock
		for _, block := range result.Content {
			switch b := block.(type) {
			case ai.ImageContent:
				got = append(got, toolBlock{Type: "image", Data: b.Data, MimeType: b.MimeType})
			case ai.TextContent:
				got = append(got, toolBlock{Type: "text", Text: b.Text})
			}
		}
		if !reflect.DeepEqual(got, want[i]) {
			if bad++; bad <= 5 {
				t.Errorf("probe %d %+q:\n got %+q\nwant %+q", i, content, got, want[i])
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
