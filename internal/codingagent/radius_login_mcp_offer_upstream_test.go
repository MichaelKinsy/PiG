//go:build !pig_strip_mcp

package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
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

// pumpUntilLoginReturns runs queued UI tasks until the login flow returns. offerRadiusMcpServer writes mcp.json
// (os.WriteFile truncates, then writes) before the flow returns, so a read after this sees the finished file; polling
// the file while the write is in flight can read it empty.
func pumpUntilLoginReturns(t *testing.T, m *InteractiveMode, done <-chan error) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Radius login: %v", err)
			}
			return
		case task := <-m.uiTaskCh:
			task()
		case <-deadline.C:
			t.Fatal("Radius login did not return")
		}
	}
}

func startRadiusLogin(t *testing.T, m *InteractiveMode) <-chan error {
	t.Helper()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	provider := successfulLoginProvider{parityOAuthProvider{id: "radius", name: "Radius"}}
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		done <- m.runLoginRegisteredOAuth(t.Context(), provider, "")
	}()
	t.Cleanup(func() {
		select {
		case <-exited:
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
		done := startRadiusLogin(t, m)
		pumpUntilRendered(t, m, m.editorContainer, "Configure Radius MCP in "+mcpPath+"?", "Yes", "No")
		deliverModalInput(t, m, []byte("\r"))
		pumpUntilLoginReturns(t, m, done)

		data, err := os.ReadFile(mcpPath)
		if err != nil {
			t.Fatalf("mcp.json not written: %v", err)
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
		done := startRadiusLogin(t, m)
		pumpUntilRendered(t, m, m.editorContainer, "Configure Radius MCP in "+mcpPath+"?")
		deliverModalInput(t, m, []byte("\r"))
		pumpUntilLoginReturns(t, m, done)

		data, err := os.ReadFile(mcpPath)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("mcp.json %q: %v", data, err)
		}
		want := map[string]any{"mcpServers": map[string]any{"gateway": map[string]any{"url": upstreamRadiusMCPURL + "/", "auth": map[string]any{"provider": "radius"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mcp.json = %s, want %v", data, want)
		}
	})

	// pig additive (D92): a runtime strip of mcp (cmd/pig applyPigletStrip records it in pigstrip) offers nothing.
	t.Run("a stripped mcp offers nothing", func(t *testing.T) {
		t.Cleanup(pigstrip.Strip(pigstrip.ListExtensions, "mcp"))
		m := newPostLoginTestMode(t)
		mcpPath := filepath.Join(m.opts.AgentDir, "mcp.json")
		pumpUntilLoginReturns(t, m, startRadiusLogin(t, m))
		if got := plainRender(m.editorContainer); strings.Contains(got, "Configure Radius MCP") {
			t.Fatalf("the Radius MCP offer is shown under strip.extensions mcp:\n%s", got)
		}
		if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
			t.Fatalf("mcp.json written under strip.extensions mcp: %v", err)
		}
	})
}

// Pi 1.0.0 interactive-mode.ts:6297-6316: an existing Radius MCP entry keeps its members in order with auth set and
// oauth removed; a new entry is the Radius MCP URL with the Radius login.
func TestRadiusMcpServerConfig(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{"gateway":{"description":"Radius tools","url":"`+RadiusMcpURL+`/","oauth":{"clientName":"x"},"timeout":30}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	existing, err := radiusMcpServerConfig(mcpPath, "gateway", true, "radius")
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"description":"Radius tools","url":"` + RadiusMcpURL + `/","timeout":30,"auth":{"provider":"radius"}}`; string(existing) != want {
		t.Fatalf("existing=%s\nwant     %s", existing, want)
	}
	added, err := radiusMcpServerConfig(mcpPath, "radius", false, "radius")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(added, &config); err != nil {
		t.Fatal(err)
	}
	if want := `{"url":"https://radius.pi.dev/mcp","auth":{"provider":"radius"}}`; string(added) != want {
		t.Fatalf("added=%s, want %s", added, want)
	}
}
