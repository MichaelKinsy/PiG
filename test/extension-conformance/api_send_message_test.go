package extensionconformance

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// TestConformance_ExtensionAPISendMessage pins Pi's pi.sendMessage(message, options)
// (packages/coding-agent/src/core/extensions/types.ts:1703-1706, agent-session.ts:2293-2328 sendCustomMessage): every SDK sends a custom message through the host,
// which hands it to a real coding.Session through the production binder every mode uses (coding.BindExtensionHostSessionActions). An idle Session whose
// message does not trigger a turn appends it to the log as a custom_message entry (agent-session.ts sendCustomMessage: `else { this.agent.state.messages.push(appMessage); this.sessionManager.appendCustomMessageEntry(...) }`).
// The fixture's customType and content ("notice", "no-turn", "default") are values no SDK fallback produces. Both fixtures avoid a model turn: one passes
// triggerTurn:false, the other no options, and an idle Session only starts a turn when triggerTurn is true.
func TestConformance_ExtensionAPISendMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

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
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			coding.BindExtensionHostSessionActions(h.bridge, func() *coding.Session { return session })

			customMessages := func() []string {
				var found []string
				for _, entry := range session.Entries() {
					if entry.Base().Type != "custom_message" {
						continue
					}
					var custom struct {
						CustomType string `json:"customType"`
						Content    any    `json:"content"`
						Display    bool   `json:"display"`
					}
					if json.Unmarshal(entry.Raw(), &custom) == nil && custom.Display {
						found = append(found, custom.CustomType+"="+toString(custom.Content))
					}
				}
				return found
			}
			if got := customMessages(); len(got) != 0 {
				t.Fatalf("the Session starts with custom messages %v", got)
			}
			runConformanceCommand(t, h, "send_message_no_turn")
			runConformanceCommand(t, h, "send_message_default")
			want := []string{"notice=no-turn", "notice=default"}
			pollUntilConformance(t, "the Session never received the messages", func() bool {
				return len(customMessages()) >= len(want)
			})
			if got := customMessages(); !slices.Equal(got, want) {
				t.Fatalf("custom messages = %v, want %v", got, want)
			}
		})
	}
}
