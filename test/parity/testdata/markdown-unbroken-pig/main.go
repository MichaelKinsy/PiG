package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

func main() {
	// An exact multiple of the width leaves no final-row padding difference.
	rows := tui.NewMarkdown(strings.Repeat("x", 64<<10)).Render(64)
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
