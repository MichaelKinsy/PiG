package subprocess

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Pi's regression #8092 (test/suite/regressions/8092-extension-realpath-jiti.test.ts, Pi e6865ec7e): an extension entry is realpath'd before jiti imports it,
// so an extension whose declared dependency is only reachable beside it in the pnpm virtual store loads through the symlink pnpm puts in node_modules.
func TestExtensionEntryIsRealpathedBeforeTheNodeImport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture links a directory; Pi's test uses a junction on Windows, which os.Symlink does not create")
	}
	for _, isolation := range []string{"shared-ok", "isolated"} {
		t.Run(isolation, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			marker := filepath.Join(root, "dep-value")
			// pnpm isolated layout: node_modules/<pkg> is a symlink into the virtual store, and declared deps exist only as store siblings.
			storePkgDir := filepath.Join(root, "node_modules", ".pnpm", "@test+ext@1.0.0", "node_modules", "@test")
			write(t, filepath.Join(storePkgDir, "dep", "index.js"), "export const value = 42;\n")
			write(t, filepath.Join(storePkgDir, "ext", "index.ts"), strings.Join([]string{
				`import { appendFileSync } from "node:fs";`,
				`import { value } from "@test/dep";`,
				`appendFileSync(` + quoteJS(marker) + `, String(value));`,
				`export default function () {}`,
				``,
			}, "\n"))
			topLevel := filepath.Join(root, "node_modules", "@test")
			if err := os.MkdirAll(topLevel, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(storePkgDir, "ext"), filepath.Join(topLevel, "ext")); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(topLevel, "ext", "index.ts")

			config := ExtConfig{Name: "realpath", Source: entry, Enabled: true, Isolation: isolation}
			host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			host.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
			t.Cleanup(func() { host.Shutdown("test") })
			loaded, errs := host.LoadAll(t.Context(), []ExtConfig{config})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("loaded=%v errors=%v", loaded, errs)
			}
			got, err := os.ReadFile(marker)
			if err != nil || string(got) != "42" {
				t.Fatalf("dependency value = %q, %v; want 42", got, err)
			}
		})
	}
}
