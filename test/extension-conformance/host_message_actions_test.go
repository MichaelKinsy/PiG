package extensionconformance

import (
	"context"
	"slices"
	"testing"
)

// TestSendMessageAndSetSessionNameAcrossSDKs: every SDK forwards ExtensionAPI.sendMessage
// (packages/coding-agent/src/core/extensions/types.ts:1692) with its customType, content and the optional
// triggerTurn/deliverAs it was given, leaving unset options unset, and ExtensionAPI.setSessionName
// (packages/coding-agent/src/core/extensions/types.ts:1715) with the name. Each fixture command makes one host call, so the
// recorded calls are exact.
func TestSendMessageAndSetSessionNameAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	commands := []struct{ name, want string }{
		{"send_message", "sendMessage:notice:hello-custom:steer:true"},
		{"send_message_default", "sendMessage:notice:default:unset:unset"},
		{"send_message_no_turn", "sendMessage:notice:no-turn:unset:false"},
		{"set_session_name", "setSessionName:conformance-session"},
	}
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var want []string
			for _, c := range commands {
				command, ok := findCommand(h.runner, c.name)
				if !ok {
					t.Fatalf("command %s missing", c.name)
				}
				if err := command.Handler(context.Background(), ""); err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				want = append(want, c.want)
				waitFor(t, func() bool { return len(*h.actions) >= len(want) })
			}
			if got := *h.actions; !slices.Equal(got, want) {
				t.Fatalf("host calls %q, want %q", got, want)
			}
		})
	}
}

// TestSetModelAcrossSDKs: every SDK forwards ExtensionAPI.setModel (packages/coding-agent/src/core/extensions/types.ts:1752)
// with the model's provider/id and returns the host's answer: true for a model it switched to and false for one it refused.
// TestConformance_SetLabelAndShortcut covers setLabel.
func TestSetModelAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			for _, call := range []struct{ command, args string }{
				{"set_model", conformanceKnownModel},
				{"set_model", "conformance/refused-model"},
			} {
				command, ok := findCommand(h.runner, call.command)
				if !ok {
					t.Fatalf("command %s missing", call.command)
				}
				if err := command.Handler(context.Background(), call.args); err != nil {
					t.Fatalf("%s %s: %v", call.command, call.args, err)
				}
			}
			wantActions := []string{"setModel:" + conformanceKnownModel, "setModel:conformance/refused-model"}
			waitFor(t, func() bool { return len(*h.actions) >= len(wantActions) })
			if got := *h.actions; !slices.Equal(got, wantActions) {
				t.Fatalf("host calls %q, want %q", got, wantActions)
			}
			wantNotify := []string{"setModel=true:info", "setModel=false:info"}
			waitFor(t, func() bool { return len(h.ui.Recorded()) >= len(wantNotify) })
			if got := h.ui.Recorded(); !slices.Equal(got, wantNotify) {
				t.Fatalf("notifications %q, want %q", got, wantNotify)
			}
		})
	}
}

// TestGetThinkingLevelAndGetCommandsAcrossSDKs: ExtensionAPI.getThinkingLevel
// (packages/coding-agent/src/core/extensions/types.ts:1755) returns the host's thinking level, and ExtensionAPI.getCommands
// (packages/coding-agent/src/core/extensions/types.ts:1742) returns the host's slash commands in order, each with its name,
// description (absent when unset), source and sourceInfo, in every SDK.
func TestGetThinkingLevelAndGetCommandsAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	const want = "getThinkingLevel=xhigh;getCommands=conformance-listed|Listed by the host|prompt||,review|Review the diff|prompt|/prompts/review.md|project,lint||skill|/skills/lint/SKILL.md|user:info"
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			command, ok := findCommand(h.runner, "host_state_probe")
			if !ok {
				t.Fatal("command host_state_probe missing")
			}
			if err := command.Handler(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 })
			if got := h.ui.Recorded(); !slices.Equal(got, []string{want}) {
				t.Fatalf("notifications %q, want [%q]", got, want)
			}
		})
	}
}
