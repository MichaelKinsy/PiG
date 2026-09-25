package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveNodeEntrypointHonoursPiExtensions pins the resolution order
// upstream uses in resolveExtensionEntries (core/extensions/loader.ts): a
// package.json "pi.extensions" list names the entry files and is consulted
// before the conventional index/main names.
//
// pig previously looked only for a root index.ts, so it could not load a pi
// package in its published form. pi-atelier declares
// "pi": {"extensions": ["./extensions/index.ts"]} and kept nothing at the root,
// which is the shape that failed.
func TestResolveNodeEntrypointHonoursPiExtensions(t *testing.T) {
	t.Run("declared entry outside the root resolves", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "package.json"),
			`{"name":"pkg","pi":{"extensions":["./extensions/index.ts"]}}`)
		want := filepath.Join(dir, "extensions", "index.ts")
		write(t, want, "export default {}\n")

		got, err := resolveNodeEntrypoint(dir)
		if err != nil {
			t.Fatalf("resolveNodeEntrypoint: %v", err)
		}
		if got != want {
			t.Errorf("entrypoint = %q, want %q", got, want)
		}
	})

	t.Run("declared entry resolves from a package.json with a byte-order mark", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "package.json"),
			"\ufeff{\"name\":\"pkg\",\"pi\":{\"extensions\":[\"./extensions/index.ts\"]}}")
		want := filepath.Join(dir, "extensions", "index.ts")
		write(t, want, "export default {}\n")
		write(t, filepath.Join(dir, "index.ts"), "export default {}\n")

		got, err := resolveNodeEntrypoint(dir)
		if err != nil {
			t.Fatalf("resolveNodeEntrypoint: %v", err)
		}
		if got != want {
			t.Errorf("entrypoint = %q, want %q", got, want)
		}
	})

	t.Run("declared entry wins over a root index", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "package.json"),
			`{"name":"pkg","pi":{"extensions":["./extensions/index.ts"]}}`)
		want := filepath.Join(dir, "extensions", "index.ts")
		write(t, want, "export default {}\n")
		write(t, filepath.Join(dir, "index.ts"), "export default {}\n")

		got, err := resolveNodeEntrypoint(dir)
		if err != nil {
			t.Fatalf("resolveNodeEntrypoint: %v", err)
		}
		if got != want {
			t.Errorf("entrypoint = %q, want the declared entry %q", got, want)
		}
	})

	t.Run("a package without a pi manifest still falls back", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "package.json"), `{"name":"pkg"}`)
		want := filepath.Join(dir, "index.ts")
		write(t, want, "export default {}\n")

		got, err := resolveNodeEntrypoint(dir)
		if err != nil {
			t.Fatalf("resolveNodeEntrypoint: %v", err)
		}
		if got != want {
			t.Errorf("entrypoint = %q, want %q", got, want)
		}
	})

	t.Run("an unbuilt declared entry names the real problem", func(t *testing.T) {
		// pi-dispatch/admin points at ./dist/index.mjs, which only exists after
		// the package is built. "no entrypoint" would misdirect the user.
		dir := t.TempDir()
		write(t, filepath.Join(dir, "package.json"),
			`{"name":"pkg","pi":{"extensions":["./dist/index.mjs"]}}`)

		_, err := resolveNodeEntrypoint(dir)
		if err == nil {
			t.Fatal("expected an error for a declared entry that does not exist")
		}
		if !strings.Contains(err.Error(), "./dist/index.mjs") ||
			!strings.Contains(err.Error(), "build the package first") {
			t.Errorf("error does not name the missing declared entry: %v", err)
		}
	})
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
