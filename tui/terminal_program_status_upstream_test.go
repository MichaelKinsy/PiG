package tui

import (
	"strings"
	"testing"
	"testing/synctest"
)

// .upstream/current/packages/tui/src/terminal.ts:576 ProcessTerminal.setProgramStatus (declared at terminal.ts:125); the cases port .upstream/v1.1.0/packages/tui/test/terminal.test.ts "program status (OSC 7501)" (#10607). Every case runs on a harness whose terminal queried at
// start with PI_PROGRAM_STATUS set as the case says.
func TestUpstreamTerminalProgramStatus(t *testing.T) {
	const (
		working = "\x1b]7501;state=working:app=pi\x1b\\"
		clear   = "\x1b]7501;state=clear\x1b\\"
		reply   = "\x1b]7501;?\x1b\\"
		da      = "\x1b[?62;4;52c"
	)
	writtenStatus := func(h *terminalNegotiationHarness) bool {
		for _, write := range h.writes {
			if strings.HasPrefix(write, "\x1b]7501;state=") {
				return true
			}
		}
		return false
	}
	run := func(t *testing.T, override string, fn func(*testing.T, *terminalNegotiationHarness)) {
		t.Setenv("PI_PROGRAM_STATUS", override)
		synctest.Test(t, func(t *testing.T) { fn(t, newTerminalNegotiationHarness(t)) })
	}
	for _, tc := range []struct {
		name     string
		override string
		run      func(*testing.T, *terminalNegotiationHarness)
	}{
		// terminal.test.ts:250
		{"reports the latest status once the terminal answers the query before DA", "", func(t *testing.T, h *terminalNegotiationHarness) {
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi"})
			h.writeCount(t, working, 0)
			h.send(reply)
			h.noInput(t)
			h.writeCount(t, working, 1)
			h.send(da)
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateDone})
			if last := h.writes[len(h.writes)-1]; last != "\x1b]7501;state=done\x1b\\" {
				t.Fatalf("last write = %q", last)
			}
		}},
		// terminal.test.ts:272
		{"reports nothing when DA arrives first and swallows late replies", "", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send(da)
			h.send("\x1b]7501;?\x07")
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi"})
			h.noInput(t)
			if writtenStatus(h) {
				t.Fatalf("status written without support: %q", h.writes)
			}
		}},
		// terminal.test.ts:288
		{"skips the query when PI_PROGRAM_STATUS=1", "1", func(t *testing.T, h *terminalNegotiationHarness) {
			if h.writes[0] != "\x1b[>7u\x1b[?u\x1b[c" {
				t.Fatalf("first write = %q", h.writes[0])
			}
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi"})
			if last := h.writes[len(h.writes)-1]; last != working {
				t.Fatalf("last write = %q", last)
			}
		}},
		// terminal.test.ts:299
		{"skips the query and reports nothing when PI_PROGRAM_STATUS=0", "0", func(t *testing.T, h *terminalNegotiationHarness) {
			if h.writes[0] != "\x1b[>7u\x1b[?u\x1b[c" {
				t.Fatalf("first write = %q", h.writes[0])
			}
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi"})
			h.writeCount(t, working, 0)
		}},
		// terminal.test.ts:314
		{"does not let a DA reply from before a restart end the new query", "", func(t *testing.T, h *terminalNegotiationHarness) {
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi"})
			h.terminal.Stop()
			h.terminal.queryAndEnableKittyProtocol()
			// The first start's replies arrive late: its DA, then the second start's reply and DA.
			h.send(da)
			h.send(reply)
			h.send(da)
			if last := h.writes[len(h.writes)-1]; last != working {
				t.Fatalf("last write = %q, writes=%q", last, h.writes)
			}
			h.noInput(t)
		}},
		// terminal.test.ts:337
		{"clears the status on stop and reports it again after restart", "", func(t *testing.T, h *terminalNegotiationHarness) {
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi"})
			h.send(reply)
			h.terminal.Stop()
			found := false
			for _, write := range h.writes {
				found = found || write == clear
			}
			if !found {
				t.Fatalf("no clear on stop: %q", h.writes)
			}
			// Stopped: nothing is written until the restarted terminal confirms support again.
			before := len(h.writes)
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pi"})
			if len(h.writes) != before {
				t.Fatalf("wrote while stopped: %q", h.writes[before:])
			}
			h.terminal.queryAndEnableKittyProtocol()
			h.send(reply)
			if last := h.writes[len(h.writes)-1]; last != working {
				t.Fatalf("last write = %q", last)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { run(t, tc.override, tc.run) })
	}
}
