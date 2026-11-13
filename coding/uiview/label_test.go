package uiview

import "testing"

// pig additive (D614): one rule holds every text a client writes as a label: a mount's title, a mount's key, which
// labels a mount that has no title, and a binding's name. ESC, a line break, a C1 control, a line or paragraph
// separator and a control that reorders the text around it (an embedding, an override, an isolate) are refused, so
// that a label cannot pass for another. The direction marks of Arabic and Hebrew text beside Latin reorder nothing on
// their own and are accepted.
func TestLabelsHoldNoControlThatBreaksOrReordersALine(t *testing.T) {
	refused := map[string]string{
		"ESC": "Day\x1b[31m", "newline": "Day\nWeek", "DEL": "Day\x7f", "C1 next line": "Day\u0085Week",
		"line separator": "Day\u2028Week", "paragraph separator": "Day\u2029Week",
		"left-to-right embedding": "\u202aDay", "right-to-left embedding": "\u202bDay", "pop directional formatting": "Day\u202c",
		"left-to-right override": "\u202dDay", "right-to-left override": "\u202eyaD",
		"left-to-right isolate": "\u2066Day", "right-to-left isolate": "\u2067Day", "first strong isolate": "\u2068Day",
		"pop directional isolate": "Day\u2069",
	}
	accepted := map[string]string{
		"Latin":                               "Day",
		"Hebrew with a right-to-left mark":    "\u05d9\u05d5\u05dd (3)\u200f",
		"Arabic with an Arabic letter mark":   "\u061c\u0628\u0631\u064a\u062f 3",
		"Latin with a left-to-right mark":     "Mail\u200e (3)",
		"a zero width joiner between letters": "a\u200db",
	}
	labels := map[string]func(text string) map[string]any{
		"title": func(text string) map[string]any { return mountRecord("k", map[string]any{"title": text}) },
		"key":   func(text string) map[string]any { return mountRecord(text, nil) },
		"binding name": func(text string) map[string]any {
			return bindingRecord(map[string]any{"key": "a", "intent": "fx.tap", "name": text})
		},
	}
	for label, record := range labels {
		for what, text := range refused {
			if err := tryMount(t, richAbilities(), record(text)); err == nil || err.Code != CodeInvalid {
				t.Errorf("%s with %s (%q): %v, want invalid", label, what, text, err)
			}
		}
		for what, text := range accepted {
			if err := tryMount(t, richAbilities(), record(text)); err != nil {
				t.Errorf("%s of %s (%q): %v, want it accepted", label, what, text, err)
			}
		}
	}
}
