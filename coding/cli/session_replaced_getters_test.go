package cli

import (
	"slices"
	"strings"
	"testing"
)

// Pi's stale message (runner.ts:571-600 assertActive, 679-690; the 2860 test's `stale` matcher).
const piStaleContextMessage = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload()."

// D30 (docs/parity/divergences/D30.md) covers a captured ctx in a Go, Rust or Python extension after a session replacement.
// This test PINS today's behaviour per SDK so that it fails the day an SDK changes:
//   - the state getters (Cwd, Mode, HasUI, Model, the theme, GetBranch, GetEntries) answer with Pi's stale message, as Pi's getters throw it:
//     a Go or Rust getter that has no error result panics with it (the handler's error text), one with an error result returns it, Python raises it;
//     the theme getters (Go UITheme, Rust theme, Python theme) fail the same way (a Go or Rust one panics, Python raises);
//   - the host calls follow D30 (b): Go and Rust return the error from the calls that have a result (SetWidget in both forms) and discard it in
//     Notify, SetStatus, SetActiveTools and Abort, which have no error result; Python raises HostCallError from every host-call method;
//     Node throws Pi's message from all of them;
//   - nothing a stale ctx does reaches the replacement session (no tool change, no name, no entry, no message).
//
// A stale ctx belongs to the outgoing Session, so Pi's expectation for every row is the stale message (2860-replaced-session-context.test.ts:147-205).
func TestReplacedSessionCapturedContextAfterReplacementPerSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes in four languages")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixtures := replaced2860Fixtures(t)
	stale := piStaleContextMessage
	hostCall := "HostCallError:" + stale
	rows := func(prefix string, names []string, outcome func(name string) string) []string {
		out := make([]string, 0, len(names))
		for _, name := range names {
			out = append(out, prefix+name+"="+outcome(name))
		}
		return out
	}
	getters := func(names []string, outcome func(name string) string) []string {
		return append(rows("getter:", names, outcome), "replacement:")
	}
	callNames := []string{"notify", "setStatus", "setActiveTools", "abort", "sendUserMessage", "appendEntry", "setSessionName", "setWidgetLines"}
	cases := []struct {
		sdk            string
		getters, calls []string
	}{
		{
			sdk:     "node",
			getters: getters([]string{"cwd", "hasUI", "model", "theme", "branch", "entries"}, func(string) string { return "throws:" + stale }),
			calls: append(rows("call:", append(callNames, "setWidgetOptions"), func(string) string { return "throws:" + stale }),
				"replacement:named=false customEntries=0 conversation="),
		},
		{
			sdk: "go",
			getters: getters([]string{"cwd", "mode", "hasUI", "model", "theme", "branch", "entries"}, func(name string) string {
				switch name {
				case "branch", "entries":
					return "error:" + stale
				}
				return "panics:" + stale
			}),
			calls: append(rows("call:", append(callNames, "setWidgetOptions"), func(name string) string {
				switch name {
				case "notify", "setStatus", "setActiveTools", "abort":
					return "nil"
				}
				return "error:" + stale
			}), "replacement:toolsChanged=false named=false customEntries=0 conversation="),
		},
		{
			sdk: "python",
			getters: getters([]string{"cwd", "mode", "hasUI", "model", "theme", "branch", "entries"}, func(string) string {
				return "raises:" + stale
			}),
			calls: append(rows("call:", append(callNames, "setWidgetOptions"), func(string) string {
				return "raises:" + hostCall
			}), "replacement:toolsChanged=false named=false customEntries=0 conversation="),
		},
		{
			sdk: "rust",
			getters: getters([]string{"cwd", "mode", "hasUI", "model", "theme", "branch", "entries"}, func(name string) string {
				switch name {
				case "branch", "entries":
					return "error:" + stale
				}
				return "panics:" + stale
			}),
			calls: append(rows("call:", append(callNames, "setWidgetValue"), func(name string) string {
				switch name {
				case "notify", "setStatus", "setActiveTools", "abort":
					return "nil"
				}
				return "error:" + stale
			}), "replacement:toolsChanged=false named=false customEntries=0 conversation="),
		},
	}
	probeLines := func(lines []string) []string {
		var out []string
		for _, line := range lines {
			if strings.HasPrefix(line, "getter:") || strings.HasPrefix(line, "call:") || strings.HasPrefix(line, "replacement:") {
				out = append(out, line)
			}
		}
		return out
	}
	for _, tc := range cases {
		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			for _, probe := range []struct {
				command string
				want    []string
			}{{"/getters", tc.getters}, {"/calls", tc.calls}} {
				t.Run(strings.TrimPrefix(probe.command, "/"), func(t *testing.T) {
					got := probeLines(runReplaced2860(t, bin, fixtures[tc.sdk], "reply with exactly: seed reply", probe.command))
					if !slices.Equal(got, probe.want) {
						t.Fatalf("%s %s:\n%s\nwant:\n%s", tc.sdk, probe.command, strings.Join(got, "\n"), strings.Join(probe.want, "\n"))
					}
				})
			}
		})
	}
}
