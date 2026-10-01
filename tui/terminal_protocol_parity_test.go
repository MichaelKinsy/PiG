package tui

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type terminalProtocolParityOutput struct {
	lines     *[]string
	recording bool
}

func (o *terminalProtocolParityOutput) Write(p []byte) (int, error) {
	if o.recording {
		*o.lines = append(*o.lines, "write:"+string(p))
	}
	return len(p), nil
}

// TestTerminalProtocolParityObservations is the PiG side of parity scenario tui-components/14-terminal-negotiation-framing. Like test/parity/testdata/terminal-protocol-pi.mjs, each case first runs the private keyboard protocol query, so the DA1 reply the terminal owes it is negotiation (upstream 0.99.1 terminal.ts:262,272), and records from after the query until before teardown. queryAndEnableKittyProtocol is private, so the observations come from this in-package test instead of a public test-only API; test/parity/testdata/terminal-protocol-pig.mjs prints them.
func TestTerminalProtocolParityObservations(t *testing.T) {
	preserveKeyboardProtocolState(t)
	lines := []string{}
	for _, name := range []string{"batch-order", "zero", "DA", "split", "late-confirmation", "rejected-prefix", "replay", "paste", "large-flags", "progress"} {
		writer := &terminalProtocolParityOutput{lines: &lines}
		terminal := NewProcessTerminalWithOutput(nil, nil, writer)
		if err := terminal.DrainInput(time.Second, 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		input := terminal.NewTerminalInput(func(data string) {
			lines = append(lines, "input:"+data+";kitty="+strconv.FormatBool(terminal.KittyProtocolActive()))
		})
		terminal.queryAndEnableKittyProtocol()
		writer.recording = true
		lines = append(lines, name)
		send := func(data string) { input.Process([]byte(data)) }
		flush := func() { <-input.C; input.Flush() }
		switch name {
		case "batch-order":
			send("a\x1b[?7u")
		case "zero":
			send("\x1b[?0u")
			send("\x1b[?62;4;52c")
		case "DA":
			send("\x1b[?62;4;52c")
		case "split":
			send("\x1b[?7")
			time.Sleep(10 * time.Millisecond)
			send("u")
		case "late-confirmation":
			send("\x1b[")
			flush()
			lines = append(lines, "framing-timeout")
			send("?7u")
		case "rejected-prefix":
			send("\x1b[")
			flush()
			lines = append(lines, "framing-timeout")
			send("a")
		case "replay":
			send("\x1b[")
			flush()
			lines = append(lines, "framing-timeout")
			flush()
		case "paste":
			send("\x1b[")
			flush()
			send("\x1b[200~\x1b[?7u\x1b[201~")
			flush()
		case "large-flags":
			send("\x1b[?18446744073709551616u")
			send("\x1b[?" + strings.Repeat("9", 400) + "u")
		case "progress":
			terminal.SetProgress(false)
		}
		lines = append(lines, "kitty="+strconv.FormatBool(terminal.KittyProtocolActive()))
		input.Close()
		lines = append(lines, "closed-timer="+strconv.FormatBool(input.C == nil))
		writer.recording = false
		if err := terminal.DrainInput(time.Second, 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}

	// The DA1 reply owed to the query enables the modifyOtherKeys fallback and is not input.
	da := slices.Index(lines, "DA")
	if da < 0 || lines[da+1] != "write:"+modifyOtherKeysEnable || slices.Contains(lines, "input:\x1b[?62;4;52c;kitty=false") {
		t.Fatalf("owed DA1 reply was not consumed as negotiation: %q", lines)
	}

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(lines); err != nil {
		t.Fatal(err)
	}
	t.Log("terminal-protocol-observation:" + strings.TrimSuffix(encoded.String(), "\n"))
}
