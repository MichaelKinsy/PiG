package cli

import (
	"path/filepath"
	"testing"
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
