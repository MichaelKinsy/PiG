package main

import (
	"encoding/json"
	"os"

	"github.com/MichaelKinsy/PiG/tui"
)

func main() {
	for _, operation := range []string{"empty flush", "nonempty flush", "clear"} {
		buffer := tui.NewStdinBuffer(tui.StdinBufferOptions{})
		before := append([]string{}, buffer.ProcessString("\x1b[64u")...)
		flushed := []string{}
		switch operation {
		case "empty flush":
			flushed = append(flushed, buffer.Flush()...)
		case "nonempty flush":
			before = append(before, buffer.ProcessString("\x1b[")...)
			flushed = append(flushed, buffer.Flush()...)
		case "clear":
			buffer.Clear()
		}
		after := append([]string{}, buffer.ProcessString("@")...)
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"operation": operation, "before": before, "flushed": flushed, "after": after}); err != nil {
			panic(err)
		}
	}
}
