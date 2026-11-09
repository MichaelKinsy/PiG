package extensionconformance

import (
	"context"
	"testing"
)

// TestSendUserMessageExpandPromptTemplatesAcrossSDKs pins ExtensionAPI.sendUserMessage(options.expandPromptTemplates) (packages/coding-agent/src/core/extensions/types.ts:1788-1792,
// agent-session.ts:2530-2557): the option an extension passes reaches the host's sendUserMessage action as sent, true and false alike, in every SDK, isolated and packed.
// The harness records the option only when it was sent, so an SDK that drops it (the host reads it as Pi's default, false, and the recording carries no suffix) fails both cases.
func TestSendUserMessageExpandPromptTemplatesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	cases := append(allHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
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
			command, ok := findCommand(h.runner, "send_user_message_expand")
			if !ok {
				t.Fatal("send_user_message_expand command missing")
			}
			for _, arg := range []string{"true", "false"} {
				*h.actions = nil
				if err := command.Handler(context.Background(), arg); err != nil {
					t.Fatal(err)
				}
				want := "sendUserMessage:expand-user:followUp:expand=" + arg
				waitFor(t, func() bool { return len(*h.actions) > 0 })
				if got := *h.actions; len(got) != 1 || got[0] != want {
					t.Fatalf("host calls %q after expandPromptTemplates=%s, want one %q", got, arg, want)
				}
			}
		})
	}
}
