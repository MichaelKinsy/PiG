package cli

import (
	"slices"
	"strings"
	"testing"
)

// Pi's stale message (runner.ts:571-600 assertActive, 679-690; the 2860 test's `stale` matcher).
const piStaleContextMessage = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload()."

// pig divergence (D30): records what a captured ctx does in a Go, Rust or Python extension after a session replacement
// (docs/parity/divergences/D30.md, group 4a audit). This test PINS today's behaviour per SDK so that it fails the day an SDK changes:
//   - the state getters (Cwd, Mode, HasUI, Model, the theme, GetBranch, GetEntries) answer with Pi's stale message, as Pi's getters throw it:
//     a Go or Rust getter that has no error result panics with it (the handler's error text), one with an error result returns it, Python raises it;
//     the theme getters of Go (UITheme), Rust and Python are the exception and still answer with the outgoing session's theme;
//   - the host calls follow D30 (b): Go and Rust return the error from the calls that have a result and discard it in Notify, SetStatus,
//     SetActiveTools and Abort (and a []string SetWidget goes through the unchecked widget push); Python raises HostCallError from every host-call method
//     except the same widget push; Node throws Pi's message from all of them;
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
		sdk, optionsCall string
		getters, calls   []string
	}{
		{
			sdk:         "node",
			optionsCall: "setWidgetOptions",
			getters:     getters([]string{"cwd", "hasUI", "model", "theme", "branch", "entries"}, func(string) string { return "throws:" + stale }),
			calls: append(rows("call:", append(callNames, "setWidgetOptions"), func(string) string { return "throws:" + stale }),
				"replacement:named=false customEntries=0 conversation="),
		},
		{
			sdk:         "go",
			optionsCall: "setWidgetOptions",
			getters: getters([]string{"cwd", "mode", "hasUI", "model", "theme", "branch", "entries"}, func(name string) string {
				switch name {
				case "theme":
					return "sdk.UITheme"
				case "branch", "entries":
					return "error:" + stale
				}
				return "panics:" + stale
			}),
			calls: append(rows("call:", append(callNames, "setWidgetOptions"), func(name string) string {
				switch name {
				case "notify", "setStatus", "setActiveTools", "abort", "setWidgetLines":
					return "nil"
				}
				return "error:" + stale
			}), "replacement:toolsChanged=false named=false customEntries=0 conversation="),
		},
		{
			sdk:         "python",
			optionsCall: "setWidgetOptions",
			getters: getters([]string{"cwd", "mode", "hasUI", "model", "theme", "branch", "entries"}, func(name string) string {
				if name == "theme" {
					return "Theme"
				}
				return "raises:" + stale
			}),
			calls: append(rows("call:", append(callNames, "setWidgetOptions"), func(name string) string {
				if name == "setWidgetLines" {
					return "nil"
				}
				return "raises:" + hostCall
			}), "replacement:toolsChanged=false named=false customEntries=0 conversation="),
		},
		{
			sdk:         "rust",
			optionsCall: "setWidgetValue",
			getters: getters([]string{"cwd", "mode", "hasUI", "model", "theme", "branch", "entries"}, func(name string) string {
				switch name {
				case "theme":
					return "Theme"
				case "branch", "entries":
					return "error:" + stale
				}
				return "panics:" + stale
			}),
			calls: append(rows("call:", append(callNames, "setWidgetValue"), func(name string) string {
				switch name {
				case "notify", "setStatus", "setActiveTools", "abort", "setWidgetLines":
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
