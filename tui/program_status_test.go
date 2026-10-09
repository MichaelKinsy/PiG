package tui

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func decodeProgramStatusMessage(t *testing.T, sequence string) (string, bool) {
	t.Helper()
	match := regexp.MustCompile(`:msg=([A-Za-z0-9+/=]*)`).FindStringSubmatch(sequence)
	if match == nil {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded), true
}

// .upstream/v1.1.0/packages/tui/test/program-status.test.ts:10 (#10607)
func TestFormatProgramStatusUpstream(t *testing.T) {
	t.Run("encodes state, app, kind, and a base64 message", func(t *testing.T) {
		got := FormatProgramStatus(ProgramStatus{State: ProgramStateBlocked, App: "pi", Kind: ProgramStatusKindPermission, Message: "Allow bash?"})
		want := "\x1b]7501;state=blocked:app=pi:kind=permission:msg=" + base64.StdEncoding.EncodeToString([]byte("Allow bash?")) + "\x1b\\"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if got := FormatProgramStatus(ProgramStatus{State: ProgramStateClear}); got != "\x1b]7501;state=clear\x1b\\" {
			t.Fatalf("clear = %q", got)
		}
	})
	// .upstream/v1.1.0/packages/tui/test/program-status.test.ts:21
	t.Run("omits kind outside blocked, invalid app names, and empty messages", func(t *testing.T) {
		if got := FormatProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "my app", Kind: ProgramStatusKindAuth, Message: " \n "}); got != "\x1b]7501;state=working\x1b\\" {
			t.Fatalf("got %q", got)
		}
		if got := FormatProgramStatus(ProgramStatus{State: ProgramStateIdle, App: strings.Repeat("a", 32)}); !regexp.MustCompile(`:app=a{32}\x1b`).MatchString(got) {
			t.Fatalf("32-character app dropped: %q", got)
		}
		if got := FormatProgramStatus(ProgramStatus{State: ProgramStateIdle, App: strings.Repeat("a", 33)}); strings.Contains(got, "app=") {
			t.Fatalf("33-character app kept: %q", got)
		}
	})
	// .upstream/v1.1.0/packages/tui/test/program-status.test.ts:31
	t.Run("replaces control characters, which make terminals discard the report", func(t *testing.T) {
		sequence := FormatProgramStatus(ProgramStatus{State: ProgramStateError, Message: "first\nsecond\x1b[31m\u009bthird\t"})
		if got, _ := decodeProgramStatusMessage(t, sequence); got != "first second [31m third" {
			t.Fatalf("message = %q", got)
		}
	})
	// .upstream/v1.1.0/packages/tui/test/program-status.test.ts:36
	t.Run("cuts long messages at a UTF-8 boundary within the spec limits", func(t *testing.T) {
		sequence := FormatProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi", Message: strings.Repeat("é", 2000)})
		message, _ := decodeProgramStatusMessage(t, sequence)
		if message != strings.Repeat("é", 1024) {
			t.Fatalf("message has %d bytes, want 1024 é", len(message))
		}
		if len(message) > 2048 || len(sequence) > 4096 {
			t.Fatalf("message %d bytes, sequence %d", len(message), len(sequence))
		}
	})
	// program-status.ts:30 MAX_MESSAGE_BYTES = 2048: an ASCII message one byte over the limit keeps exactly 2048 bytes. Upstream's two-byte
	// case cannot tell 2048 from 2049, because both cut 2000 é to 1024 of them.
	t.Run("keeps exactly MAX_MESSAGE_BYTES of a longer ASCII message", func(t *testing.T) {
		sequence := FormatProgramStatus(ProgramStatus{State: ProgramStateWorking, Message: strings.Repeat("a", 2049)})
		if message, _ := decodeProgramStatusMessage(t, sequence); message != strings.Repeat("a", 2048) {
			t.Fatalf("message has %d bytes, want 2048", len(message))
		}
	})
	// A message longer than the limit that ends in a multi-byte character loses the whole character, never half of it.
	t.Run("never splits a code point at the byte limit", func(t *testing.T) {
		sequence := FormatProgramStatus(ProgramStatus{State: ProgramStateWorking, Message: strings.Repeat("a", 2047) + "€"})
		if message, _ := decodeProgramStatusMessage(t, sequence); message != strings.Repeat("a", 2047) {
			t.Fatalf("message has %d bytes", len(message))
		}
	})
}

// .upstream/v1.1.0/packages/tui/test/program-status.test.ts:45
func TestIsProgramStatusReplyUpstream(t *testing.T) {
	for sequence, want := range map[string]bool{
		"\x1b]7501;?\x1b\\":              true,
		"\x1b]7501;?\x07":                true,
		"\x1b]7501;?version=2\x1b\\":     true,
		"\x1b]7501;state=idle\x1b\\":     false,
		"\x1b]11;rgb:0000/0000/0000\x07": false,
	} {
		if got := IsProgramStatusReply(sequence); got != want {
			t.Errorf("IsProgramStatusReply(%q) = %v, want %v", sequence, got, want)
		}
	}
}
