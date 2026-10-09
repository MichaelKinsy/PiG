package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A Package's conventional entry ./src/index.ts names its extension "src", so
// two unrelated Packages routinely share one extension name. The authentication
// inventory must keep them apart: a login routes to the extension that
// declared the provider, not to the last extension of that name.
const antigravityShapedExtension = `import * as piAiCompat from "@earendil-works/pi-ai/compat";

export default function (pi: any): void {
  const register = (piAiCompat as any).registerApiProvider;
  if (typeof register === "function") {
    const stream = (): never => { throw new Error("not streamed in this test"); };
    register({ api: "antigravity", stream, streamSimple: stream });
  }
  pi.registerProvider("antigravity", {
    name: "Antigravity",
    baseUrl: "https://example.invalid",
    api: "antigravity",
    models: [],
    refreshModels: async () => [],
    oauth: {
      name: "Antigravity",
      login: async () => ({ refresh: "r", access: "a", expires: Date.now() + 3600_000 }),
      refreshToken: async (credentials: any) => credentials,
      getApiKey: (credentials: any) => credentials.access,
    },
    streamSimple: () => { throw new Error("not streamed in this test"); },
  });
}
`

const otherSrcExtension = `export default function (pi: any): void {
  pi.registerCommand("other-hello", { description: "other", handler: async () => {} });
}
`

func writeSrcIndexPackage(t *testing.T, root, name, source string) string {
	t.Helper()
	pkg := filepath.Join(root, name)
	writeStartupFixtureFile(t, filepath.Join(pkg, "package.json"), `{"name":"`+name+`","type":"module","pi":{"extensions":["./src/index.ts"]}}`)
	writeStartupFixtureFile(t, filepath.Join(pkg, "src", "index.ts"), source)
	return pkg
}

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

			registry, err := discoverAuthContributions()
			if err != nil {
				t.Fatal(err)
			}
			defer registry.close()
			if _, declared := registry.target("antigravity"); !declared {
				t.Fatalf("inspection did not find the provider: targets=%v diagnostics=%v", registry.targets, registry.diagnostics)
			}
			provider, err := authProviderFromContribution("antigravity", &registry)
			if err != nil {
				t.Fatalf("login did not reach the declaring extension: %v", err)
			}
			if provider.ID() != "antigravity" || provider.Name() != "Antigravity" {
				t.Fatalf("provider = %q %q", provider.ID(), provider.Name())
			}
		})
	}
}
