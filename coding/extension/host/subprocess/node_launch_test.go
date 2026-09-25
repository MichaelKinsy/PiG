package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Spawning a Node extension runs node directly, with exactly the arguments the
// published launcher script would exec, instead of a shell that forks a
// dirname process before exec'ing node. Node resolves an --import value as a
// module specifier, so the loader is named by the URL Node's pathToFileURL
// returns; a bare Windows path fails with ERR_UNSUPPORTED_ESM_URL_SCHEME. The
// cache root contains a space, as CI's TEMP does, so the URL is percent-encoded.
func TestNodeLauncherSpawnsNodeDirectlyWithLauncherArguments(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "cache root with spaces")
	src := filepath.Join(t.TempDir(), "direct.mjs")
	if err := os.WriteFile(src, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewBuilderWithConfigRoot(root, root).Build("direct", src)
	if err != nil {
		t.Fatal(err)
	}

	cmd := buildExtCommand(context.Background(), result.BinaryPath, "")
	if cmd.Path != node {
		t.Fatalf("spawned %s, want node (%s) without the launcher shell", cmd.Path, node)
	}
	runtimeDir := result.BinaryPath + ".runtime"
	loaderURL := nodePathToFileURLs(t, node, filepath.Join(runtimeDir, "register-loader.mjs"))[0]
	want := []string{"--import", loaderURL, filepath.Join(runtimeDir, "cli.mjs"), src}
	if !slices.Equal(cmd.Args[1:], want) {
		t.Fatalf("direct node args = %q, want %q", cmd.Args[1:], want)
	}

	// The same --import value must preload the loader: the pi-ai alias
	// resolves only through the registered hooks.
	probe := exec.Command(node, cmd.Args[1], cmd.Args[2],
		"--input-type=module", "--eval", `import { Type } from "@earendil-works/pi-ai"; if (typeof Type.Object !== "function") process.exit(1);`)
	if output, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("node %s %s: %v\n%s", cmd.Args[1], cmd.Args[2], err, output)
	}
}

// nodeFileURL must produce the href Node's url.pathToFileURL produces for the
// same path, so every --import value names the file Node would name.
func TestNodeFileURLMatchesNodePathToFileURL(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	base := filepath.Join(t.TempDir(), "dir with spaces")
	paths := []string{
		filepath.Join(base, "register-loader.mjs"),
		filepath.Join(base, "é 日本", "x.mjs"),
		filepath.Join(base, "a#b%c[d]e^f~g{h}i`j!k$l&m'n(o)p+q,r;s=t@u", "x.mjs"),
		filepath.Join(base, "a", "..", "b", ".", "c.mjs"),
		base + string(filepath.Separator),
		"relative dir" + string(filepath.Separator) + "x.mjs",
	}
	if runtime.GOOS != "windows" {
		paths = append(paths, filepath.Join(base, `quote"less<greater>question?pipe|back\slash`, "x.mjs"), filepath.Join(base, "tab\tnewline\ncr\rdel\x7f"))
	}
	want := nodePathToFileURLs(t, node, paths...)
	for i, path := range paths {
		got, err := nodeFileURL(path)
		if err != nil {
			t.Fatalf("nodeFileURL(%q): %v", path, err)
		}
		if got != want[i] {
			t.Errorf("nodeFileURL(%q) = %q, want Node's %q", path, got, want[i])
		}
	}
}

// Windows forms that cannot be created as test files on every host: drive
// letters, UNC shares, extended-length prefixes, and characters Windows file
// names reject. Expected values are Node 24.19.0 url.pathToFileURL output with
// { windows: true } for the same input.
func TestWindowsFileURLFromAbsoluteMatchesNode(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`C:\a b\x.mjs`, "file:///C:/a%20b/x.mjs"},
		{`c:\lower`, "file:///c:/lower"},
		{`C:\dir\`, "file:///C:/dir/"},
		{`C:\é\日本`, "file:///C:/%C3%A9/%E6%97%A5%E6%9C%AC"},
		{"C:\\a\tb\nc\rd\x01e\x7ff", "file:///C:/a%09b%0Ac%0Dd%01e%7Ff"},
		{`C:\q"l<g>q?p|s*c:`, "file:///C:/q%22l%3Cg%3Eq%3Fp%7Cs*c:"},
		{`\\server\share\x y`, "file://server/share/x%20y"},
		{`\\SERVER\Share\X`, "file://server/Share/X"},
		{`\\?\UNC\server\share\f`, "file://server/share/f"},
		{`\\?\C:\long\p`, "file:///C:/long/p"},
		{`\\münich\share\x y`, "file://xn--mnich-kva/share/x%20y"},
		{`\\MÜNICH\share\x`, "file://xn--mnich-kva/share/x"},
		{`\\Bücher.example\s\x`, "file://xn--bcher-kva.example/s/x"},
		{`\\straße\s\x`, "file://xn--strae-oqa/s/x"},
		{`\\日本\s\x`, "file://xn--wgv71a/s/x"},
		{`\\ﬀ\s\x`, "file://ff/s/x"},
		{`\\-lead\s\x`, "file://-lead/s/x"},
		{`\\localhost\share\x`, "file:///share/x"},
		{`\\LOCALHOST\share\x`, "file:///share/x"},
		{`\\127.0.0.1\s\x`, "file://127.0.0.1/s/x"},
		{`\\[::1]\s\x`, "file://[::1]/s/x"},
	} {
		got, err := fileURLFromAbs(tc.path, true)
		if err != nil {
			t.Errorf("fileURLFromAbs(%q): %v", tc.path, err)
			continue
		}
		if got != tc.want {
			t.Errorf("fileURLFromAbs(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
	for _, bad := range []string{`\\server`, `\\\share\x`, `\\a b\s\x`, `\\a%b\s\x`, "\\\\\u200d\\s\\x"} {
		if got, err := fileURLFromAbs(bad, true); err == nil {
			t.Errorf("fileURLFromAbs(%q) = %q, want Node's invalid UNC path error", bad, got)
		}
	}
}

// UNC server names pass through the WHATWG host parser in Node's
// pathToFileURL: IDNA, localhost, IPv4 and IPv6 literals, and forbidden code
// points. Every name here must give Node's href, or fail where Node throws.
func TestUNCFileURLHostsMatchNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	servers := []string{
		"server", "SERVER", "münich", "MÜNICH", "münich.", "Bücher.example", "xn--mnich-kva", "straße", "faß.de", "日本", "ﬀ",
		"ab--cd", "-lead", "trail-", "a..b", "a.", "localhost", "LocalHost", "localhost.",
		"127.0.0.1", "0x7f.1", "0177.0.0.1", "1.2.3", "4294967295", "4294967296", "256.1.1.1", "1.2.3.4.5", "08.1.1.1", "1.2.3.", "example.1",
		"[::1]", "[0:0:0:0:0:0:0:1]", "[1:0:0:2:0:0:0:3]", "[::ffff:1.2.3.4]", "[fe80::1%eth0]", "[::1",
		"a b", "a%b", "a%41b", "m%C3%BCnich", "a%2Eb", "a#b", "a?b", "a@b", "a:b", "a^b", "a|b", "\u200d", "a\u200db", "\u0661\u0662a",
		"#b", "?b", "a?b#c", "münich#x", "a b#c", "localhost#x", "1.2#x",
	}
	paths := make([]string, len(servers))
	for i, server := range servers {
		paths[i] = `\\` + server + `\share\x`
	}
	input, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--input-type=module", "--eval",
		`import { pathToFileURL } from "node:url"; import { readFileSync } from "node:fs"; process.stdout.write(JSON.stringify(JSON.parse(readFileSync(0, "utf8")).map((p) => { try { return pathToFileURL(p, { windows: true }).href; } catch { return null; } })));`)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node pathToFileURL: %v", err)
	}
	var want []*string
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(paths) {
		t.Fatalf("node pathToFileURL output %q: %v", output, err)
	}
	for i, path := range paths {
		got, err := fileURLFromAbs(path, true)
		switch {
		case want[i] == nil && err == nil:
			t.Errorf("fileURLFromAbs(%q) = %q, but Node throws", path, got)
		case want[i] != nil && err != nil:
			t.Errorf("fileURLFromAbs(%q): %v, want Node's %q", path, err, *want[i])
		case want[i] != nil && got != *want[i]:
			t.Errorf("fileURLFromAbs(%q) = %q, want Node's %q", path, got, *want[i])
		}
	}
}

// nodePathToFileURLs returns url.pathToFileURL(path).href for each path, as
// computed by the node binary under test.
func nodePathToFileURLs(t *testing.T, node string, paths ...string) []string {
	t.Helper()
	input, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--input-type=module", "--eval",
		`import { pathToFileURL } from "node:url"; import { readFileSync } from "node:fs"; process.stdout.write(JSON.stringify(JSON.parse(readFileSync(0, "utf8")).map((p) => pathToFileURL(p).href)));`)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node pathToFileURL: %v", err)
	}
	var hrefs []string
	if err := json.Unmarshal(output, &hrefs); err != nil || len(hrefs) != len(paths) {
		t.Fatalf("node pathToFileURL output %q: %v", output, err)
	}
	return hrefs
}

func TestNodeLauncherArtifactDoesNotRequirePOSIXShell(t *testing.T) {
	src := filepath.Join(t.TempDir(), "portable.mjs")
	if err := os.WriteFile(src, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewBuilderWithConfigRoot(t.TempDir(), t.TempDir()).Build("portable", src)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(result.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(artifact), "#!/bin/sh") {
		t.Fatalf("node launcher still requires /bin/sh:\n%s", artifact)
	}
}

// An artifact without a recorded Node entry is still executed as-is.
func TestBuildExtCommandRunsOtherArtifactsAsIs(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if cmd := buildExtCommand(context.Background(), bin, "go"); cmd.Path != bin {
		t.Fatalf("spawned %s, want %s", cmd.Path, bin)
	}
}
