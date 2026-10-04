package runtimecell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// pythonReloadCacheHarness loads the rendered runner without starting it and drives its module cache as the runner does: each load is one factory call, and each reload pass precedes the factory calls of that pass.
const pythonReloadCacheHarness = `import os, sys, time
runner = {'__name__': 'pig_runner', '__file__': sys.argv[1]}
exec(compile(open(sys.argv[1], encoding='utf-8').read(), sys.argv[1], 'exec'), runner)
root = runner['ITEMS'][0][4]
sys.path.insert(0, os.path.join(root, '.venv', 'lib', 'site-packages'))
sys.path.insert(0, os.path.dirname(sys.argv[1]))

def load():
    runner['_begin'](root)
    module = runner['_module']('ext_main', root)
    runner['_stamp'](root)
    return module

def edit(name, text):
    path = os.path.join(root, *name.split('/'))
    with open(path, 'w', encoding='utf-8') as f:
        f.write(text)
    # Keep the edit newer than the load on a file system with a coarse clock.
    future = time.time_ns() + 5_000_000_000
    os.utime(path, ns=(future, future))

main = load()
import ext_lazy, fake_sdk, dep, cache_mod
kept = {'fake_sdk': fake_sdk, 'dep': dep, 'cache_mod': cache_mod}

runner['_reload_edited']('1')
assert load() is main, 'an unedited extension keeps its factory module'
assert sys.modules.get('ext_lazy') is ext_lazy, 'an unedited module imported after the factory stays imported'

# A module imported after the last factory call has no stamp when it is edited.
import ext_late
edit('ext_late.py', 'state = ["edited"]\n')
runner['_reload_edited']('2')
assert 'ext_late' not in sys.modules and 'ext_lazy' not in sys.modules, 'an edit drops every module of the extension'
for name, module in kept.items():
    assert sys.modules.get(name) is module, name + ' stays imported'
second = load()
assert second is not main, 'the factory module is imported again after an edit'
import ext_late as late_again
assert late_again.state == ['edited'], 'the edited source is imported'

edit('ext_main.py', 'state = ["main edited"]\n')
runner['_reload_edited']('2')
assert load() is second, 'a pass already seen drops nothing'
runner['_reload_edited']('0')
assert load() is second, 'an admission outside a reload drops nothing'
runner['_reload_edited']('3')
third = load()
assert third is not second and third.state == ['main edited'], 'an edited factory module is imported again'
print('ok')
`

func TestPythonRunnerReimportsOnlyAnEditedExtensionsOwnSource(t *testing.T) {
	python := toolchain.PythonExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found: %v", python, err)
	}
	// The extension directory holds the SDK, an installed package and the runner's cache, as a home directory does.
	root := t.TempDir()
	harness := filepath.Join(t.TempDir(), "harness.py")
	if err := os.WriteFile(harness, []byte(pythonReloadCacheHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(root, "cache", "runner.py")
	for name, text := range map[string]string{
		"ext_main.py":                    "state = []\n",
		"ext_lazy.py":                    "state = []\n",
		"ext_late.py":                    "state = []\n",
		"sdk/fake_sdk.py":                "state = []\n",
		".venv/lib/site-packages/dep.py": "state = []\n",
		"cache/cache_mod.py":             "state = []\n",
		"cache/runner.py":                renderPythonRunner([]PythonExtension{{Name: "ext", Root: root, Package: "ext_main", Factory: "new_extension"}}, filepath.Join(root, "sdk")),
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, python, harness, runner).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("harness: %v\n%s", err, out)
	}
}
