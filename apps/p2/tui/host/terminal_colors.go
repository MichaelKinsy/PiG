// Package host is the Go form of p2's TUI launcher side (src/hosts/tui*.ts): the pieces of the launcher that survive the move from a Node launcher
// plus sandboxed guest worker to one compiled-in Go TUI process (D475).
//
// pi: apps/p2/src/hosts/tui-terminal-colors.ts
package host

// The launcher side of `terminalColors` (p2-tui-port.md sections 1.6, 3.2): the launcher queries the visitor's terminal (OSC 10/11/4 + DA1, as
// pi-tui's TUI.queryTerminalColors) and follows light/dark reports (mode 2031). Replies are consumed here, never forwarded.

import (
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

const paletteSize = 16

// TerminalColorQuery is TERMINAL_COLOR_QUERY.
var TerminalColorQuery = func() string {
	var b strings.Builder
	b.WriteString("\x1b]10;?\x07\x1b]11;?\x07")
	for index := range paletteSize {
		b.WriteString("\x1b]4;" + strconv.Itoa(index) + ";?\x07")
	}
	b.WriteString("\x1b[c")
	return b.String()
}()

var (
	oscColor         = lazyregexp.New(`(?i)^\x1b\](?:(1[01])|4;(\d{1,3}));([^\x07\x1b]*)(?:\x07|\x1b\\)$`)
	deviceAttributes = lazyregexp.New(`^\x1b\[\?[\d;]*c$`)
	schemeReport     = lazyregexp.New(`^(?:\x1b\[\?997;(1|2)n)+$`)
	hexChannel       = lazyregexp.New(`(?i)^[0-9a-f]{1,4}$`)
	rgbPrefix        = lazyregexp.New(`(?i)^rgba?:`)
)

func channel(hex string) (float64, bool) {
	if !hexChannel.MatchString(hex) {
		return 0, false
	}
	value, _ := strconv.ParseInt(hex, 16, 64)
	maximum := math.Pow(16, float64(len(hex))) - 1
	return math.Floor(float64(value)/maximum*255 + 0.5), true
}

// ParseOscColor is a copy of pi-tui terminal-colors.ts `parseOscColorValue` (`#rrggbb`, `#rrrrggggbbbb`, `rgb:r/g/b`).
func ParseOscColor(raw string) (pitui.RgbColor, bool) {
	value := strings.TrimFunc(raw, func(r rune) bool { return strings.ContainsRune(" \t\n\v\f\r\u00a0\ufeff\u2028\u2029", r) })
	var parts []string
	if hex, ok := strings.CutPrefix(value, "#"); ok {
		if len(hex) == 6 || len(hex) == 12 {
			third := len(hex) / 3
			parts = []string{hex[:third], hex[third : 2*third], hex[2*third:]}
		}
	} else {
		parts = strings.Split(rgbPrefix.ReplaceAllString(value, ""), "/")
	}
	if len(parts) < 3 {
		return pitui.RgbColor{}, false
	}
	r, okR := channel(parts[0])
	g, okG := channel(parts[1])
	b, okB := channel(parts[2])
	if !okR || !okG || !okB {
		return pitui.RgbColor{}, false
	}
	return pitui.RgbColor{R: r, G: g, B: b}, true
}

// ColorFgBgScheme is colorFgBgScheme: COLORFGBG (`fg;bg`) as coding-agent's detectColorFgBgTheme: a fallback when the terminal reports no background.
func ColorFgBgScheme(env map[string]string) (theme.TerminalTheme, bool) {
	value, ok := env["COLORFGBG"]
	if !ok {
		return "", false
	}
	parts := strings.Split(value, ";")
	background := strings.TrimSpace(parts[len(parts)-1])
	if len(background) == 0 || len(background) > 2 {
		return "", false
	}
	for _, r := range background {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	index, _ := strconv.Atoi(background)
	if index > 15 {
		return "", false
	}
	if index <= 6 || index == 8 {
		return "dark", true
	}
	return "light", true
}

// ColorFgBgSchemeFromEnv is ColorFgBgScheme over the process environment (the TypeScript default `process.env`).
func ColorFgBgSchemeFromEnv() (theme.TerminalTheme, bool) {
	return ColorFgBgScheme(map[string]string{"COLORFGBG": os.Getenv("COLORFGBG")})
}

type pendingQuery struct {
	palette [paletteSize]*pitui.RgbColor
	replied map[string]bool
	timer   *time.Timer
}

// TerminalColorProbeOptions are the TerminalColorProbe constructor options.
type TerminalColorProbeOptions struct {
	Write    func(data string)
	OnUpdate func(state theme.TerminalState)
	Scheme   theme.TerminalTheme
	// Timeout is the query's deadline; the default is 100ms.
	Timeout time.Duration
}

// TerminalColorProbe queries the visitor's terminal for its colors and follows its light/dark reports.
type TerminalColorProbe struct {
	mu       sync.Mutex
	state    theme.TerminalState
	pending  *pendingQuery
	write    func(string)
	onUpdate func(theme.TerminalState)
	timeout  time.Duration
}

// NewTerminalColorProbe is the TerminalColorProbe constructor.
func NewTerminalColorProbe(options TerminalColorProbeOptions) *TerminalColorProbe {
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 100 * time.Millisecond
	}
	return &TerminalColorProbe{write: options.Write, onUpdate: options.OnUpdate, timeout: timeout, state: theme.TerminalState{Scheme: options.Scheme}}
}

// State is the probe's current state.
func (p *TerminalColorProbe) State() theme.TerminalState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Start enables light/dark reports and queries the colors; Stop disables the reports again.
func (p *TerminalColorProbe) Start() {
	p.write("\x1b[?2031h")
	p.mu.Lock()
	p.queryLocked()
	p.mu.Unlock()
	p.write(TerminalColorQuery)
}

// Stop stops the query and the reports.
func (p *TerminalColorProbe) Stop() {
	p.mu.Lock()
	if p.pending != nil {
		p.pending.timer.Stop()
		p.pending = nil
	}
	p.mu.Unlock()
	p.write("\x1b[?2031l")
}

func (p *TerminalColorProbe) queryLocked() {
	if p.pending != nil {
		p.pending.timer.Stop()
	}
	query := &pendingQuery{replied: map[string]bool{}}
	query.timer = time.AfterFunc(p.timeout, func() { p.finish(query) })
	p.pending = query
}

func (p *TerminalColorProbe) finish(query *pendingQuery) {
	p.mu.Lock()
	if p.pending != query {
		p.mu.Unlock()
		return
	}
	query.timer.Stop()
	p.pending = nil
	complete := true
	palette := make([]pitui.RgbColor, 0, paletteSize)
	for _, color := range query.palette {
		if color == nil {
			complete = false
			break
		}
		palette = append(palette, *color)
	}
	if complete {
		p.state.Colors.Palette = palette
	}
	state := p.state
	p.mu.Unlock()
	p.onUpdate(state)
}

// Consume consumes color and scheme replies from terminal input; everything else is forwarded by the caller.
func (p *TerminalColorProbe) Consume(data string) bool {
	if match := schemeReport.FindStringSubmatch(data); match != nil {
		p.mu.Lock()
		// The scheme is the last report of the sequence (the pattern repeats; the capture holds the last).
		if match[1] == "2" {
			p.state.Scheme = "light"
		} else {
			p.state.Scheme = "dark"
		}
		p.queryLocked()
		p.mu.Unlock()
		p.write(TerminalColorQuery)
		return true
	}
	p.mu.Lock()
	query := p.pending
	if query != nil && deviceAttributes.MatchString(data) {
		p.mu.Unlock()
		p.finish(query)
		return true
	}
	match := oscColor.FindStringSubmatch(data)
	if match == nil {
		p.mu.Unlock()
		return false
	}
	// Late replies (after the timeout) still update the colors.
	rgb, hasRGB := ParseOscColor(match[3])
	target := match[2]
	switch match[1] {
	case "10":
		target = "foreground"
	case "11":
		target = "background"
	}
	named := target == "foreground" || target == "background"
	var finish *pendingQuery
	notify := false
	switch {
	case hasRGB && named:
		color := rgb
		if target == "foreground" {
			p.state.Colors.Foreground = &color
		} else {
			p.state.Colors.Background = &color
		}
	case hasRGB && query != nil && !named:
		if index, err := strconv.Atoi(target); err == nil && index < paletteSize {
			color := rgb
			query.palette[index] = &color
		}
	}
	if query != nil {
		query.replied[target] = true
		if len(query.replied) == paletteSize+2 {
			finish = query
		}
	} else if hasRGB && named {
		notify = true
	}
	state := p.state
	p.mu.Unlock()
	if finish != nil {
		p.finish(finish)
	} else if notify {
		p.onUpdate(state)
	}
	return true
}
