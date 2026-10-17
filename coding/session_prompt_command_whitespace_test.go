package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:1766-1779 splits at the first literal space and passes the remaining arguments unchanged to the extension command.
func TestPromptExtensionCommandPreservesArgumentWhitespace(t *testing.T) {
	for _, input := range []string{"/testcmd", "/testcmd hello world", "/testcmd  hello  world \t", "/testcmd \t\n", "/testcmd \ufefftail\ufeff"} {
		for _, extensionOrigin := range []bool{false, true} {
			var received []string
			h := newQueueCharacterizationHarness(t, queueCommandExtension(func(_ context.Context, args string) error {
				received = append(received, args)
				return nil
			}), nil)
			var err error
			if extensionOrigin {
				err = h.session.SendUserMessage(t.Context(), input, &extension.SendUserMessageOptions{ExpandPromptTemplates: new(true)})
			} else {
				err = h.session.Prompt(t.Context(), input)
			}
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if len(input) > len("/testcmd") {
				want = input[len("/testcmd "):]
			}
			if len(received) != 1 || received[0] != want {
				t.Errorf("input=%q extension=%v: arguments=%q; want one %q", input, extensionOrigin, received, want)
			}
			if h.provider.callCount() != 0 || len(h.session.Messages()) != 0 {
				t.Fatal("extension command entered the model transcript")
			}
		}
	}
}
