package uiview

import (
	"fmt"
	"strings"
	"testing"
)

func TestBindingKeyTable(t *testing.T) {
	// The reserved table is tested on the parsed key: every row is refused with every modifier beside it.
	for _, row := range ReservedKeys() {
		for _, mods := range []string{"", "shift+", "alt+", "alt+shift+"} {
			id := mods + row.Base
			if row.Ctrl {
				id = "ctrl+" + id
			}
			k, err := ParseBindingKey(id)
			if err != nil {
				t.Errorf("%s: %v", id, err)
				continue
			}
			if got := k.Reserved(); got == nil || got.Base != row.Base || row.Why == "" {
				t.Errorf("%s: reserved row = %+v, want %+v", id, got, row)
			}
			if err := tryMount(t, richAbilities(), bindingRecord(keyBinding(id, "fx.tap"))); err == nil || err.Code != CodeInvalid || !strings.Contains(err.Message, row.Why) {
				t.Errorf("%s: error = %v, want invalid naming %q", id, err, row.Why)
			}
		}
	}
	for id, want := range map[string]BindingKey{
		"d":            {Base: "d"},
		"shift+ctrl+p": {Base: "p", Shift: true, Ctrl: true},
		"ctrl+shift+p": {Base: "p", Shift: true, Ctrl: true},
		"alt+pageUp":   {Base: "pageUp", Alt: true},
		"ctrl+\\":      {Base: "\\", Ctrl: true},
	} {
		if got, err := ParseBindingKey(id); err != nil || got != want || got.Reserved() != nil {
			t.Errorf("%s: parsed %+v (%v), want %+v", id, got, err, want)
		}
	}
	if got := fmt.Sprint(BindingGestures()); got != "[swipe-left swipe-right swipe-up swipe-down]" {
		t.Errorf("gestures = %s", got)
	}
}
