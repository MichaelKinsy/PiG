package extensionconformance

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// sessionActionsAPI is the Go reference for the host actions of extension.API: each member runs the production Session operation the
// runner binds for an extension (coding/session_bind.go bindExtensionCore: SendMessage, SetActiveTools; Session.SetSessionName).
type sessionActionsAPI struct {
	extension.API
	session *coding.Session
	t       *testing.T
}

func (a sessionActionsAPI) SetSessionName(name string) {
	if err := a.session.SetSessionName(name); err != nil {
		a.t.Fatal(err)
	}
}

// SetActiveTools goes through the context actions the Session binds into its runner (session_bind.go BindCore SetActiveTools), so a wrong binding fails the row.
func (a sessionActionsAPI) SetActiveTools(toolNames []string) {
	a.session.ExtensionRunner().CreateCommandContext().SetActiveTools(toolNames)
}

func (a sessionActionsAPI) SendMessage(message extension.SendMessagePayload, options *extension.SendMessageOptions) {
	if err := a.session.SendMessage(extension.CustomMessageRef(message), options); err != nil {
		a.t.Fatal(err)
	}
}

// TestConformanceAPISessionActions pins Pi's pi.setSessionName (packages/coding-agent/src/core/extensions/types.ts:1726), pi.setActiveTools
// (:1750, agent-session.ts:1518 setActiveToolsByName) and pi.sendMessage (:1703) against a real Session: the name is persisted and read
// back, only the named tools stay active, and a message sent without triggering a turn is appended to the session as a custom message
// carrying its type, content and display flag.
func TestConformanceAPISessionActions(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	var api extension.API = sessionActionsAPI{session: session, t: t}

	if got := session.SessionName(); got != "" {
		t.Fatalf("a new Session is named %q, want unnamed", got)
	}
	api.SetSessionName("conformance-session")
	if got := session.SessionName(); got != "conformance-session" {
		t.Errorf("SessionName() = %q after setSessionName, want conformance-session", got)
	}

	before := session.ActiveToolNames()
	if len(before) < 2 {
		t.Fatalf("the Session starts with active tools %v, want at least two builtin tools", before)
	}
	api.SetActiveTools([]string{before[0]})
	if got := session.ActiveToolNames(); !slices.Equal(got, []string{before[0]}) {
		t.Errorf("ActiveToolNames() = %v after setActiveTools([%s]), want only %s", got, before[0], before[0])
	}

	noTurn := false
	api.SendMessage(extension.SendMessagePayload{CustomType: "conformance-note", Content: "hello-custom", Display: true}, &extension.SendMessageOptions{TriggerTurn: &noTurn})
	found := false
	for _, entry := range session.Entries() {
		var custom struct {
			Type       string `json:"type"`
			CustomType string `json:"customType"`
			Content    any    `json:"content"`
			Display    bool   `json:"display"`
		}
		if err := json.Unmarshal(entry.Raw(), &custom); err != nil {
			t.Fatal(err)
		}
		if custom.Type == "custom_message" && custom.CustomType == "conformance-note" {
			found = custom.Content == "hello-custom" && custom.Display
		}
	}
	if !found {
		t.Errorf("no custom_message entry conformance-note with content hello-custom and display true after sendMessage; entries %+v", session.Entries())
	}
}
