//go:build !pig_strip_node_extensions

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestAuthLoginRoutesToTheExtensionThatDeclaredTheProviderWhenNamesCollide(t *testing.T) {
	for name, order := range map[string][]string{"provider first": {"a-provider", "b-other"}, "provider last": {"b-other", "a-provider"}} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			cwd, agentDir := filepath.Join(home, "work"), filepath.Join(home, "agent")
			for _, dir := range []string{cwd, agentDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HOME", home)
			t.Setenv("PIG_HOME", filepath.Join(home, "pig"))
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PI_CODING_AGENT_DIR", agentDir)
			t.Cleanup(ai.ResetOAuthProviders)
			t.Chdir(cwd)
			sources := map[string]string{"a-provider": antigravityShapedExtension, "b-other": otherSrcExtension}
			var packages []codingagent.PackageSource
			for _, name := range order {
				packages = append(packages, codingagent.PackageSource{Source: writeSrcIndexPackage(t, filepath.Join(home, "packages"), name, sources[name])})
			}
			if err := codingagent.NewSettingsManager(cwd, agentDir).SetPackages(packages); err != nil {
				t.Fatal(err)
			}

			registry, err := discoverAuthContributions(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer registry.close()
			if _, declared := registry.target("antigravity"); !declared {
				t.Fatalf("inspection did not find the provider: targets=%v diagnostics=%v", registry.targets, registry.diagnostics)
			}
			provider, err := authProviderFromContribution(t.Context(), "antigravity", &registry)
			if err != nil {
				t.Fatalf("login did not reach the declaring extension: %v", err)
			}
			if provider.ID() != "antigravity" || provider.Name() != "Antigravity" {
				t.Fatalf("provider = %q %q", provider.ID(), provider.Name())
			}
		})
	}
}
