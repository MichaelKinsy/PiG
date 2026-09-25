package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func main() {
	out := map[string]any{
		"loader":      probeLoader(),
		"bordered":    probeBorderedLoader(),
		"countdown":   probeCountdownTimer(),
		"componentOK": true,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		panic(err)
	}
}

func probeLoader() map[string]any {
	loader := tui.NewLoader("Loading...")
	lines := loader.Render(40)
	before := strings.Join(lines, "\n")
	loader.Tick()
	after := strings.Join(loader.Render(40), "\n")
	return map[string]any{
		"lineCount":        len(lines),
		"firstEmpty":       len(lines) > 0 && lines[0] == "",
		"secondTrimmed":    strings.TrimSpace(stripANSI(lines[1])),
		"secondWidth":      widthx.VisibleWidth(lines[1]),
		"frameAdvanced":    before != after,
		"emptyFramePrefix": emptyFramePrefix(),
	}
}

func emptyFramePrefix() string {
	loader := &tui.Loader{Message: "waiting", Frames: []string{}}
	lines := loader.Render(30)
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(stripANSI(lines[1]))
}

func probeBorderedLoader() map[string]any {
	cancellable := tui.NewBorderedLoader("Loading data...", true)
	cancellableLines := cancellable.Render(60)
	non := tui.NewBorderedLoader("Processing...", false)
	nonLines := non.Render(60)
	before := strings.Join(cancellableLines, "\n")
	cancellable.NextFrame()
	after := strings.Join(cancellable.Render(60), "\n")
	return map[string]any{
		"cancellableLines":    len(cancellableLines),
		"cancellableContains": containsAll(stripANSI(strings.Join(cancellableLines, "\n")), "Loading data", "cancel"),
		"nonCancellableLines": len(nonLines),
		"nonContains":         containsAll(stripANSI(strings.Join(nonLines, "\n")), "Processing"),
		"nonHasCancel":        strings.Contains(stripANSI(strings.Join(nonLines, "\n")), "cancel"),
		"hasBorders":          strings.Contains(stripANSI(cancellableLines[0]), "─") && strings.Contains(stripANSI(cancellableLines[len(cancellableLines)-1]), "─"),
		"frameAdvanced":       before != after,
	}
}

func probeCountdownTimer() map[string]any {
	var ticks []int
	expired := false
	ct := tui.NewCountdownTimer(1100*time.Millisecond, func(seconds int) {
		ticks = append(ticks, seconds)
	}, func() { expired = true })
	time.Sleep(1250 * time.Millisecond)
	ct.Dispose()
	return map[string]any{
		"ticks":   ticks,
		"expired": expired,
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

var _ = fmt.Sprintf
