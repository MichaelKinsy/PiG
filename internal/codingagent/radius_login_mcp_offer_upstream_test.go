package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// The Radius MCP endpoint Pi 1.0.0 offers after a Radius sign-in:
// .upstream/v1.0.0/packages/coding-agent/src/core/radius.ts:5-6 RADIUS_MCP_URL, from DEFAULT_RADIUS_GATEWAY.
const upstreamRadiusMCPURL = "https://radius.pi.dev/mcp"

// pumpUntilRendered runs queued UI tasks until component renders every wanted string.
func pumpUntilRendered(t *testing.T, m *InteractiveMode, component tui.Component, want ...string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	rendered := func() bool {
		got := plainRender(component)
		for _, w := range want {
			if !strings.Contains(got, w) {
				return false
			}
		}
		return true
	}
	for !rendered() {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-time.After(5 * time.Millisecond):
		case <-deadline.C:
			t.Fatalf("never rendered %q; last render:\n%s", want, plainRender(component))
		}
	}
}

func startRadiusLogin(t *testing.T, m *InteractiveMode) <-chan error {
	t.Helper()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	provider := successfulLoginProvider{parityOAuthProvider{id: "radius", name: "Radius"}}
	done := make(chan error, 1)
	go func() { done <- m.runLoginRegisteredOAuth(t.Context(), provider, "") }()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Radius login did not return")
		}
	})
	return done
}

// Implementation-derived (Pi 1.0.0 has no upstream test for this path): after a Radius sign-in, showLoginDialog calls
// offerRadiusMcpServer (.upstream/v1.0.0/packages/coding-agent/src/modes/interactive/interactive-mode.ts:6276), which
// asks "Configure Radius MCP in <agentDir>/mcp.json?" with Yes/No when no global server uses the Radius login, and on
// Yes writes {"url": RADIUS_MCP_URL, "auth": {"provider": "radius"}} under the name "radius"
// (interactive-mode.ts:6296-6343; extensions/mcp/config.ts:156-166 addMcpServerConfig).
func TestRadiusLoginOffersTheRadiusMCPServerUpstream(t *testing.T) {
	t.Run("offers the server and writes it on Yes", func(t *testing.T) {
		m := newPostLoginTestMode(t)
		mcpPath := filepath.Join(m.opts.AgentDir, "mcp.json")
		startRadiusLogin(t, m)
		pumpUntilRendered(t, m, m.editorContainer, "Configure Radius MCP in "+mcpPath+"?", "Yes", "No")
		deliverModalInput(t, m, []byte("\r"))

		deadline := time.Now().Add(5 * time.Second)
		var data []byte
		for {
			var err error
			if data, err = os.ReadFile(mcpPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("mcp.json not written: %v", err)
			}
			select {
			case task := <-m.uiTaskCh:
				task()
			case <-time.After(5 * time.Millisecond):
			}
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("mcp.json %q: %v", data, err)
		}
		want := map[string]any{"mcpServers": map[string]any{"radius": map[string]any{"url": upstreamRadiusMCPURL, "auth": map[string]any{"provider": "radius"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mcp.json = %s, want %v", data, want)
		}
	})

	// interactive-mode.ts:6306-6312: an existing global server at the Radius MCP URL keeps its name; "auth" replaces its MCP OAuth sign-in.
	t.Run("points an existing server at the Radius login", func(t *testing.T) {
		m := newPostLoginTestMode(t)
		mcpPath := filepath.Join(m.opts.AgentDir, "mcp.json")
		existing := `{"mcpServers":{"gateway":{"url":"` + upstreamRadiusMCPURL + `/","oauth":{"clientId":"fixture"}}}}`
		if err := os.WriteFile(mcpPath, []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}
		startRadiusLogin(t, m)
		pumpUntilRendered(t, m, m.editorContainer, "Configure Radius MCP in "+mcpPath+"?")
		deliverModalInput(t, m, []byte("\r"))

		deadline := time.Now().Add(5 * time.Second)
		for {
			data, err := os.ReadFile(mcpPath)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("mcp.json %q: %v", data, err)
			}
			want := map[string]any{"mcpServers": map[string]any{"gateway": map[string]any{"url": upstreamRadiusMCPURL + "/", "auth": map[string]any{"provider": "radius"}}}}
			if reflect.DeepEqual(got, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("mcp.json = %s, want %v", data, want)
			}
			select {
			case task := <-m.uiTaskCh:
				task()
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
}
