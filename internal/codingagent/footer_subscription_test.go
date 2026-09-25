package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The real Node registration and footer caller must preserve Pi's explicit
// subscription flag. OAuth alone does not imply (sub), nor does API-key auth
// on a subscription-capable provider (footer.ts, model-runtime.ts).
func TestExtensionOAuthSubscriptionFooter(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fixture, err := filepath.Abs("../../parity/scenarios/footer/testdata/subscription.mjs")
	if err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	built, err := subprocess.NewBuilder(t.TempDir()).Build("subscription", fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "subscription", Path: built.BinaryPath, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: agentDir}}
	m.statusLine = m.newFooter()
	for _, tc := range []struct {
		provider string
		typeName ai.CredentialType
		want     bool
	}{
		{"subscription-yes", "", false},
		{"subscription-yes", ai.CredentialOAuth, true},
		{"subscription-no", ai.CredentialOAuth, false},
		{"subscription-omitted", ai.CredentialOAuth, false},
		{"subscription-yes", ai.CredentialAPIKey, false},
		{"subscription-yes", ai.CredentialOAuth, true},
	} {
		if tc.typeName != "" {
			if err := auth.Set(tc.provider, ai.Credential{Type: tc.typeName, Access: "fixture", Key: "fixture"}); err != nil {
				t.Fatal(err)
			}
		}
		m.opts.Model = footerTestModel(tc.provider, "label-test", 0, 0)
		m.statusLine.SetModel(m.opts.Model)
		m.updateProviderInfo()
		line := stripANSI(m.statusLine.Render(100)[1])
		stats := "0.0%/200k (auto)"
		if tc.want {
			stats = "$0.000 (sub) " + stats
		}
		want := stats + strings.Repeat(" ", 100-len(stats)-len("label-test")) + "label-test"
		if line != want {
			t.Errorf("provider=%s auth=%s: footer=%q, want %q", tc.provider, tc.typeName, line, want)
		}
	}
	host.Shutdown("removed")
	m.updateProviderInfo()
	if line := stripANSI(m.statusLine.Render(100)[1]); strings.Contains(line, "(sub)") {
		t.Fatalf("removed extension retained footer label: %q", line)
	}
}
