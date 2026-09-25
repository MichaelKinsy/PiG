package widthx

import "testing"

func TestAnsiCodeTracker_BasicSGR(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b[1m")
	if !tr.HasActiveCodes() {
		t.Fatal("bold should be active")
	}
	if got, want := tr.ActiveCodes(), "\x1b[1m"; got != want {
		t.Errorf("ActiveCodes after bold = %q, want %q", got, want)
	}
	tr.Process("\x1b[31m")
	if got, want := tr.ActiveCodes(), "\x1b[1;31m"; got != want {
		t.Errorf("ActiveCodes after bold+red = %q, want %q", got, want)
	}
}

func TestAnsiCodeTracker_Reset(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b[1;31m")
	tr.Process("\x1b[0m")
	if tr.HasActiveCodes() {
		t.Error("reset should clear all SGR state")
	}
}

func TestAnsiCodeTracker_PartialResets(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b[1;3;4;31;42m") // bold+italic+underline+fg31+bg42
	tr.Process("\x1b[24m")          // underline off
	if got, want := tr.ActiveCodes(), "\x1b[1;3;31;42m"; got != want {
		t.Errorf("after 24m: %q, want %q", got, want)
	}
	tr.Process("\x1b[22m") // bold+dim off
	if got, want := tr.ActiveCodes(), "\x1b[3;31;42m"; got != want {
		t.Errorf("after 22m: %q, want %q", got, want)
	}
	tr.Process("\x1b[39m") // default fg
	if got, want := tr.ActiveCodes(), "\x1b[3;42m"; got != want {
		t.Errorf("after 39m: %q, want %q", got, want)
	}
}

func TestAnsiCodeTracker_256Color(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b[38;5;240m")
	if got, want := tr.ActiveCodes(), "\x1b[38;5;240m"; got != want {
		t.Errorf("256-color fg: %q, want %q", got, want)
	}
	tr.Process("\x1b[48;5;100m")
	if got, want := tr.ActiveCodes(), "\x1b[38;5;240;48;5;100m"; got != want {
		t.Errorf("256-color fg+bg: %q, want %q", got, want)
	}
}

func TestAnsiCodeTracker_RGBColor(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b[38;2;255;128;0m")
	if got, want := tr.ActiveCodes(), "\x1b[38;2;255;128;0m"; got != want {
		t.Errorf("RGB fg: %q, want %q", got, want)
	}
}

func TestAnsiCodeTracker_Hyperlink(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b]8;;https://example.com\x1b\\")
	if got, want := tr.ActiveCodes(), "\x1b]8;;https://example.com\x1b\\"; got != want {
		t.Errorf("after hyperlink open: %q, want %q", got, want)
	}
	// SGR reset must NOT close hyperlink
	tr.Process("\x1b[0m")
	if got, want := tr.ActiveCodes(), "\x1b]8;;https://example.com\x1b\\"; got != want {
		t.Errorf("hyperlink survives SGR reset: %q, want %q", got, want)
	}
	tr.Process("\x1b]8;;\x1b\\") // close
	if tr.HasActiveCodes() {
		t.Error("hyperlink should be closed")
	}
}

func TestAnsiCodeTracker_HyperlinkWithBel(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b]8;;https://e.com\x07")
	if got, want := tr.ActiveCodes(), "\x1b]8;;https://e.com\x07"; got != want {
		t.Errorf("hyperlink (BEL terminated): ActiveCodes = %q, want %q", got, want)
	}
}

func TestAnsiCodeTracker_LineEndReset(t *testing.T) {
	tr := &AnsiCodeTracker{}
	if tr.LineEndReset() != "" {
		t.Error("empty tracker should have no line-end reset")
	}
	tr.Process("\x1b[4m") // underline on
	if got, want := tr.LineEndReset(), "\x1b[24m"; got != want {
		t.Errorf("underline reset: %q, want %q", got, want)
	}
	tr.Process("\x1b]8;;https://e.com\x1b\\")
	want := "\x1b[24m\x1b]8;;\x1b\\"
	if got := tr.LineEndReset(); got != want {
		t.Errorf("underline+hyperlink reset: %q, want %q", got, want)
	}
}

func TestAnsiCodeTracker_UpdateFromText(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.UpdateFromText("\x1b[1mhello\x1b[31m world\x1b[0m")
	// After processing: bold cleared by 0m, fg cleared by 0m → empty
	if tr.HasActiveCodes() {
		t.Error("0m at end should leave tracker empty")
	}
	tr.Clear()
	tr.UpdateFromText("\x1b[1mhello\x1b[31m world")
	// No reset → bold+red active
	if got, want := tr.ActiveCodes(), "\x1b[1;31m"; got != want {
		t.Errorf("ActiveCodes after partial process: %q, want %q", got, want)
	}
}

func TestAnsiCodeTracker_Clear(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b[1;31m")
	tr.Process("\x1b]8;;https://e.com\x1b\\")
	tr.Clear()
	if tr.HasActiveCodes() {
		t.Error("Clear should reset all state including hyperlink")
	}
}

func TestAnsiCodeTracker_BrightColors(t *testing.T) {
	tr := &AnsiCodeTracker{}
	tr.Process("\x1b[91m") // bright red fg
	if got, want := tr.ActiveCodes(), "\x1b[91m"; got != want {
		t.Errorf("bright fg: %q, want %q", got, want)
	}
	tr.Process("\x1b[101m") // bright red bg
	if got, want := tr.ActiveCodes(), "\x1b[91;101m"; got != want {
		t.Errorf("bright fg+bg: %q, want %q", got, want)
	}
}
