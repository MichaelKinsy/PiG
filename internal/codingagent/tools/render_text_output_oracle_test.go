package tools

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/tui"
)

type textOutputBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type textOutputCase struct {
	Content    []textOutputBlock `json:"content"`
	ShowImages bool              `json:"showImages"`
	Images     string            `json:"images"`
}

// TestGetTextOutputMatchesPi runs core/tools/render-utils.ts getTextOutput and PiG's GetTextOutput over 2,500 seeded tool results: text blocks with ANSI sequences, carriage
// returns, binary control characters and unpaired-looking bytes, empty text, image blocks that are PNG, JPEG, GIF, WebP, garbage or empty data with a matching, mismatching, empty or unknown
// mime type, in any order, with image support off, kitty or iterm2 and showImages on or off. The output string must be equal: the text blocks joined by newlines, then, when images are not
// shown, one fallback line per image block (mime type and dimensions when the header can be read).
func TestGetTextOutputMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261128, 13))
	pick := func(xs ...string) string { return xs[rng.IntN(len(xs))] }
	webp, err := base64.StdEncoding.DecodeString("UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoBAAEAAgA0JaACdLoB+AADsAD+8MQL/yC5YXXI1/8gP+QH/ID/+PIAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	imageData := func() string {
		width, height := 1+rng.IntN(300), 1+rng.IntN(300)
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		var encoded bytes.Buffer
		switch rng.IntN(7) {
		case 0, 1:
			_ = png.Encode(&encoded, img)
		case 2:
			_ = jpeg.Encode(&encoded, img, nil)
		case 3:
			_ = gif.Encode(&encoded, img, nil)
		case 4:
			encoded.Write(webp)
		case 5:
			encoded.WriteString(pick("garbage", "\x89PNG\r\n\x1a\n", "\xff\xd8\xff", "GIF89a"))
		}
		return base64.StdEncoding.EncodeToString(encoded.Bytes())
	}
	text := func() string {
		var b strings.Builder
		for range rng.IntN(6) {
			b.WriteString(pick("plain", "\x1b[31mred\x1b[0m", "a\r\nb", "\r", "tab\there", "\x00\x01\x07bell", "\x1b]0;title\x07", "é日本🙂", "", "\n", "\x7f\x9b", "\u2028x\ufffe", "\x1b[", "\x08x"))
		}
		return b.String()
	}
	var cases []textOutputCase
	for range 2500 {
		c := textOutputCase{ShowImages: rng.IntN(2) == 0, Images: pick("", "kitty", "iterm2")}
		for range rng.IntN(5) {
			if rng.IntN(2) == 0 {
				c.Content = append(c.Content, textOutputBlock{Type: "text", Text: text()})
				continue
			}
			c.Content = append(c.Content, textOutputBlock{Type: "image", Data: imageData(), MimeType: pick("image/png", "image/jpeg", "image/gif", "image/webp", "image/png", "", "image/unknown", "image/bmp")})
		}
		cases = append(cases, c)
	}

	var want []string
	pioracle.Run(t, `
const { setCapabilities } = await load("pi-tui/index.js");
const { getTextOutput } = await load("pi-coding-agent/core/tools/render-utils.js");
const out = [];
for (const c of input) {
	setCapabilities({ images: c.images === "" ? null : c.images, trueColor: true, hyperlinks: false });
	out.push(getTextOutput({ content: (c.content ?? []).map((b) => b.type === "text" ? { type: "text", text: b.text } : { type: "image", data: b.data, ...(b.mimeType === undefined ? {} : { mimeType: b.mimeType }) }) }, c.showImages));
}
emit(out);`, cases, &want)

	failures := 0
	for i, c := range cases {
		tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocol(c.Images), TrueColor: true})
		var content []ai.ToolResultMessageContent
		for _, b := range c.Content {
			if b.Type == "text" {
				content = append(content, ai.TextContent{Text: b.Text})
			} else {
				content = append(content, ai.ImageContent{Data: b.Data, MimeType: b.MimeType})
			}
		}
		if got := GetTextOutput(content, c.ShowImages); got != want[i] {
			failures++
			if failures <= 5 {
				t.Errorf("case %d (images %q, show %v)\n content %+v\n Go %q\n Pi %q", i, c.Images, c.ShowImages, c.Content, got, want[i])
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
