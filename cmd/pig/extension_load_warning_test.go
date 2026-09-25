// pig-specific: no upstream equivalent.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	readDone := drainPipe(r)
	fn()
	_ = w.Close()
	result := <-readDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	_ = r.Close()
	return string(result.data)
}

// An unresolvable extension directory is carried as a load failure instead of
// a stderr warning, so startup reports it as upstream loadExtensions does.
func TestPathToExtConfigInvalidSourceIsActionable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extension.go"), []byte("package broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var configs []subprocess.ExtConfig
	stderr := captureStderr(t, func() { configs = pathToExtConfigs(dir) })
	if stderr != "" {
		t.Fatalf("unresolvable extension printed %q", stderr)
	}
	if _, ok := pathToExtConfig(dir); ok {
		t.Fatalf("pathToExtConfig(%q) accepted source with no conventional factory or standalone", dir)
	}
	if len(configs) != 1 || configs[0].ResolveError() == nil {
		t.Fatalf("configs = %#v, want one unresolved config", configs)
	}
	message := extensionLoadDiagnostics([]error{loadErrorFor(t, configs)})[0].Message
	for _, want := range []string{`Failed to load extension "` + dir + `": Failed to load extension: `, "func Extension() *sdk.Extension", "package main"} {
		if !strings.Contains(message, want) {
			t.Errorf("diagnostic %q does not contain %q", message, want)
		}
	}
}

func TestPathToExtConfigPlainExplicitDirectoryIsLoadFailure(t *testing.T) {
	dir := t.TempDir()
	configs := pathToExtConfigs(dir)
	if len(configs) != 1 || configs[0].ResolveError() == nil || !strings.Contains(configs[0].ResolveError().Error(), "no recognized factory or standalone source") {
		t.Fatalf("plain explicit directory configs = %#v", configs)
	}
}

// loadErrorFor runs configs through a host and returns its single load error.
func loadErrorFor(t *testing.T, configs []subprocess.ExtConfig) error {
	t.Helper()
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(loaded) != 0 || len(errs) != 1 {
		t.Fatalf("LoadAll = %v, %v; want one error", loaded, errs)
	}
	return errs[0]
}

func TestPathToExtConfigConventionalFactoryLoadsSilently(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "goodext")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/goodext\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "package goodext\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\"goodext\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var ok bool
	stderr := captureStderr(t, func() { _, ok = pathToExtConfig(dir) })
	if !ok || stderr != "" {
		t.Fatalf("factory result ok=%t stderr=%q", ok, stderr)
	}
}
