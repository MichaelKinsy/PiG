package cli

import (
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func fileURLOf(path string) string {
	p := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func resolvePathCases(t *testing.T, base string) (cases []struct{ name, input, want string }) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	spaced := filepath.Join(base, "extra dir", "one")
	return []struct{ name, input, want string }{
		{"trim and relative", "  rel/dir \n", filepath.Join(base, "rel", "dir")},
		{"tilde", "~/probe", filepath.Join(home, "probe")},
		{"file URL with percent-encoding", fileURLOf(spaced), spaced},
		{"absolute", spaced, spaced},
	}
}

// paths.ts:75-106 resolvePath(p, cwd, { trim: true }), applied by
// resource-loader.ts:850-867 to --skill, --prompt-template and --theme paths.
func TestResolveSettingsPathFollowsResolvePath(t *testing.T) {
	base := t.TempDir()
	for _, tc := range resolvePathCases(t, base) {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSettingsPath(base, tc.input); got != tc.want {
				t.Fatalf("resolveSettingsPath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// package-manager.ts:2145-2151 resolvePath/resolvePathFromBase use { trim: true };
// paths.ts:50-64 isLocalPath makes file: sources local, so `pi -e file:///x` loads.
func TestLocalPackageSourcesFollowResolvePath(t *testing.T) {
	base := t.TempDir()
	for _, tc := range resolvePathCases(t, base) {
		t.Run(tc.name, func(t *testing.T) {
			got, err := packagemanager.ResolveLocalPackageRoot(base, tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("resolveLocalPackageRoot(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
			}
		})
	}
	for _, source := range []string{fileURLOf(filepath.Join(base, "ext.ts")), "  " + fileURLOf(filepath.Join(base, "ext.ts")) + " "} {
		if kind := packagemanager.DetectSourceKind(source); kind != "local" {
			t.Errorf("detectSourceKind(%q) = %q, want local", source, kind)
		}
	}
}

// package-manager.ts resolveLocalEntries: settings resource arrays resolve each
// entry with resolvePath({ trim: true, homeDir }), so ~ and file: entries load.
func TestConfiguredResourceEntriesFollowResolvePath(t *testing.T) {
	base := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	files := map[string]string{
		"rel.ts":     filepath.Join(base, "rel.ts"),
		"~/tilde.ts": filepath.Join(home, "tilde.ts"),
		fileURLOf(filepath.Join(base, "url dir", "u.ts")): filepath.Join(base, "url dir", "u.ts"),
	}
	for _, path := range files {
		writePackageResource(t, path, "export default function() {}")
	}
	got, err := packagecontent.ResolveConfiguredWithError([]string{" rel.ts ", "~/tilde.ts", fileURLOf(filepath.Join(base, "url dir", "u.ts"))}, base, packagecontent.Extensions)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range files {
		if !slices.Contains(got, want) {
			t.Errorf("resolved entries %q lack %q", got, want)
		}
	}
}

// package-manager.ts:1375-1393,1690-1700: a file: source keys as
// local:<resolved path>, so adding the same package by path and by URL keeps one
// entry and removing by URL removes it.
func TestFileURLPackageSourceSharesLocalIdentity(t *testing.T) {
	cwd := t.TempDir()
	sm := codingagent.NewSettingsManager(cwd, filepath.Join(cwd, "agent"))
	pkg := filepath.Join(cwd, "local-pkg")
	writePackageResource(t, filepath.Join(pkg, "extensions", "index.ts"), "export default function() {}")
	if a, b := packagemanager.PackageSourceIdentity(cwd, pkg), packagemanager.PackageSourceIdentity(cwd, fileURLOf(pkg)); a != b {
		t.Fatalf("identity by path %q != by URL %q", a, b)
	}
	if _, err := packageManagerFor(cwd, sm, nil).AddSourceToSettings(pkg, false); err != nil {
		t.Fatal(err)
	}
	if _, err := packageManagerFor(cwd, sm, nil).AddSourceToSettings(fileURLOf(pkg), false); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetGlobalSettings().Packages; len(got) != 1 {
		t.Fatalf("packages = %+v, want one entry", got)
	}
	removed, err := packageManagerFor(cwd, sm, nil).RemoveSourceFromSettings(fileURLOf(pkg), false)
	if err != nil || !removed || len(sm.GetGlobalSettings().Packages) != 0 {
		t.Fatalf("removed=%t err=%v packages=%+v", removed, err, sm.GetGlobalSettings().Packages)
	}
}

// main.ts:548-550 resolves CLI resource paths with resolvePath(value, cwd) and
// no trim; an invalid file: URL makes fileURLToPath throw and startup fail.
func TestResolveCLIResourceFlagsFollowsMain(t *testing.T) {
	cwd := t.TempDir()
	flags, err := resolveCLIResourceFlags(Args{Skills: []string{" ./rel "}, Extensions: []string{fileURLOf(filepath.Join(cwd, "e.ts")), "npm:pkg"}}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, " ./rel "); flags.Skills[0] != want && flags.Skills[0] != filepath.Clean(want) {
		t.Errorf("untrimmed --skill = %q, want %q", flags.Skills[0], want)
	}
	if flags.Extensions[0] != filepath.Join(cwd, "e.ts") || flags.Extensions[1] != "npm:pkg" {
		t.Errorf("extensions = %q", flags.Extensions)
	}
	for name, in := range map[string]Args{
		"skill": {Skills: []string{"file:///a%2Fb"}}, "prompt": {PromptTemplates: []string{"file:///a%2Fb"}},
		"theme": {Themes: []string{"file:///a%2Fb"}}, "extension": {Extensions: []string{"file:///a%2Fb"}},
	} {
		if _, err := resolveCLIResourceFlags(in, cwd); err == nil {
			t.Errorf("%s: invalid file URL was accepted", name)
		}
	}
}
