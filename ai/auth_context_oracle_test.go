package ai

// pi: packages/ai/src/auth/context.ts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestDefaultProviderAuthContextMatchesPi runs Pi's defaultProviderAuthContext under Node and compares its env and fileExists answers with DefaultProviderAuthContext for the same environment and files.
func TestDefaultProviderAuthContextMatchesPi(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "token"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	// "~sib" is not "~/sib": Pi joins homedir() and the rest of the path with no separator, so it names a sibling of the home directory.
	if err := os.WriteFile(home+"sib", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(home + "sib") })
	env := map[string]string{
		"PIG_CTX_SET":   "  value \n",
		"PIG_CTX_BLANK": " \t\u00a0\ufeff\n",
		"PIG_CTX_EMPTY": "",
	}
	names := []string{"PIG_CTX_SET", "PIG_CTX_BLANK", "PIG_CTX_EMPTY", "PIG_CTX_UNSET_ENTIRELY"}
	paths := []string{filepath.Join(home, "token"), "~/token", "~/dir", "~/missing", filepath.Join(home, "missing"), "token", "~sib", "~nosuch"}
	type answer struct {
		Env    map[string]*string `json:"env"`
		Exists map[string]bool    `json:"exists"`
	}
	var want answer
	input := map[string]any{"names": names, "paths": paths}
	var childEnv []string
	for k, v := range env {
		childEnv = append(childEnv, k+"="+v)
	}
	childEnv = append(childEnv, "HOME="+home, "USERPROFILE="+home)
	pioracle.Run(t, `
const { defaultProviderAuthContext } = await load("pi-ai/auth/context.js");
const ctx = defaultProviderAuthContext();
const out = { env: {}, exists: {} };
for (const name of input.names) out.env[name] = (await ctx.env(name)) ?? null;
for (const path of input.paths) out.exists[path] = await ctx.fileExists(path);
emit(out);`, input, &want, childEnv...)

	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ctx := DefaultProviderAuthContext()
	for _, name := range names {
		got, ok := ctx.Env(name)
		piVal := want.Env[name]
		if ok != (piVal != nil) || (ok && got != *piVal) {
			t.Errorf("Env(%q) = %q, %v; Pi %v", name, got, ok, piVal)
		}
	}
	for _, path := range paths {
		if got := ctx.FileExists(path); got != want.Exists[path] {
			t.Errorf("FileExists(%q) = %v; Pi %v", path, got, want.Exists[path])
		}
	}
}
