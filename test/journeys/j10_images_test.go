//go:build parity

package journeys

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

// testPNG is a small deterministic picture: both products read the same bytes.
func testPNG() string {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 128, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.String()
}

// fakeXclip answers the clipboard queries of Pi (utils/clipboard-image.ts readClipboardImageViaXclip, utils/clipboard.ts readClipboardText) from files the journey writes under .clip: mode is image, text or none.
const fakeXclip = `#!/bin/sh
mode=$(cat .clip/mode 2>/dev/null || echo none)
want=""
while [ $# -gt 0 ]; do
  case "$1" in -t) want=$2; shift ;; esac
  shift
done
case "$want" in
  TARGETS)
    case "$mode" in
      image) printf 'TARGETS\nimage/png\n' ;;
      text) printf 'TARGETS\nUTF8_STRING\ntext/plain\n' ;;
      *) exit 1 ;;
    esac ;;
  image/png) [ "$mode" = image ] && cat .clip/image.png || exit 1 ;;
  *) [ "$mode" = text ] && cat .clip/text || exit 1 ;;
esac
`

// The clipboard file name carries a random UUID in both products.
var clipboardFileName = harness.Normalizer{
	Pattern: `pi-clipboard-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, With: "pi-clipboard-UUID",
	Reason: "utils/clipboard-image.ts names the pasted file with crypto.randomUUID(): a fresh value per paste in both products",
}

func init() {
	register(&harness.Journey{
		Name:        "images-clipboard-attach",
		Doc:         "Image input: the read tool returning a picture, a clipboard image or text pasted with Ctrl+V (a stub xclip), an @ file mention completed from the editor, and the requests that carry the image to the model.",
		Touches:     []string{"coding/tools", "ai/", "internal/codingagent/", "tui/", "coding/"},
		Normalizers: []harness.Normalizer{clipboardFileName},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{
				Images:   true,
				Files:    map[string]string{"pic.png": testPNG(), "notes.txt": "plain text\n"},
				Bin:      map[string]string{"xclip": fakeXclip},
				ExtraEnv: []string{"DISPLAY=:99"},
			}
		},
		Script: script(
			onPrompt("READIMG", fixed(harness.Use(harness.Call{Name: "read", Args: map[string]any{"path": "pic.png"}}))),
			// The OpenAI-compatible conversion answers an image tool result with a user turn that carries the picture (ai/providers/transform-messages: "Attached image(s) from tool result:").
			func(r *harness.Request) (harness.Reply, bool) {
				if last := r.Last(); last.Role == "user" && len(last.ImageURLs) > 0 && strings.HasPrefix(last.Text, "Attached image(s) from tool result") {
					return harness.Say("I looked at the picture."), true
				}
				return harness.Reply{}, false
			},
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "read-image", Run: func(c *harness.Ctx) {
				prompt(c, "READIMG please", 2)
				c.WaitFor("I looked at the picture.")
				c.Quiet(c.Settle())
				c.Snap("read-image")
			}}},
			{Step: harness.Step{Name: "paste-clipboard-image", Run: func(c *harness.Ctx) {
				c.WriteFile(".clip/mode", "image")
				c.WriteFile(".clip/image.png", testPNG())
				c.Key("C-v")
				c.WaitFor("pi-clipboard-")
				c.Quiet(c.Settle())
				c.Snap("pasted-image-path")
				c.Key("C-u")
				c.Quiet(c.Settle())
			}}},
			{Step: harness.Step{Name: "paste-clipboard-text", Run: func(c *harness.Ctx) {
				c.WriteFile(".clip/mode", "text")
				c.WriteFile(".clip/text", "clipboard words here")
				c.Key("C-v")
				c.WaitFor("clipboard words here")
				c.Quiet(c.Settle())
				c.Snap("pasted-text")
				prompt(c, "", 1)
				c.WaitFor("Echo: clipboard words here")
				c.Snap("pasted-text-sent")
			}}},
			{Step: harness.Step{Name: "at-mention", Run: func(c *harness.Ctx) {
				c.Type("look at @pic")
				// The list row must follow the typed text: the read-image action may already have put "read pic.png" in the transcript, and a Tab sent before the list arrives completes nothing in Pi.
				c.Await("pic.png listed below the editor", func(s string) bool {
					i := strings.LastIndex(s, "look at @pic")
					return i >= 0 && strings.Contains(s[i:], "pic.png")
				})
				c.Quiet(c.Settle())
				c.Snap("mention-list")
				c.Key("Tab")
				c.Quiet(c.Settle())
				c.Snap("mention-completed")
				prompt(c, "", 1)
				c.WaitFor("Echo: look at @pic.png")
				c.Snap("mention-sent")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
