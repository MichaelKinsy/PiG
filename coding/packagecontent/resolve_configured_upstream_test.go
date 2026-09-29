package packagecontent_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

// The error-bearing API is additive: the original function remains assignable
// to the published Go signature, while production startup checks errors.
func TestResolveConfiguredPublicSignature(t *testing.T) {
	var resolve func([]string, string, packagecontent.Kind) []string = packagecontent.ResolveConfigured
	base := t.TempDir()
	path := filepath.Join(base, "prompt.md")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolve([]string{"prompt.md"}, base, packagecontent.Prompts); !reflect.DeepEqual(got, []string{path}) {
		t.Fatalf("ResolveConfigured = %q, want %q", got, path)
	}
	if got := resolve([]string{"file:///a%2Fb"}, base, packagecontent.Prompts); got != nil {
		t.Fatalf("invalid URL returned %q", got)
	}
}

// package-manager.ts:221-223,2145-2151 prefers HOME over homedir() on Windows.
func TestConfiguredPathsUsePackageHome(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", t.TempDir())
	path := filepath.Join(home, "prompt.md")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := packagecontent.ResolveConfiguredWithError([]string{" ~/prompt.md "}, base, packagecontent.Prompts)
	if err != nil || !reflect.DeepEqual(got, []string{path}) {
		t.Fatalf("ResolveConfiguredWithError = %q, %v; want %q", got, err, path)
	}
}
