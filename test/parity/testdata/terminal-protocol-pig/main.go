package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type output struct {
	lines     *[]string
	recording bool
}

func (o *output) Write(p []byte) (int, error) {
	if o.recording {
		*o.lines = append(*o.lines, "write:"+string(p))
	}
	return len(p), nil
}

func main() {
	lines := []string{}
	for _, name := range []string{"batch-order", "zero", "DA", "split", "late-confirmation", "rejected-prefix", "replay", "paste", "large-flags", "progress"} {
		writer := &output{lines: &lines}
		terminal := tui.NewProcessTerminalWithOutput(nil, nil, writer)
		tui.SetKittyProtocolActive(false)
		if err := terminal.DrainInput(time.Second, 50*time.Millisecond); err != nil {
			panic(err)
		}
		input := terminal.NewTerminalInput(func(data string) {
			lines = append(lines, "input:"+data+";kitty="+strconv.FormatBool(terminal.KittyProtocolActive()))
		})
		writer.recording = true
		lines = append(lines, name)
		send := func(data string) { input.Process([]byte(data)) }
		flush := func() { <-input.C; input.Flush() }
		switch name {
		case "batch-order":
			send("a\x1b[?7u")
		case "zero":
			send("\x1b[?0u")
			send("\x1b[?62;4;52c")
		case "DA":
			send("\x1b[?62;4;52c")
		case "split":
			send("\x1b[?7")
			time.Sleep(10 * time.Millisecond)
			send("u")
		case "late-confirmation":
			send("\x1b[")
			flush()
			lines = append(lines, "framing-timeout")
			send("?7u")
		case "rejected-prefix":
			send("\x1b[")
			flush()
			lines = append(lines, "framing-timeout")
			send("a")
		case "replay":
			send("\x1b[")
			flush()
			lines = append(lines, "framing-timeout")
			flush()
		case "paste":
			send("\x1b[")
			flush()
			send("\x1b[200~\x1b[?7u\x1b[201~")
			flush()
		case "large-flags":
			send("\x1b[?18446744073709551616u")
			send("\x1b[?" + strings.Repeat("9", 400) + "u")
		case "progress":
			terminal.SetProgress(false)
		}
		lines = append(lines, "kitty="+strconv.FormatBool(terminal.KittyProtocolActive()))
		input.Close()
		lines = append(lines, "closed-timer="+strconv.FormatBool(input.C == nil))
		writer.recording = false
		if err := terminal.DrainInput(time.Second, 50*time.Millisecond); err != nil {
			panic(err)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(lines); err != nil {
		panic(err)
	}
}
