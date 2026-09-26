package subprocess_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// pinnedPiPackages is where `npm ci` in extensions/sdk-ts installs the Pi
// release named by coding.UpstreamVersion.
var pinnedPiPackages = filepath.Join("..", "..", "..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")

func readPinned(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{pinnedPiPackages}, parts...)...))
	if err != nil {
		t.Fatalf("read the pinned Pi package (run npm ci in extensions/sdk-ts): %v", err)
	}
	return data
}

// The Node runtime serves Pi's key module verbatim; a pin change must re-vendor it.
func TestVendoredPiTuiKeysMatchThePinnedPackage(t *testing.T) {
	manifest := readPinned(t, "node_modules", "@earendil-works", "pi-tui", "package.json")
	var pkg struct{ Version string }
	if err := json.Unmarshal(manifest, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != coding.UpstreamVersion {
		t.Fatalf("installed pi-tui %s, pinned Pi %s: run npm ci in extensions/sdk-ts", pkg.Version, coding.UpstreamVersion)
	}
	vendored, err := os.ReadFile(filepath.Join("runtime-node", "shims", "pi-tui-keys.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	header := "// Verbatim copy of dist/keys.js from @earendil-works/pi-tui@" + pkg.Version + " "
	if !bytes.HasPrefix(vendored, []byte(header)) {
		t.Fatalf("pi-tui-keys.mjs does not name pi-tui %s: run automation/gen/vendor-pi-tui-keys.sh", pkg.Version)
	}
	body := vendored
	for range 2 {
		body = body[bytes.IndexByte(body, '\n')+1:]
	}
	if !bytes.Equal(body, readPinned(t, "node_modules", "@earendil-works", "pi-tui", "dist", "keys.js")) {
		t.Fatal("pi-tui-keys.mjs differs from the pinned dist/keys.js: run automation/gen/vendor-pi-tui-keys.sh")
	}
}

// The bundled TypeBox must be the release the pinned Pi depends on.
func TestVendoredTypeBoxMatchesThePinnedDependency(t *testing.T) {
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(readPinned(t, "package.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "automation", "gen", "vendor-typebox.sh"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^version="([^"]+)"$`).FindSubmatch(script)
	if match == nil {
		t.Fatal("automation/gen/vendor-typebox.sh does not declare version=")
	}
	if want := manifest.Dependencies["typebox"]; string(match[1]) != want {
		t.Fatalf("vendored TypeBox %s, pinned Pi depends on typebox %s: update automation/gen/vendor-typebox.sh and rerun it", match[1], want)
	}
}

// The Node runtime serves Pi's width utilities verbatim (only the
// get-east-asian-width import is pointed at the vendored copy), so extension
// rows measure, truncate and wrap exactly as under Pi.
func TestVendoredPiTuiUtilsMatchThePinnedPackage(t *testing.T) {
	vendored, err := os.ReadFile(filepath.Join("runtime-node", "shims", "pi-tui-utils.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	header := "// Verbatim copy of dist/utils.js from @earendil-works/pi-tui@" + coding.UpstreamVersion + " "
	if !bytes.HasPrefix(vendored, []byte(header)) {
		t.Fatalf("pi-tui-utils.mjs does not name pi-tui %s: run automation/gen/vendor-pi-tui-utils.sh", coding.UpstreamVersion)
	}
	body := vendored
	for range 2 {
		body = body[bytes.IndexByte(body, '\n')+1:]
	}
	pinned := readPinned(t, "node_modules", "@earendil-works", "pi-tui", "dist", "utils.js")
	pinned = bytes.Replace(pinned, []byte(`from "get-east-asian-width";`), []byte(`from "./get-east-asian-width/index.js";`), 1)
	if !bytes.Equal(body, pinned) {
		t.Fatal("pi-tui-utils.mjs differs from the pinned dist/utils.js: run automation/gen/vendor-pi-tui-utils.sh")
	}
	for _, name := range []string{"index.js", "lookup.js", "lookup-data.js", "utilities.js", "package.json"} {
		got, err := os.ReadFile(filepath.Join("runtime-node", "shims", "get-east-asian-width", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, readPinned(t, "node_modules", "get-east-asian-width", name)) {
			t.Fatalf("vendored get-east-asian-width/%s differs from the pinned dependency: run automation/gen/vendor-pi-tui-utils.sh", name)
		}
	}
}

// The runtime's pi-tui layout components render byte-for-byte like the pinned
// package's: extensions (pi-mcp-adapter, pi-rtk-optimizer) compose panels from
// Box, Container, Text and Spacer and call their setters and clear().
func TestPiTuiLayoutComponentsMatchThePinnedPackage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	readPinned(t, "node_modules", "@earendil-works", "pi-tui", "package.json")
	pinned, err := filepath.Abs(filepath.Join(pinnedPiPackages, "node_modules", "@earendil-works", "pi-tui", "dist", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	shim, err := filepath.Abs(filepath.Join("runtime-node", "shims", "pi-tui.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	script := `const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const bg = (s) => "\x1b[44m" + s + "\x1b[49m";
function scene(m) {
  const box = new m.Box(2, 1, bg);
  box.addChild(new m.Text("Hello, \x1b[1mbold\x1b[22m wrapped text that runs past the width", 1, 0));
  box.addChild(new m.Spacer(2));
  const text = new m.Text("second", 0, 0);
  text.setCustomBgFn(bg);
  box.addChild(text);
  const out = [box.render(24)];
  box.setBgFn(undefined);
  out.push(box.render(24));
  const spacer = new m.Spacer();
  spacer.setLines(3);
  const container = new m.Container();
  container.addChild(box);
  container.addChild(spacer);
  out.push(container.render(30));
  container.clear();
  box.clear();
  out.push(container.render(30), box.render(30));
  return JSON.stringify(out);
}
const want = scene(pi), got = scene(pig);
if (want !== got) { console.log("pinned: " + want + "\npig:    " + got); process.exit(1); }`
	cmd := exec.Command(node, "--input-type=module", "--eval", script, "file://"+filepath.ToSlash(pinned), "file://"+filepath.ToSlash(shim))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pi-tui layout components differ: %v\n%s", err, output)
	}
}
