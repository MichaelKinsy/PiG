package codingagent

// pi: packages/coding-agent/src/utils/clipboard.ts

import (
	"maps"
	"slices"
	"testing"
)

// isRemoteSession (clipboard.ts:11-13) is true for SSH_CONNECTION, SSH_CLIENT or MOSH_CONNECTION, and a remote session always emits
// OSC 52 after the local write (clipboard.ts:127-130). Pi's clipboard tests set only SSH_CONNECTION.
func TestCopyToClipboardEveryRemoteSessionVariableEmitsOSC52(t *testing.T) {
	for _, variable := range []string{"SSH_CONNECTION", "SSH_CLIENT", "MOSH_CONNECTION"} {
		t.Run(variable, func(t *testing.T) {
			f := newClipboardFixture(t, "darwin", map[string]string{variable: "client server"})
			if err := f.copier.copy("hello"); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.run.calls, []string{"pbcopy"}) || f.out.String() != "\x1b]52;c;aGVsbG8=\x07" {
				t.Fatalf("calls = %v, OSC 52 = %q; want pbcopy then one OSC 52", f.run.calls, f.out)
			}
		})
	}
	f := newClipboardFixture(t, "darwin", nil)
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if f.osc52Writes() != 0 {
		t.Fatal("a local session emitted OSC 52 after pbcopy succeeded")
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts "PI_CLIPBOARD_OSC52=1 emits OSC 52 after a successful %s copy" (#10826, #10835, #10114): the variable makes the session remote, so each
// platform's local write runs first (native setText on macOS and Windows, xclip on Linux) and one OSC 52 follows. Only the exact value "1" counts.
//
// mutation-checked: the mutant "PI_CLIPBOARD_OSC52 is ignored" fails every row.
func TestCopyToClipboardPIClipboardOSC52ForcesOSC52AfterTheLocalWrite(t *testing.T) {
	const osc52 = "\x1b]52;c;aGVsbG8=\x07"
	for _, tc := range []struct {
		platform string
		env      map[string]string
	}{
		{"darwin", nil},
		{"windows", nil},
		{"linux", map[string]string{"DISPLAY": ":99"}},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			env := map[string]string{"PI_CLIPBOARD_OSC52": "1"}
			maps.Copy(env, tc.env)
			f := newClipboardFixture(t, tc.platform, env)
			var texts []string
			f.native = nativeSetTextRecorder(&texts, func() error { return nil })
			if err := f.copier.copy("hello"); err != nil {
				t.Fatal(err)
			}
			if f.out.String() != osc52 {
				t.Fatalf("terminal output = %q, want one OSC 52 %q", f.out.String(), osc52)
			}
			if tc.platform == "linux" {
				if !slices.Equal(f.run.calls, []string{"xclip"}) || !slices.Equal(f.run.args[0], []string{"-selection", "clipboard"}) || *f.run.input[0] != "hello" {
					t.Fatalf("calls=%v args=%q, want xclip -selection clipboard with the text", f.run.calls, f.run.args)
				}
			} else if !slices.Equal(texts, []string{"hello"}) {
				t.Fatalf("native writes = %q, want the text once", texts)
			}
		})
	}
	for _, value := range []string{"", "0", "true", "yes", "2"} {
		t.Run("value "+value+" is not 1", func(t *testing.T) {
			f := newClipboardFixture(t, "darwin", map[string]string{"PI_CLIPBOARD_OSC52": value})
			if err := f.copier.copy("hello"); err != nil {
				t.Fatal(err)
			}
			if f.osc52Writes() != 0 {
				t.Fatalf("PI_CLIPBOARD_OSC52=%q emitted OSC 52", value)
			}
		})
	}
}
