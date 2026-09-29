package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const configPiPackage = "@earendil-works/pi-coding-agent"
const configOldPiPackage = "@mariozechner/pi-coding-agent"

type configCommandWire struct {
	Command string               `json:"command"`
	Args    []string             `json:"args"`
	Display string               `json:"display"`
	Steps   []*configCommandWire `json:"steps,omitempty"`
}

func emitConfigCommand(t *testing.T, name string, command *SelfUpdateCommand, prefix string, spaced bool) {
	t.Helper()
	token := "PREFIX"
	if spaced {
		token = "PREFIX SPACE"
	}
	var project func(*SelfUpdateCommand) *configCommandWire
	project = func(command *SelfUpdateCommand) *configCommandWire {
		if command == nil {
			return nil
		}
		wire := &configCommandWire{Command: command.Command, Args: append([]string{}, command.Args...), Display: command.Display}
		if prefix != "" {
			for i, arg := range wire.Args {
				if arg == prefix {
					wire.Args[i] = token
				}
			}
			wire.Display = strings.ReplaceAll(wire.Display, prefix, token)
		}
		for _, step := range command.Steps {
			wire.Steps = append(wire.Steps, project(step))
		}
		return wire
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(struct {
		Case    string             `json:"case"`
		Command *configCommandWire `json:"command"`
	}{name, project(command)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("CONFIG_COMMAND %s", data.String())
}

func configNativeInstall(t *testing.T, owner PackageManagerOwner, packageName string, spaced bool) (*SelfUpdateProvenance, string, string) {
	t.Helper()
	t.Setenv("PIG_INSTALL_TIER", "")
	prefix := t.TempDir()
	if spaced {
		prefix = filepath.Join(prefix, "pi prefix ")
	}
	root := filepath.Join(prefix, "lib", "node_modules")
	outputs := map[string]string{}
	switch owner {
	case ownerNPM:
		outputs["npm root -g"] = root
	case ownerPNPM:
		root = filepath.Join(prefix, "pnpm", "global", "5", "node_modules")
		outputs["pnpm root -g"] = root
	case ownerYarn:
		global := filepath.Join(prefix, "yarn", "global")
		root = filepath.Join(global, "node_modules")
		outputs["yarn global dir"] = global
	case ownerBun:
		bin := filepath.Join(prefix, ".bun", "bin")
		root = filepath.Join(prefix, ".bun", "install", "global", "node_modules")
		outputs["bun pm bin -g"] = bin
	}
	pkg := filepath.Join(root, filepath.FromSlash(packageName))
	dir := filepath.Join(pkg, "dist")
	if err := mkdirAllLikeNode(dir); err != nil {
		t.Fatal(err)
	}
	exe := writeFakeExe(t, dir)
	// D39 replaces Node execPath/packageDir heuristics with the actual native executable bound to a manager-reported root. Command construction itself is platform-neutral; the mutation tier separately enforces Windows owner restrictions.
	provenance, ambiguous := detectPackageManagerOwnership(runtime.GOOS, exe, fakeCmdRunner{outputs: outputs})
	if ambiguous || provenance == nil || provenance.PackageOwner != owner || provenance.PackageName != packageName {
		t.Fatalf("ownership=%+v ambiguous=%t", provenance, ambiguous)
	}
	return provenance, prefix, pkg
}

// mkdirAllLikeNode creates path as Node's fs.mkdirSync(path, {recursive: true}) does. On Windows Node passes
// the \\?\ form of an absolute path to the OS, so a component that ends in a space, like Pi's "pi prefix "
// fixture (config.test.ts:289), keeps it; the plain Win32 form would drop the space from each component it creates.
func mkdirAllLikeNode(path string) error {
	if runtime.GOOS == "windows" && filepath.IsAbs(path) && !strings.HasPrefix(path, `\\?\`) {
		path = `\\?\` + path
	}
	return os.MkdirAll(path, 0o755)
}

// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:150
func TestConfigPackageRootSkipsCopiedDistMetadataOriginal(t *testing.T) {
	provenance, _, pkg := configNativeInstall(t, ownerNPM, configPiPackage, false)
	for _, path := range []string{filepath.Join(pkg, "package.json"), filepath.Join(pkg, "dist", "package.json")} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bundle := filepath.Join(pkg, "dist", "bundle")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := writeFakeExe(t, bundle)
	// The native owner root is the behavioral counterpart of Node's module-root lookup; embedded Go assets do not walk package.json files.
	got, err := resolveTierForExe(t, executable, fakeCmdRunner{outputs: map[string]string{"npm root -g": filepath.Join(provenance.NpmPrefix, "lib", "node_modules")}})
	// The executable is resolved through its symlinks, as Node resolves the CLI module path, so macOS /var reads back as /private/var.
	if err != nil || got.PackageDir != realPathForTest(t, pkg) {
		t.Fatalf("package root=%+v err=%v want=%s", got, err, pkg)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:163
func TestConfigWindowsPnpmPathOriginal(t *testing.T) {
	executable := `C:\Users\Admin\Documents\pnpm-repository\global\5\.pnpm\@earendil-works+pi-coding-agent@0.67.68\node_modules\@earendil-works\pi-coding-agent\dist\cli.js`
	provenance, ambiguous := detectPackageManagerOwnership("windows", executable, fakeCmdRunner{outputs: map[string]string{"pnpm root -g": `C:\Users\Admin\Documents\pnpm-repository\global\5`}})
	if ambiguous || provenance == nil || provenance.PackageOwner != ownerPNPM {
		t.Fatalf("ownership=%+v ambiguous=%t", provenance, ambiguous)
	}
	command := provenance.GetSelfUpdateCommand(nil, SelfUpdatePackageTarget{PackageName: configPiPackage})
	if command == nil || "Run: "+command.Display != "Run: pnpm install -g --ignore-scripts --config.minimumReleaseAge=0 @earendil-works/pi-coding-agent" {
		t.Fatalf("command=%+v", command)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:174
func TestConfigUnknownWrapperCannotSelfUpdateOriginal(t *testing.T) {
	t.Setenv("PIG_INSTALL_TIER", "")
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "")
	provenance, err := resolveTierForExe(t, "/usr/local/bin/node", fakeCmdRunner{})
	if err != nil || provenance.Tier != TierUnsupported {
		t.Fatalf("unknown ownership=%+v err=%v", provenance, err)
	}
	if command := provenance.GetSelfUpdateCommand(nil, SelfUpdatePackageTarget{PackageName: configPiPackage}); command != nil {
		t.Fatalf("unknown wrapper update=%+v", command)
	}
	// D39 adds the native executable and concrete reinstall guidance rather than Pi's Node-package instruction. The refusal and no-mutation outcome are unchanged.
	want := UnsupportedRemediation("/usr/local/bin/node")
	if got := provenance.GetSelfUpdateUnavailableInstruction(); got != want {
		t.Fatalf("instruction=%q want=%q", got, want)
	}
}

func TestConfigNpmSelfUpdateCommandsOriginal(t *testing.T) {
	for _, tc := range []struct {
		name, installed, target, spec string
		configured, empty, spaced     bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:184
		{name: "self-updates npm installs from custom prefixes", installed: configPiPackage},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:205
		{name: "self-updates exact npm versions without uninstalling the current package", installed: configPiPackage, target: configPiPackage, spec: configPiPackage + "@1.2.3"},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:228
		{name: "self-updates renamed packages from the current install prefix", installed: configOldPiPackage, target: "@new-scope/pi"},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:252
		{name: "self-update respects configured npmCommand", installed: configPiPackage, configured: true},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:272
		{name: "self-update treats empty npmCommand as unset", installed: configPiPackage, empty: true},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:288
		{name: "quotes npm self-update display paths", installed: configPiPackage, spaced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provenance, prefix, _ := configNativeInstall(t, ownerNPM, tc.installed, tc.spaced)
			var configured []string
			if tc.configured {
				configured = []string{"npm", "--prefix", prefix}
			} else if tc.empty {
				configured = []string{}
			}
			target := tc.target
			if target == "" {
				target = tc.installed
			}
			spec := tc.spec
			if spec == "" {
				spec = target
			}
			command := provenance.GetSelfUpdateCommand(configured, SelfUpdatePackageTarget{PackageName: target, InstallSpec: spec})
			displayPrefix := prefix
			if tc.spaced {
				displayPrefix = `"` + prefix + `"`
			}
			install := &SelfUpdateCommand{Command: "npm", Args: []string{"--prefix", prefix, "install", "-g", "--ignore-scripts", "--min-release-age=0", spec}, Display: "npm --prefix " + displayPrefix + " install -g --ignore-scripts --min-release-age=0 " + spec}
			want := install
			if target != tc.installed {
				remove := &SelfUpdateCommand{Command: "npm", Args: []string{"--prefix", prefix, "uninstall", "-g", tc.installed}, Display: "npm --prefix " + displayPrefix + " uninstall -g " + tc.installed}
				want = &SelfUpdateCommand{Command: install.Command, Args: install.Args, Display: remove.Display + " && " + install.Display, Steps: []*SelfUpdateCommand{remove, install}}
			}
			if !reflect.DeepEqual(command, want) {
				t.Fatalf("command=%+v want=%+v", command, want)
			}
			emitConfigCommand(t, tc.name, command, prefix, tc.spaced)
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:298
func TestConfigWindowsNpmDoesNotInventCustomPrefixOriginal(t *testing.T) {
	pkg := `C:\Users\Admin\npm prefix\node_modules\@earendil-works\pi-coding-agent`
	provenance, ambiguous := detectPackageManagerOwnership("windows", pkg+`\dist\cli.js`, fakeCmdRunner{outputs: map[string]string{"npm root -g": `C:\Users\Admin\npm prefix\node_modules`}})
	if ambiguous || provenance == nil || provenance.PackageOwner != ownerNPM || provenance.NpmPrefix != "" {
		t.Fatalf("ownership=%+v ambiguous=%t", provenance, ambiguous)
	}
	command := provenance.GetSelfUpdateCommand(nil, SelfUpdatePackageTarget{PackageName: configPiPackage})
	if command == nil || "Run: "+command.Display != "Run: npm install -g --ignore-scripts --min-release-age=0 @earendil-works/pi-coding-agent" {
		t.Fatalf("command=%+v", command)
	}
}

func TestConfigOtherManagerSelfUpdateCommandsOriginal(t *testing.T) {
	for _, tc := range []struct {
		name              string
		owner             PackageManagerOwner
		installed, target string
		install, remove   []string
		display           string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:309
		{name: "self-updates bun global installs from bun pm bin", owner: ownerBun, installed: configPiPackage, install: []string{"install", "-g", "--ignore-scripts", "--minimum-release-age=0", configPiPackage}, display: "bun install -g --ignore-scripts --minimum-release-age=0 " + configPiPackage},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:322
		{name: "self-updates renamed pnpm global installs by removing the old package first", owner: ownerPNPM, installed: configOldPiPackage, target: "@new-scope/pi", install: []string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", "@new-scope/pi"}, remove: []string{"remove", "-g", configOldPiPackage}, display: "pnpm remove -g " + configOldPiPackage + " && pnpm install -g --ignore-scripts --config.minimumReleaseAge=0 @new-scope/pi"},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:391
		{name: "self-updates renamed yarn global installs by removing the old package first", owner: ownerYarn, installed: configOldPiPackage, target: "@new-scope/pi", install: []string{"global", "add", "--ignore-scripts", "@new-scope/pi"}, remove: []string{"global", "remove", configOldPiPackage}, display: "yarn global remove " + configOldPiPackage + " && yarn global add --ignore-scripts @new-scope/pi"},
		// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:416
		{name: "self-updates renamed bun global installs by removing the old package first", owner: ownerBun, installed: configOldPiPackage, target: "@new-scope/pi", install: []string{"install", "-g", "--ignore-scripts", "--minimum-release-age=0", "@new-scope/pi"}, remove: []string{"uninstall", "-g", configOldPiPackage}, display: "bun uninstall -g " + configOldPiPackage + " && bun install -g --ignore-scripts --minimum-release-age=0 @new-scope/pi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provenance, _, _ := configNativeInstall(t, tc.owner, tc.installed, false)
			target := tc.target
			if target == "" {
				target = tc.installed
			}
			got := provenance.GetSelfUpdateCommand(nil, SelfUpdatePackageTarget{PackageName: target})
			want := &SelfUpdateCommand{Command: string(tc.owner), Args: tc.install, Display: tc.display}
			if tc.remove != nil {
				remove := &SelfUpdateCommand{Command: string(tc.owner), Args: tc.remove, Display: string(tc.owner) + " " + strings.Join(tc.remove, " ")}
				install := &SelfUpdateCommand{Command: string(tc.owner), Args: tc.install, Display: string(tc.owner) + " " + strings.Join(tc.install, " ")}
				want.Steps = []*SelfUpdateCommand{remove, install}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("command=%+v want=%+v", got, want)
			}
			emitConfigCommand(t, tc.name, got, "", false)
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:348
func TestConfigPnpmStoreEntrypointOwnershipOriginal(t *testing.T) {
	t.Setenv("PIG_INSTALL_TIER", "")
	temp := t.TempDir()
	root := filepath.Join(temp, "Library", "pnpm", "global", "v11")
	global := filepath.Join(root, "11e9a", "node_modules", "@earendil-works", "pi-coding-agent")
	store := filepath.Join(temp, "Library", "pnpm", "store", "v11", "links", "@earendil-works", "pi-coding-agent", "0.75.0", "hash", "node_modules", "@earendil-works", "pi-coding-agent")
	if err := os.MkdirAll(filepath.Join(store, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "package.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := writeFakeExe(t, filepath.Join(store, "dist"))
	// D39 binds a real native launcher symlink instead of accepting an unrelated Node argv[1] path as ownership proof.
	testenv.RequireDirectoryLink(t, store, global)
	entrypoint := filepath.Join(global, "dist", filepath.Base(executable))
	provenance, err := resolveSelfUpdateTierOn(runtime.GOOS, executable, fakeCmdRunner{outputs: map[string]string{"pnpm root -g": root}}, entrypoint)
	if err != nil || provenance == nil || provenance.Tier != TierPackageManager || provenance.PackageOwner != ownerPNPM {
		t.Fatalf("store ownership=%+v err=%v", provenance, err)
	}
	command := provenance.GetSelfUpdateCommand(nil, SelfUpdatePackageTarget{PackageName: configPiPackage})
	want := &SelfUpdateCommand{Command: "pnpm", Args: []string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", configPiPackage}, Display: "pnpm install -g --ignore-scripts --config.minimumReleaseAge=0 " + configPiPackage}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command=%+v want=%+v", command, want)
	}
	// A launcher for a different executable cannot supply a ownership claim.
	unrelated := writeFakeExe(t, t.TempDir())
	other, err := resolveSelfUpdateTierOn(runtime.GOOS, unrelated, fakeCmdRunner{outputs: map[string]string{"pnpm root -g": root}}, entrypoint)
	if err != nil || other.Tier == TierPackageManager {
		t.Fatalf("spoofed ownership=%+v err=%v", other, err)
	}
	fmt.Println("CONFIG_PNPM_STORE resolved")
}

// Pi config.ts:72-79 applies JavaScript /\s/ to both the executable and each argument.
func TestConfigCommandDisplayJavaScriptWhitespace(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"plain", "plain"},
		{"with space", `"with space"`},
		{"with\u00a0nbsp", "\"with\u00a0nbsp\""},
		{"with\ufeffbom", "\"with\ufeffbom\""},
		{"with\fbreak", "\"with\fbreak\""},
		{"with\u0085nel", "with\u0085nel"},
	} {
		step := makeSelfUpdateCommandStep(tc.value, []string{tc.value})
		if want := tc.want + " " + tc.want; step.Display != want {
			t.Errorf("display(%q)=%q, want %q", tc.value, step.Display, want)
		}
	}
}

// config.ts:169-187 keeps npm's argument contract even when npmCommand selects a wrapper or another executable name.
func TestConfigNpmOwnerKeepsNpmArgsForConfiguredExecutable(t *testing.T) {
	for _, executable := range []string{"pnpm", "bun", "yarn", "mise"} {
		got := PackageManagerUpdateCommand(ownerNPM, "@scope/pkg", []string{executable, "--custom"}, SelfUpdatePackageTarget{PackageName: "@scope/pkg", InstallSpec: "@scope/pkg@1.2.3"})
		want := &SelfUpdateCommand{Command: executable, Args: []string{"--custom", "install", "-g", "--ignore-scripts", "--min-release-age=0", "@scope/pkg@1.2.3"}, Display: executable + " --custom install -g --ignore-scripts --min-release-age=0 @scope/pkg@1.2.3"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("npm owner with %s: got %+v, want %+v", executable, got, want)
		}
	}
}

func TestConfigCommandDisplayQuotesExecutable(t *testing.T) {
	provenance, _, _ := configNativeInstall(t, ownerNPM, configPiPackage, false)
	command := provenance.GetSelfUpdateCommand([]string{"/tools with spaces/npm"}, SelfUpdatePackageTarget{PackageName: configPiPackage})
	want := `"/tools with spaces/npm" install -g --ignore-scripts --min-release-age=0 @earendil-works/pi-coding-agent`
	if command == nil || command.Display != want {
		t.Fatalf("display=%+v want=%s", command, want)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/config.test.ts:442
func TestConfigReadOnlyPackageRootRejectsSelfUpdateOriginal(t *testing.T) {
	if testenv.RunUnprivileged(t) {
		return
	}
	t.Setenv("PIG_INSTALL_TIER", "")
	prefix := t.TempDir()
	root := filepath.Join(prefix, "lib", "node_modules")
	pkg := filepath.Join(root, "@earendil-works", "pi-coding-agent")
	bin := filepath.Join(pkg, "dist")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := writeFakeExe(t, bin)
	testenv.ReadOnlyDir(t, pkg)
	provenance, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{"npm root -g": root}})
	if err != nil {
		t.Fatal(err)
	}
	if provenance.Tier != TierUnsupported {
		t.Fatalf("read-only package was allowed to update: %+v", provenance)
	}
	if command := provenance.GetSelfUpdateCommand(nil, SelfUpdatePackageTarget{PackageName: configPiPackage}); command != nil {
		t.Fatalf("read-only command=%+v", command)
	}
	if instruction := provenance.GetSelfUpdateUnavailableInstruction(); !strings.Contains(instruction, "the install path is not writable") {
		t.Fatalf("instruction=%q", instruction)
	}
	fmt.Println("CONFIG_READONLY command=absent hint=not-writable")
}
