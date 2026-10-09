package extensionconformance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestConformance_ExtensionAPISessionToolsCommandsMessages pins Pi's pi.getActiveTools, pi.setActiveTools and pi.sendMessage
// (packages/coding-agent/src/core/extensions/types.ts:1692, 1728-1760 and agent-session.ts _bindExtensionCore). Every SDK calls the member through the host,
// which coding.BindExtensionHostSessionActions, the binding every mode uses, answers from a real Session: the SDK reads the Session's own tool names, and
// its calls change the Session's active tools and append the custom message it sent. pi.getCommands is answered by each mode's own command catalog
// (cmd/pig print and RPC modes, internal/codingagent interactive mode); TestExtensionAPIGetCommandsReadsTheBoundSession drives it through the production binding.
func TestConformance_ExtensionAPISessionToolsCommandsMessages(t *testing.T) {
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
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			// The production binding of every mode (cmd/pig bindSessionExtensionActions) answers the SDKs from the Session.
			coding.BindExtensionHostSessionActions(h.bridge, func() *coding.Session { return session })

			reported := func(prefix string) string {
				for i := range slices.Backward(*h.notify) {
					if rest, ok := strings.CutPrefix((*h.notify)[i], prefix); ok {
						return strings.TrimSuffix(rest, ":info")
					}
				}
				return "<none>"
			}

			// The SDK reads the Session's active tools: a default Session has the built-in coding tools, which no SDK fallback lists.
			if len(session.ActiveToolNames()) < 2 {
				t.Fatalf("the Session's active tools = %v", session.ActiveToolNames())
			}
			*h.notify = nil
			runConformanceCommand(t, h, "active_tools_get")
			pollUntilConformance(t, 5*time.Second, "the SDK never reported its active tools", func() bool { return reported("active_tools:") != "<none>" })
			if got, want := reported("active_tools:"), strings.Join(session.ActiveToolNames(), ","); got != want {
				t.Fatalf("the SDK read active tools %q, the Session holds %q", got, want)
			}
			runConformanceCommandArgs(t, h, "active_tools_set", "read")
			pollUntilConformance(t, 5*time.Second, "the Session never took the active tools the SDK set", func() bool { return slices.Equal(session.ActiveToolNames(), []string{"read"}) })
			*h.notify = nil
			runConformanceCommand(t, h, "active_tools_get")
			pollUntilConformance(t, 5*time.Second, "the SDK never reported its active tools", func() bool { return reported("active_tools:") != "<none>" })
			if got := reported("active_tools:"); got != "read" {
				t.Fatalf("the SDK read active tools %q after setting read", got)
			}

			// A message sent without a turn lands in the Session as a custom message entry.
			runConformanceCommand(t, h, "send_message_no_turn")
			pollUntilConformance(t, 5*time.Second, "the Session never held the custom message the SDK sent", func() bool {
				for _, entry := range session.Entries() {
					var parsed map[string]any
					if json.Unmarshal(entry.Raw(), &parsed) == nil && parsed["type"] == "custom_message" && parsed["customType"] == "notice" && parsed["content"] == "no-turn" {
						return true
					}
				}
				return false
			})
		})
	}
}

// sessionUserMessageAPI is the Go reference for extension.API.SendUserMessage: the production Session, which runs the message as a prompt.
type sessionUserMessageAPI struct {
	extension.API
	session *coding.Session
}

func (a sessionUserMessageAPI) SendUserMessage(content any, options *extension.SendUserMessageOptions) {
	_ = a.session.SendExtensionUserMessage(content, options)
}

// TestConformance_ExtensionAPISendUserMessage pins Pi's pi.sendUserMessage (packages/coding-agent/src/core/extensions/types.ts:1699-1706, agent-session.ts
// _bindExtensionCore): every SDK sends a user message through the host, which runs it as a prompt on a real Session through extension.API, and the
// Session's transcript then holds the user message the SDK sent and the assistant turn that answered it.
func TestConformance_ExtensionAPISendUserMessage(t *testing.T) {
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
			services.Registry().SetRuntimeAPIKey("faux", "sk-faux")
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: &ai.Model{ID: "faux-1", Provider: ai.NewFauxProvider(ai.FauxConfig{})}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			var api extension.API = sessionUserMessageAPI{session: session}
			h.bridge.SetHostAction("sendUserMessage", func(content any, options subprocess.SendUserMessageOptions) error {
				api.SendUserMessage(content, &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAs(options.DeliverAs)})
				return nil
			})

			runConformanceCommandArgs(t, h, "send_user_message", `"probe-user-text"`)
			pollUntilConformance(t, 10*time.Second, "the Session never held the user message the SDK sent and the turn that answered it", func() bool {
				var user, assistant bool
				for _, entry := range session.Entries() {
					raw := string(entry.Raw())
					user = user || (strings.Contains(raw, `"role":"user"`) && strings.Contains(raw, `"probe-user-text"`))
					assistant = assistant || strings.Contains(raw, `"role":"assistant"`)
				}
				return user && assistant
			})
		})
	}
}
