package components

// A masked one-line input for secret login prompts (p2-tui-port.md section 1.8, a p2 improvement over pi's plain Input): characters show as
// dots, the value is never rendered, echoed after submit, kept in history or snapshotted, and it is wiped from the component as soon as it is
// handed over.
//
// pi: apps/p2/src/tui/components/secret-input.ts

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const (
	pasteStart  = "\x1b[200~"
	pasteEnd    = "\x1b[201~"
	secretLimit = 100_000
)

// SecretInput is the masked input.
type SecretInput struct {
	focused bool
	// OnSubmit receives the value; the input has forgotten it by then.
	OnSubmit func(value string)
	value    []rune
	pasting  bool
	ctx      SelectorContext
}

// NewSecretInput is the SecretInput constructor.
func NewSecretInput(ctx SelectorContext) *SecretInput {
	return &SecretInput{ctx: ctx, OnSubmit: func(string) {}}
}

// SetFocused is the TypeScript's `focused` setter.
func (s *SecretInput) SetFocused(focused bool) { s.focused = focused }

// Length is the number of characters (code points) typed.
func (s *SecretInput) Length() int { return len(s.value) }

// Clear forgets the value.
func (s *SecretInput) Clear() { s.value = nil }

// add appends one line of printable text; pasted line breaks and controls are dropped.
func (s *SecretInput) add(text string) {
	for _, char := range text {
		if char <= 0x1f || (char >= 0x7f && char <= 0x9f) {
			continue
		}
		if len(s.value) < secretLimit {
			s.value = append(s.value, char)
		}
	}
}

// HandleInput is the key handling.
func (s *SecretInput) HandleInput(data string) {
	if strings.Contains(data, pasteStart) {
		s.pasting = true
		data = strings.Replace(data, pasteStart, "", 1)
	}
	if s.pasting {
		end := strings.Index(data, pasteEnd)
		if end == -1 {
			s.add(data)
		} else {
			s.add(data[:end])
			s.pasting = false
		}
		return
	}
	keys := s.ctx.Keys()
	switch {
	case keys.Matches(data, "tui.input.submit"):
		value := string(s.value)
		s.Clear()
		s.OnSubmit(value)
	case keys.Matches(data, "tui.editor.deleteCharBackward"):
		if len(s.value) > 0 {
			s.value = s.value[:len(s.value)-1]
		}
	case keys.Matches(data, "tui.editor.deleteToLineStart") || keys.Matches(data, "tui.editor.deleteWordBackward"):
		s.Clear()
	default:
		printable, ok := tui.DecodeKittyPrintable(data)
		if !ok && data != "" && !strings.ContainsFunc(data, func(r rune) bool { return r <= 0x1f || r == 0x7f }) {
			printable, ok = data, true
		}
		if ok && printable != "" {
			s.add(printable)
		}
	}
}

// Render shows one dot per character, never the value.
func (s *SecretInput) Render(width int) []string {
	dots := strings.Repeat("•", min(len(s.value), max(0, width-6)))
	cursor := ""
	if s.focused {
		cursor = widthx.CursorMarker + "\x1b[7m \x1b[27m"
	}
	return []string{widthx.TruncateToWidth("> "+dots+cursor, width, "…", false)}
}

// Invalidate has nothing to drop.
func (s *SecretInput) Invalidate() {}
