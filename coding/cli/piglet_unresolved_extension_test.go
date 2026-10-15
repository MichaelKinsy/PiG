package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
)

func writeUnresolvedExtensionPiglet(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "unresolved.piglet.yaml")
	source := "name: unresolved\nextensions:\n  - name: missing-ext\n    origins:\n      - local:./does-not-exist\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A Piglet extension whose origins do not resolve is an extension that fails to
// load. Pi reports every failed extension as a startup error, not a warning
// (main.ts:799-802 `Failed to load extension "<path>": <error>`).
func TestResolvePigletExtConfigsKeepsUnresolvedExtensionAsFailure(t *testing.T) {
	parsed, err := piglet.Parse(writeUnresolvedExtensionPiglet(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	configs := resolvePigletExtConfigs(parsed)
	if len(configs) != 1 || configs[0].ResolveError() == nil {
		t.Fatalf("configs = %#v, want one unresolved extension", configs)
	}
	diagnostic := extensionLoadDiagnostics([]error{loadErrorFor(t, configs)})[0]
	for _, want := range []string{`Failed to load extension "`, `missing-ext`, `no origin resolved`} {
		if !strings.Contains(diagnostic.Message, want) {
			t.Fatalf("diagnostic %q lacks %q", diagnostic.Message, want)
		}
	}
	if diagnostic.Type != "error" {
		t.Fatalf("diagnostic type = %q, want error", diagnostic.Type)
	}
}

// Pi exits 1 with the error and the `-ne` hint when an extension fails to load
// (main.ts:913-923). The failure must not be a stderr warning that the
// alternate screen hides.
func TestPigletUnresolvedExtensionFailsStartupLikeBrokenExtension(t *testing.T) {
	home := t.TempDir()
	agentDir, cwd := filepath.Join(home, "agent"), filepath.Join(home, "cwd")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pigletPath := writeUnresolvedExtensionPiglet(t, filepath.Join(home, "piglets"))
	binary := buildPigBinaryForSignalTest(t)

	failed := runRPCStartup(t, binary, home, agentDir, cwd, "--piglet", pigletPath)
	var exitErr *exec.ExitError
	if !errors.As(failed.err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("startup error = %v, want exit status 1\nstderr:\n%s", failed.err, failed.stderr)
	}
	lines := strings.Split(strings.TrimSpace(failed.stderr), "\n")
	wantHint := `Hint: Start without extensions using "pig -ne".`
	if len(lines) < 2 || !strings.HasPrefix(lines[len(lines)-2], "Error: Failed to load extension ") || !strings.Contains(lines[len(lines)-2], "missing-ext") || lines[len(lines)-1] != wantHint {
		t.Fatalf("stderr does not end with the extension failure and hint:\n%s", failed.stderr)
	}
	if strings.Contains(failed.stderr, "warning: extension") {
		t.Fatalf("failure still reported as a warning:\n%s", failed.stderr)
	}
}

// Pi's loadExtensions records every failed extension in path order
// (packages/coding-agent/src/core/extensions/loader.ts:684-696), and main.ts
// reports each one (main.ts:799-802). Two unresolved Piglet extensions are two
// load errors, in declaration order around the extensions that resolved; the
// startup merge must not collapse them into one.
func TestResolvePigletExtConfigsKeepsEveryUnresolvedExtensionInOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "present.mjs"), []byte("export default function () {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "order.piglet.yaml")
	source := "name: order\nextensions:\n" +
		"  - name: missing-a\n    origins:\n      - local:./does-not-exist-a\n" +
		"  - name: present\n    origins:\n      - local:./present.mjs\n" +
		"  - name: missing-b\n    origins:\n      - local:./does-not-exist-b\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	configs := mergeExtConfigs(resolvePigletExtConfigs(parsed))
	if len(configs) != 3 {
		t.Fatalf("configs = %#v, want missing-a, present and missing-b", configs)
	}
	for i, want := range []string{"missing-a", "", "missing-b"} {
		resolveErr := configs[i].ResolveError()
		if want == "" {
			if resolveErr != nil {
				t.Fatalf("configs[%d] = %v, want the resolved extension", i, resolveErr)
			}
			continue
		}
		if resolveErr == nil || !strings.Contains(resolveErr.Error(), `"`+want+`"`) {
			t.Fatalf("configs[%d] error = %v, want the %s failure", i, resolveErr, want)
		}
	}
}
