package subprocess

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The jiti/static and node:sea option assertions in the upstream cases are Node-binary internals. This tests their portable contract through the compiled Go host: runtime modules stay unloaded until an extension is selected, and the selected module resolves the bundled Pi/TypeBox imports.
// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8237-node-sea-extension-loading.test.ts:51
// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9540-extension-loader-lazy.test.ts:38
func TestUpstreamExtensionLoaderDefersRuntimeUntilSelection(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "loaded")
	entry := filepath.Join(root, "extension.ts")
	write(t, entry, `import { appendFileSync } from "node:fs";
import { Type } from "typebox";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
appendFileSync(`+strconv.Quote(marker)+`, "module\n");
export default function(pi) {
 if (Type.Object({}).type !== "object" || typeof getAgentDir !== "function") throw new Error("bundled imports unavailable");
 appendFileSync(`+strconv.Quote(marker)+`, "factory\n");
 pi.registerCommand("loaded", { handler: async () => {} });
}`)
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadAll(t.Context(), nil)
	if len(loaded) != 0 || len(errs) != 0 || host.ExtensionCount() != 0 {
		t.Fatalf("empty load = %#v, %v", loaded, errs)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("module executed before selection: %v", err)
	}
	ext, err := host.Load(t.Context(), ExtConfig{Name: "extension", Source: entry, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ext.Commands["loaded"]; !ok {
		t.Fatal("selected factory did not register")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "module\nfactory\n" {
		t.Fatalf("module/factory calls = %q", data)
	}
}
