package codingagent

import (
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestSessionEntryWritersPreserveSurrogateEscapes(t *testing.T) {
	// Pi session-manager.ts:1160-1185 persists JSON.stringify(entry), which spells a lone UTF-16 unit as a lowercase \udXXX escape; JSON.parse reads the same unit back. Expectations are JSON.stringify spellings, not a decode of PiG's own output.
	high, low := jsstring.FromUTF16([]uint16{0xd800}), jsstring.FromUTF16([]uint16{0xdfff})
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, dir).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	user := upstreamSessionUser(t, session, "hi")
	upstreamSessionAssistant(t, session, "ok")
	label := "l" + low
	steps := []func() error{
		func() error { _, err := session.AppendSessionInfo("n" + high); return err },
		func() error { _, err := session.AppendLabelChange(user, &label); return err },
		func() error {
			_, err := session.AppendCustomEntry("c"+high, map[string]any{"k" + high: "v" + low})
			return err
		},
		func() error {
			_, err := session.AppendCustomMessageEntry("cm"+high, "x"+high, true, map[string]any{"d": low})
			return err
		},
		func() error {
			_, err := session.AppendCompaction("s"+high, user, 5, map[string]any{"d": high}, true, nil)
			return err
		},
		func() error {
			_, err := session.BranchWithSummary(&user, "b"+low, map[string]any{"d": low}, true, nil)
			return err
		},
		func() error { _, err := session.AppendModelChange("p"+high, "m"+low); return err },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(session.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"name":"n\ud800"`,
		`"label":"l\udfff"`,
		`"customType":"c\ud800","data":{"k\ud800":"v\udfff"}`,
		`"customType":"cm\ud800","content":"x\ud800","display":true,"details":{"d":"\udfff"}`,
		`"summary":"s\ud800"`,
		`"details":{"d":"\ud800"}`,
		`"summary":"b\udfff","details":{"d":"\udfff"}`,
		`"provider":"p\ud800","modelId":"m\udfff"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session file lacks %s", want)
		}
	}
	if strings.Contains(string(data), `\ufffd`) || strings.Contains(string(data), "\ufffd") {
		t.Errorf("session file replaced a lone surrogate:\n%s", data)
	}

	reloaded, err := NewSessionManagerWithDir(dir, dir).Load(session.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetSessionName(); got != "n"+high {
		t.Errorf("reloaded name units=%x, want %x", jsstring.ToUTF16(got), jsstring.ToUTF16("n"+high))
	}
	if got := sessionNode(t, reloaded, user).Label; got != label {
		t.Errorf("reloaded label units=%x, want %x", jsstring.ToUTF16(got), jsstring.ToUTF16(label))
	}

	// Pinned Pi 0.87.1 opens PiG's file and re-serializes every entry with JSON.stringify; each must equal PiG's line byte for byte.
	out, err := piDirectoryNode(t, `
const sm = SessionManager.open(process.argv[2], process.argv[3]);
console.log(JSON.stringify(sm.getSessionName()));
console.log(JSON.stringify(sm.getLabel(process.argv[4])));
for (const entry of sm.getEntries()) console.log(JSON.stringify(entry));
`, session.Path(), dir, user).CombinedOutput()
	if err != nil {
		t.Fatalf("Pi: %v\n%s", err, out)
	}
	piLines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
	pigLines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")[1:]
	if len(piLines) != len(pigLines)+2 {
		t.Fatalf("Pi output has %d lines, want %d:\n%s", len(piLines), len(pigLines)+2, out)
	}
	if piLines[0] != `"n\ud800"` || piLines[1] != `"l\udfff"` {
		t.Errorf("Pi name=%s label=%s, want \"n\\ud800\" and \"l\\udfff\"", piLines[0], piLines[1])
	}
	for i, line := range pigLines {
		if piLines[i+2] != line {
			t.Errorf("entry %d:\nPi  %s\nPiG %s", i, piLines[i+2], line)
		}
	}
}
