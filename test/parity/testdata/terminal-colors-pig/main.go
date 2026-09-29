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
	if string(data) == "\x1b]11;?\x07" {
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
func main() {
	lines := []string{}
	for _, value := range []string{"rgba:0000/8000/ffff/0000", "rgb:" + strings.Repeat("f", 64) + "/0/0", "rgb:" + strings.Repeat("f", 256) + "/0/0", "\ufeff#ffffff\ufeff", "\u0085#ffffff\u0085", "#+1+2+3", "#-1-2-3"} {
		lines = append(lines, "parsed:"+color(tui.ParseOsc11BackgroundColor("\x1b]11;"+value+"\x07")))
	}
	lines = append(lines, "scheme:"+string(tui.ParseTerminalColorSchemeReport("\x1b[?997;2n\x1b[?997;1n\x1b[?997;1n")))
	ui := tui.NewWithOutput(output{&lines}, 80, 24)
	send := func(data string) {
		lines = append(lines, fmt.Sprintf("consumed:%t", ui.ConsumeOsc11BackgroundResponse(data)))
	}
	first := ui.QueryTerminalBackgroundColor(tui.TerminalColorQueryOptions{TimeoutMs: 1})
	second := ui.QueryTerminalBackgroundColor(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
	lines = append(lines, "first:"+color((<-first).Color))
	send("x")
	send("\x1b]11;#000000\x07")
	select {
	case result := <-second:
		lines = append(lines, "second-pending:false", "early:"+color(result.Color))
	default:
		lines = append(lines, "second-pending:true")
	}
	send("\x1b]11;#ffffff\x07")
	lines = append(lines, "second:"+color((<-second).Color))
	malformed := ui.QueryTerminalBackgroundColor(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
	send("\x1b]11;not-a-color\x07")
	lines = append(lines, "malformed:"+color((<-malformed).Color))
	send("\x1b]11;#000000\x07")
	ui.Stop()
	if err := json.NewEncoder(os.Stdout).Encode(lines); err != nil {
		panic(err)
	}
}
