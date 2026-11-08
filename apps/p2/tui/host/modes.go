package host

// Terminal modes the launcher owns (p2-tui-port.md section 3.6): which private modes worker output may set, which it did set, and the sequences that
// undo them on exit, crash, worker swap, suspend and around the trusted UI.
//
// pi: apps/p2/src/hosts/tui-modes.ts

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// defaultModes is every mode's state in a fresh terminal ("h" set, "l" reset).
var defaultModes = map[string]string{"25": "h", "7": "h", "2004": "l", "2026": "l", "1000": "l", "1002": "l", "1003": "l", "1006": "l", "1004": "l"}

// TerminalReset is independent of tracking: everything a worker could have touched, back to a usable shell terminal.
const TerminalReset = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1004l\x1b[?7h\x1b[?2004l\x1b[?25h\x1b[?2026l\x1b[0m\x1b[r\x1b[?1049l"

var privateMode = lazyregexp.New(`\x1b\[\?(\d+)([hl])`)

// TerminalModes tracks the modes the current worker changed.
type TerminalModes struct {
	// Allowed are the modes a frame may set.
	Allowed map[string]bool
	// state is the last state per mode as written by the current worker (only modes it changed), in the order first changed.
	order []string
	state map[string]string
}

// NewTerminalModes admits the base modes, the mouse modes unless the policy is off, and 1003 only for "all".
func NewTerminalModes(mouse MousePolicy) *TerminalModes {
	allowed := map[string]bool{}
	for mode := range BaseModes {
		allowed[mode] = true
	}
	if mouse != MouseOff && mouse != "" {
		for _, mode := range MouseModes {
			allowed[mode] = true
		}
	}
	if mouse == MouseAll {
		allowed["1003"] = true
	}
	return &TerminalModes{Allowed: allowed, state: map[string]string{}}
}

// Filter filters one worker frame and records every private mode it changes.
func (m *TerminalModes) Filter(data string) (string, error) {
	output, err := FilterTerminalOutput(data, m.Allowed)
	if err != nil {
		return "", err
	}
	for _, match := range privateMode.FindAllStringSubmatch(output, -1) {
		if _, ok := m.state[match[1]]; !ok {
			m.order = append(m.order, match[1])
		}
		m.state[match[1]] = match[2]
	}
	return output, nil
}

// Restore re-applies the worker's mode state after launcher-drawn UI (worker output was discarded meanwhile).
func (m *TerminalModes) Restore() string {
	var b strings.Builder
	for _, mode := range m.order {
		if mode != "1049" && mode != "2026" {
			b.WriteString("\x1b[?" + mode + m.state[mode])
		}
	}
	return b.String()
}

func (m *TerminalModes) inverse() string {
	var b strings.Builder
	for _, mode := range m.order {
		if def, ok := defaultModes[mode]; ok && def != m.state[mode] {
			b.WriteString("\x1b[?" + mode + def)
		}
	}
	return b.String()
}

// Cleanup is the inverse of every mode the worker changed, then the fixed reset; it forgets the worker's state.
func (m *TerminalModes) Cleanup() string {
	inverse := m.inverse()
	m.order, m.state = nil, map[string]string{}
	return inverse + TerminalReset
}

// SuspendSequence leaves the worker's terminal state for a local program (editor, suspend) without forgetting it.
func (m *TerminalModes) SuspendSequence() string { return m.inverse() + TerminalReset }

// ResumeSequence re-enters the worker's terminal state after SuspendSequence (the worker then redraws everything).
func (m *TerminalModes) ResumeSequence() string {
	alt := ""
	if m.AltScreen() {
		alt = "\x1b[?1049h"
	}
	return alt + m.Restore() + "\x1b[2J\x1b[H"
}

// AltScreen is whether the worker currently has the alternate screen.
func (m *TerminalModes) AltScreen() bool { return m.state["1049"] == "h" }
