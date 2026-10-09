package extensionconformance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// sessionWritesAPI is the Go reference for the Session-writing members of extension.API: the production Session that every SDK's host call reaches.
type sessionWritesAPI struct {
	extension.API
	session *coding.Session
}

func (a sessionWritesAPI) SetSessionName(name string) { _ = a.session.SetSessionName(name) }

func (a sessionWritesAPI) AppendEntry(customType string, data any) {
	_, _ = a.session.AppendCustomEntry(customType, data)
}

func (a sessionWritesAPI) SetLabel(entryID, label string) {
	var value *string
	if label != "" {
		value = &label
	}
	_, _ = a.session.Inner().AppendLabelChange(entryID, value)
}

// TestConformance_ExtensionAPISessionWrites pins Pi's pi.setSessionName, pi.appendEntry and pi.setLabel (packages/coding-agent/src/core/extensions/types.ts:1713-1727
// and agent-session.ts _bindExtensionCore). Every SDK calls the member through the host, which applies it to a real Session through extension.API, and the
// Session then holds the name, the custom entry and the label the SDK wrote.
func TestConformance_ExtensionAPISessionWrites(t *testing.T) {
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
			var api extension.API = sessionWritesAPI{session: session}
			h.bridge.SetHostAction("setSessionName", func(name string) error { api.SetSessionName(name); return nil })
			h.bridge.SetHostAction("appendEntry", func(customType string, data any, _ *subprocess.DirectEntryAppend) error {
				api.AppendEntry(customType, data)
				return nil
			})
			h.bridge.SetHostAction("setLabel", func(entryID, label string) error { api.SetLabel(entryID, label); return nil })

			runConformanceCommand(t, h, "set_session_name")
			pollUntilConformance(t, 5*time.Second, "the Session never took the name the SDK set", func() bool { return session.SessionName() == "conformance-session" })

			runConformanceCommand(t, h, "append_entry")
			var appended map[string]any
			pollUntilConformance(t, 5*time.Second, "the Session never held the custom entry the SDK appended", func() bool {
				for _, entry := range session.Entries() {
					var parsed map[string]any
					if json.Unmarshal(entry.Raw(), &parsed) == nil && parsed["customType"] == "conformance-entry" {
						appended = parsed
						return true
					}
				}
				return false
			})
			if appended["type"] != "custom" || appended["data"] != "hello-entry" {
				t.Fatalf("the appended entry = %v", appended)
			}

			target, err := session.AppendCustomEntry("labelled", "x")
			if err != nil {
				t.Fatal(err)
			}
			runConformanceCommandArgs(t, h, "label_probe", target+" probe-label")
			pollUntilConformance(t, 5*time.Second, "the Session never held the label the SDK set", func() bool {
				label, ok := session.Inner().GetLabel(target)
				return ok && label == "probe-label"
			})
			runConformanceCommandArgs(t, h, "label_probe", target)
			pollUntilConformance(t, 5*time.Second, "an empty label did not clear the label", func() bool {
				_, ok := session.Inner().GetLabel(target)
				return !ok
			})
		})
	}
}
