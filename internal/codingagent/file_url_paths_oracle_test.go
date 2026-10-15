package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's resolvePath/normalizePath (utils/paths.js) against ExtensionSessionDir and resolveExportPath: a leading ~ and a file:// URL (through Node's fileURLToPath, which rejects a remote host, encoded slashes and relative forms) become paths; any other input stays as given.
func TestFileURLPathInputsMatchPiOracle(t *testing.T) {
	inputs := []string{
		"", "/abs/out.html", "rel/out.html", "~", "~/x", "~/", "~user/x", "file:///abs/out.html", "file:///abs/a%20b.html", "file:///abs/a%2Fb.html", "file:///abs/a%5Cb.html", "file:///abs/%E2%9C%93.html", "file:///abs/%zz", "file:///abs/a b",
		"file://localhost/abs/x", "file://remote/abs/x", "file://", "file:///", "file:///abs/../x", "file:///abs/./x", "file:///abs//x", "file:///abs/x?q=1#f", "file:///abs/x/", "FILE:///abs/x", "file:/abs/x", "file:abs/x", "file:///C:/x", "file:///c%3A/x", "file:///abs/\u00e9.html",
		"file://[::1]/x", "file://127.0.0.1/x", "file://LOCALHOST/x", "file:///abs/%00x", "file://%2Fabs/x", "https://example.com/x", "x:file:///abs", " file:///abs/x", "./out.html", "../out.html", "out dir/out.html", "@file", "C:\\x", "\\\\server\\share",
	}
	type result struct {
		Path  string `json:"path"`
		Error bool   `json:"error"`
	}
	var want struct {
		Resolve   []result `json:"resolve"`
		Normalize []result `json:"normalize"`
	}
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/utils/paths.js");
const run = (fn) => { try { return { path: fn() }; } catch { return { error: true }; } };
emit({ resolve: input.map((i) => run(() => mod.resolvePath(i, "/work/base"))), normalize: input.map((i) => run(() => mod.normalizePath(i))) });`, inputs, &want, "HOME=/home/oracle", "USERPROFILE=/home/oracle")
	t.Setenv("HOME", "/home/oracle")
	t.Setenv("USERPROFILE", "/home/oracle")
	for i, input := range inputs {
		got, err := resolveExportPath(input, "/work/base")
		if (err != nil) != want.Resolve[i].Error || (err == nil && got != want.Resolve[i].Path) {
			t.Errorf("resolveExportPath(%q): Go %q err=%v, Pi %+v", input, got, err, want.Resolve[i])
		}
		// ExtensionSessionDir keeps the input when normalizePath would throw, and "" for the default.
		wantDir := want.Normalize[i].Path
		if want.Normalize[i].Error {
			wantDir = ExpandTildePath(input)
		}
		if dir := ExtensionSessionDir(input); dir != wantDir {
			t.Errorf("ExtensionSessionDir(%q) = %q, Pi normalizePath %+v", input, dir, want.Normalize[i])
		}
	}
}
