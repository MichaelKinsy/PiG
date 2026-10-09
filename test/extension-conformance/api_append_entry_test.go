package extensionconformance

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestConformance_ExtensionAPIAppendEntry pins Pi's pi.appendEntry(customType, data)
// (packages/coding-agent/src/core/extensions/types.ts:1719, agent-session.ts appendCustomEntry): every SDK appends a custom entry through the
// host, which applies it to a real Session through the production binder every mode uses (codingagent.BindAppendEntryAction), and the Session must hold a custom entry with the type and data the SDK sent.
// The fixture's type and data ("conformance-entry", "hello-entry") are values no SDK fallback produces.
func TestConformance_ExtensionAPIAppendEntry(t *testing.T) {
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
			// The host action is the one print/JSON, RPC and interactive mode bind: codingagent.BindAppendEntryAction over the Session.
			codingagent.BindAppendEntryAction(h.bridge, func() (*codingagent.Session, error) { return session.Inner(), nil }, nil)

			customEntries := func() []string {
				var found []string
				for _, entry := range session.Entries() {
					if entry.Base().Type != "custom" {
						continue
					}
					var custom struct {
						CustomType string `json:"customType"`
						Data       any    `json:"data"`
					}
					if json.Unmarshal(entry.Raw(), &custom) == nil {
						found = append(found, custom.CustomType+"="+toString(custom.Data))
					}
				}
				return found
			}
			if got := customEntries(); len(got) != 0 {
				t.Fatalf("the Session starts with custom entries %v", got)
			}
			runConformanceCommand(t, h, "append_entry")
			pollUntilConformance(t, 5*time.Second, "the Session never received the entry", func() bool {
				return slices.Contains(customEntries(), "conformance-entry=hello-entry")
			})
			if got := customEntries(); !slices.Equal(got, []string{"conformance-entry=hello-entry"}) {
				t.Fatalf("custom entries = %v, want exactly the one the SDK appended", got)
			}
		})
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}
