//go:build integration

// unknown_slash_parity_test.go: parity test for unknown / non-builtin
// slash commands.
//
// Why this exists. Upstream pi's interactive-mode dispatcher routes
// any `/<token>` that is NOT a builtin and NOT a registered extension
// command through to the LLM as ordinary user input. pig instead
// returns a pig-specific error "Error: unknown command: /<token> -
// try /help" (even though /help is not a builtin in either system).
//
// This is a fidelity divergence surfaced 2026-05-10 while reviewing
// integration test quality. The test is intentionally written to
// FAIL on pig until the dispatcher is brought into parity, and is
// documented as a known-failing parity gate, not a flaky test.
//
// This test passes on both binaries after the unknown-command gap closes.
// systems with a symmetric assertion.

package integration

import (
	"strings"
	"testing"
	"time"
)

// TestParity_UnknownSlashCommand sends a slash command that exists on
// neither builtin nor extension surface. Expected upstream-pi
// behavior: the literal text is treated as a user message, the LLM
// receives "/help" (or "/foobar") and replies: i.e. NO "unknown
// command" error in the pane.
//
// pig today: prints "Error: unknown command: /help: try /help".
// That string in the pane is the failure signal.
func TestParity_UnknownSlashCommand(t *testing.T) {
	for _, cfg := range bothSystems(t) {
		cfg := cfg
		t.Run(cfg.name, func(t *testing.T) {
			session, cleanup := launchSystem(t, cfg)
			defer cleanup()

			// /help is convenient because both systems removed it as a
			// builtin. The expected behavior is: forward to LLM, get a
			// reasonable English reply.
			pane := sendSlashAndCapture(t, session, "/help", 12*time.Second)
			t.Logf("%s after /help:\n%s", cfg.name, lastN(pane, 12))

			// Hard rule: never show a pig-specific dispatcher error.
			// upstream pi never produces this string for a non-builtin
			// slash; pig must not either.
			if strings.Contains(pane, "unknown command:") {
				t.Errorf("%s: dispatcher rejected /help with 'unknown command:': upstream forwards to LLM. Pane:\n%s",
					cfg.name, pane)
			}

			// Self-referential bug: error message points at /help even
			// though /help isn't a builtin. Catch this specifically so
			// it shows up clearly in CI logs.
			if strings.Contains(pane, "try /help") {
				t.Errorf("%s: dispatcher error references non-existent /help builtin. Pane:\n%s",
					cfg.name, pane)
			}

			_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
		})
	}
}
