package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

type output struct{ lines *[]string }

func (o output) Write(data []byte) (int, error) {
	if strings.HasPrefix(string(data), "\x1b]10;") {
		*o.lines = append(*o.lines, "write:"+string(data))
	}
	return len(data), nil
}
func color(value *tui.RgbColor) string {
	if value == nil {
		return "undefined"
	}
	return fmt.Sprintf("%g,%g,%g", value.R, value.G, value.B)
}
func parsed(data string) string {
	reply, ok := tui.ParseOscColorResponse(data)
	if !ok {
		return "undefined"
	}
	target := fmt.Sprint(int(reply.Target))
	switch reply.Target {
	case tui.OscColorTargetForeground:
		target = "foreground"
	case tui.OscColorTargetBackground:
		target = "background"
	}
	return target + ":" + color(reply.RGB)
}

func desc(c tui.TerminalColors) string {
	palette := "undefined"
	if c.Palette != nil {
		parts := make([]string, len(c.Palette))
		for i := range c.Palette {
			parts[i] = color(&c.Palette[i])
		}
		palette = strings.Join(parts, "|")
	}
	return "fg=" + color(c.Foreground) + ";bg=" + color(c.Background) + ";palette=" + palette
}

func main() {
	lines := []string{}
	for _, value := range []string{"rgba:0000/8000/ffff/0000", "rgb:" + strings.Repeat("f", 64) + "/0/0", "rgb:" + strings.Repeat("f", 256) + "/0/0", "\ufeff#ffffff\ufeff", "\u0085#ffffff\u0085", "#+1+2+3", "#-1-2-3"} {
		lines = append(lines, "parsed:"+parsed("\x1b]11;"+value+"\x07"))
	}
	for _, data := range []string{"\x1b]10;#808080\x1b\\", "\x1b]4;7;rgb:ffff/0000/8000\x07", "\x1b]4;999;#010203\x07", "\x1b]12;#010203\x07", "\x1b]11;#ffffff", "\x1b]11;not-a-color\x07"} {
		lines = append(lines, "parsed:"+parsed(data))
	}
	lines = append(lines, "scheme:"+string(tui.ParseTerminalColorSchemeReport("\x1b[?997;2n\x1b[?997;1n\x1b[?997;1n")))
	ui := tui.NewWithOutput(output{&lines}, 80, 24)
	send := func(data string) {
		lines = append(lines, fmt.Sprintf("consumed:%t", ui.ConsumeTerminalColorResponse(data)))
	}
	hex := func(r, g, b int) string { return fmt.Sprintf("#%02x%02x%02x", r, g, b) }
	const da1 = "\x1b[?62;4;52c"
	first := ui.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1})
	second := ui.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
	lines = append(lines, "first:"+desc((<-first).Colors))
	send("x")
	send("\x1b]11;#000000\x07")
	select {
	case result := <-second:
		lines = append(lines, "second-pending:false", "early:"+desc(result.Colors))
	default:
		lines = append(lines, "second-pending:true")
	}
	send(da1)
	send("\x1b]10;#808080\x07")
	send("\x1b]11;#ffffff\x07")
	send("\x1b]11;#123456\x07")
	for index := range 16 {
		send("\x1b]4;" + fmt.Sprint(index) + ";" + hex(index*16, 255-index*16, index) + "\x07")
	}
	lines = append(lines, "second:"+desc((<-second).Colors))
	send(da1)
	malformed := ui.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
	send("\x1b]11;not-a-color\x07")
	send(da1)
	lines = append(lines, "malformed:"+desc((<-malformed).Colors))
	send(da1)
	send("\x1b]11;#000000\x07")
	ui.Stop()
	if err := json.NewEncoder(os.Stdout).Encode(lines); err != nil {
		panic(err)
	}
}
