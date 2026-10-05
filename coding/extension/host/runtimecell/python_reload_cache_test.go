package runtimecell

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// pythonReloadCacheHarness loads the rendered runner without starting it and runs one case of its module cache. load is one admission's factory call and reload one reload pass: its first admission decides, then every member's factory runs again. Every case starts with ext, inner and twin loaded and a module of each root imported after the factories, as a handler imports one. The factories run in the order ext, inner, twin, or twin, inner, ext for a case whose name ends in _twin_first, because the runner starts them on concurrent threads.
const pythonReloadCacheHarness = `import importlib.util, io, os, sys, threading, time, types

runner_path, case = sys.argv[1], sys.argv[2]
runner = {'__name__': 'pig_runner', '__file__': runner_path}
exec(compile(open(runner_path, encoding='utf-8').read(), runner_path, 'exec'), runner)
ITEM = {item[0]: item for item in runner['ITEMS']}
NAMES = ('ext', 'inner', 'twin')
ORDER = tuple(reversed(NAMES)) if case.endswith('_twin_first') else NAMES
ROOT = ITEM['ext'][4]
sys.path.insert(0, os.path.join(ROOT, '.venv', 'lib', 'site-packages'))
sys.path.insert(0, os.path.dirname(runner_path))


class Ext:
    def __init__(self, module):
        self.module = module

    def run_with_socket(self, sock):
        probe.loaded = self.module
        probe.runs.append(self.module)
        probe.serve.wait(10)


# A generation returns once probe.serve is set; a case clears it to hold generations open.
probe = types.SimpleNamespace(Ext=Ext, loaded=None, runs=[], serve=threading.Event(), lazy_loads=[])
probe.serve.set()
sys.modules['harness_probe'] = probe


def load(name):
    probe.loaded = None
    runner['_run'](ITEM[name], 'socket')
    return probe.loaded


def reload(reload_pass):
    runner['_reload_edited'](reload_pass)
    return {name: load(name) for name in ORDER}


def write(name, text, mtime_ns):
    path = os.path.join(ROOT, *name.split('/'))
    with open(path, 'w', encoding='utf-8') as f:
        f.write(text)
    os.utime(path, ns=(mtime_ns, mtime_ns))
    return path


def edit(name, text):
    # Newer than every factory call, even on a file system with a coarse clock.
    write(name, text, time.time_ns() + 5_000_000_000)


def source(name):
    with open(os.path.join(ROOT, *name.split('/')), encoding='utf-8') as f:
        return f.read()


first = {name: load(name) for name in ORDER}
import ext_lazy, inner_lazy, fake_sdk, dep, cache_mod
before = dict(sys.modules)
after_start = time.time_ns()


def kept(*names):
    for name in names:
        assert sys.modules.get(name) is before[name], name + ' stays imported'


def dropped(*names):
    for name in names:
        assert sys.modules.get(name) is not before[name], name + ' is dropped'


def case_unedited():
    second = reload('1')
    for name in NAMES:
        assert second[name] is first[name], 'an unedited reload keeps the factory module of ' + name
    kept('ext_helper', 'twin_helper', 'inner_helper', 'inner.ext_owned', 'ext_lazy', 'inner_lazy', 'fake_sdk', 'dep', 'cache_mod')


def case_kept_sources():
    edit('sdk/fake_sdk.py', 'state = ["edited"]\n')
    edit('.venv/lib/site-packages/dep.py', 'state = ["edited"]\n')
    edit('cache/cache_mod.py', 'state = ["edited"]\n')
    second = reload('1')
    for name in NAMES:
        assert second[name] is first[name], 'an edit to the SDK, an installed package or the runner directory keeps ' + name
    kept('fake_sdk', 'dep', 'cache_mod')


def case_same_root():
    edit('twin_helper.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['twin'] is not first['twin'] and sys.modules['twin_helper'].state == ['edited'], 'the edited extension imports its edited helper'
    assert second['ext'] is first['ext'], 'an extension in the same directory keeps its factory module'
    assert second['inner'] is first['inner'], 'an extension in a nested directory keeps its factory module'
    # ext_lazy belongs to ext as much as to twin, and ext did not reload.
    kept('ext_helper', 'inner_helper', 'inner.ext_owned', 'ext_lazy', 'inner_lazy', 'fake_sdk', 'dep', 'cache_mod')


def case_outer_root():
    edit('ext_helper.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['ext'] is not first['ext'] and sys.modules['ext_helper'].state == ['edited'], 'the edited extension imports its edited helper'
    assert second['twin'] is first['twin'] and second['inner'] is first['inner'], 'every other extension keeps its factory module'
    dropped('inner.ext_owned')
    kept('twin_helper', 'inner_helper', 'ext_lazy', 'inner_lazy')


def case_nested_root():
    edit('inner/inner_helper.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['inner'] is not first['inner'] and sys.modules['inner_helper'].state == ['edited'], 'the edited extension imports its edited helper'
    assert second['ext'] is first['ext'] and second['twin'] is first['twin'], 'an extension in an enclosing directory keeps its factory module'
    assert 'inner_lazy' not in sys.modules, 'an edit drops every module of the extension'
    kept('ext_helper', 'twin_helper', 'inner.ext_owned', 'ext_lazy')


def case_nested_unclaimed():
    edit('inner/inner_lazy.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['inner'] is not first['inner'], 'an edit to a module imported after the factory reloads its extension'
    assert second['ext'] is first['ext'] and second['twin'] is first['twin'], 'a module under a nested root belongs to the nested extension alone'
    import inner_lazy as again
    assert again.state == ['edited'], 'the edited source is imported'
    kept('ext_helper', 'twin_helper', 'inner.ext_owned', 'ext_lazy')


def case_claim_beats_location():
    edit('inner/ext_owned.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['ext'] is not first['ext'] and sys.modules['inner.ext_owned'].state == ['edited'], 'the extension whose factory imported a module owns it'
    assert second['inner'] is first['inner'] and second['twin'] is first['twin'], 'the module does not belong to the extension whose directory holds it'
    kept('inner_helper', 'inner_lazy', 'twin_helper', 'ext_lazy')


def case_shared_unclaimed():
    edit('ext_lazy.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['ext'] is not first['ext'] and second['twin'] is not first['twin'], 'a module no factory imported belongs to every extension of its root'
    assert second['inner'] is first['inner'], 'an extension in a nested directory keeps its factory module'
    assert 'ext_lazy' not in sys.modules, 'the edited module is dropped'
    import ext_lazy as again
    assert again.state == ['edited'], 'the edited source is imported'
    kept('inner_helper', 'inner_lazy')


def case_passes():
    reload('1')
    edit('ext_main.py', source('ext_main.py') + 'state = ["main edited"]\n')
    assert reload('1')['ext'] is first['ext'], 'a pass already seen drops nothing'
    assert reload('0')['ext'] is first['ext'], 'an admission outside a reload drops nothing'
    third = reload('2')
    assert third['ext'] is not first['ext'] and third['ext'].state == ['main edited'], 'an edited factory module is imported again'


def case_size_only():
    path = os.path.join(ROOT, 'inner', 'inner_helper.py')
    mtime = os.stat(path).st_mtime_ns
    write('inner/inner_helper.py', 'state = ["a longer edit"]\n', mtime)
    assert reload('1')['inner'] is not first['inner'], 'an edit that keeps the modification time is found by its size'


def case_deleted():
    os.remove(os.path.join(ROOT, 'inner', 'inner_lazy.py'))
    assert reload('1')['inner'] is not first['inner'], 'a deleted module file reloads its extension'


def case_reimported_by_old_generation():
    # The edit is older than the factory call that imports the edited source.
    write('inner/inner_helper.py', 'state = ["edited", "and longer"]\n', time.time_ns())
    runner['_reload_edited']('1')
    # The old generation's handler imports the helper again before the new factory call does.
    import inner_helper as helper
    second = {name: load(name) for name in NAMES}
    assert second['inner'] is not first['inner'] and helper.state == ['edited', 'and longer'], 'the edited extension imports its edited helper'
    third = reload('2')
    assert third['inner'] is second['inner'], 'an unedited reload after an edit keeps the re-imported factory module'
    assert sys.modules.get('inner_helper') is helper, 'an unedited reload after an edit keeps a module imported again outside the factory'


def case_new_module_between_loads():
    time.sleep(0.05)
    write('inner/inner_helper.py', 'state = ["edited", "and longer"]\n', time.time_ns())
    time.sleep(0.05)
    before_second = time.time_ns()
    second = reload('1')
    assert second['inner'] is not first['inner'], 'the edited extension reloads'
    # The file is newer than the first factory call and older than the second.
    write('inner/inner_new.py', 'state = []\n', (after_start + before_second) // 2)
    import inner_new
    third = reload('2')
    assert third['inner'] is second['inner'], 'a module first imported after a re-import, with a file older than that re-import, is not edited'
    assert sys.modules.get('inner_new') is inner_new, 'the module stays imported'


def case_edit_before_admission_outside_reload():
    time.sleep(0.05)
    write('inner/inner_lazy.py', 'state = ["edited"]\n', time.time_ns())
    time.sleep(0.05)
    runner['_reload_edited']('0')
    assert load('inner') is first['inner'], 'an admission outside a reload keeps the module'
    second = reload('1')
    assert second['inner'] is not first['inner'], 'an edit made before an admission outside a reload is found by the next reload'
    assert 'inner_lazy' not in sys.modules, 'the edited module is dropped'


def case_unusual_modules():
    path = write('inner/broken_lazy.py', "import harness_probe\nharness_probe.lazy_loads.append(1)\nraise RuntimeError('the lazy module was loaded')\n", time.time_ns())
    spec = importlib.util.spec_from_file_location('broken_lazy', path)
    spec.loader = importlib.util.LazyLoader(spec.loader)
    module = importlib.util.module_from_spec(spec)
    sys.modules['broken_lazy'] = module
    spec.loader.exec_module(module)
    # sys.modules may hold an object that is not a module.
    sys.modules['harness_number'] = 42
    edit('inner/inner_helper.py', 'state = ["edited"]\n')
    second = reload('1')
    assert not probe.lazy_loads and type(module) is importlib.util._LazyModule, 'the reload decision does not load a lazily loaded module'
    assert second['inner'] is not first['inner'], 'the edited extension reloads'


def case_added_module_file():
    directory = os.path.join(ROOT, 'inner')
    info = os.stat(directory)
    write('inner/inner_added.py', 'state = ["added"]\n', time.time_ns())
    edit('inner/inner_main.py', source('inner/inner_main.py').replace('import inner_helper\n', 'import inner_helper\nimport inner_added\n'))
    # A file system with a coarse clock leaves the directory's modification time as the import system cached it.
    os.utime(directory, ns=(info.st_atime_ns, info.st_mtime_ns))
    second = reload('1')
    assert second['inner'] is not first['inner'] and sys.modules['inner_added'].state == ['added'], 'a reload imports a module file added since the last import'


def case_failed_factory():
    good = source('twin_main.py')
    write('twin_main.py', good + 'raise RuntimeError("the factory module failed")\n', time.time_ns())
    runner['_reload_edited']('1')
    try:
        load('twin')
    except RuntimeError:
        pass
    else:
        raise AssertionError('the broken factory module fails')
    # An admission outside a reload, as a replacement Session's, runs the fixed factory module.
    write('twin_main.py', good, time.time_ns())
    runner['_reload_edited']('0')
    fixed = load('twin')
    assert fixed is not None and fixed is not first['twin'], 'the fixed factory module is imported'
    second = reload('2')
    assert second['twin'] is fixed, 'an unedited reload keeps a factory module that executed again after a failure'
    edit('twin_helper.py', 'state = ["edited"]\n')
    third = reload('3')
    assert third['twin'] is not fixed, 'the edited extension reloads'
    assert third['ext'] is first['ext'], 'a module that a failed factory call imported belongs to that extension'


def case_failed_decision():
    edit('inner/inner_helper.py', 'state = ["edited"]\n')

    def check_fails(*args):
        raise RuntimeError('the edit check failed')

    edited, runner['_edited'] = runner['_edited'], check_fails
    stderr, sys.stderr = sys.stderr, io.StringIO()
    # The runner starts inner's first generation, which serves while the control thread parks the process and admits a second one with a reload pass.
    os.environ['PIG_EXT_ACTIVE_MEMBERS'] = 'inner'
    os.environ[ITEM['inner'][1]] = 'socket'
    sys.stdin = io.StringIO('park\t\t\nadmit\tinner\tsocket\t\t\t1\n')
    probe.runs.clear()
    probe.serve.clear()
    thread = threading.Thread(target=runner['main'], daemon=True)
    thread.start()
    deadline = time.monotonic() + 10
    while len(probe.runs) < 2 and time.monotonic() < deadline:
        time.sleep(0.01)
    runs = list(probe.runs)
    probe.serve.set()
    thread.join(10)
    report, sys.stderr = sys.stderr.getvalue(), stderr
    runner['_edited'] = edited
    assert len(runs) == 2, 'the runner admits the member after a reload decision fails: ' + report
    assert not thread.is_alive(), 'the runner exits with its last generation: ' + report
    assert all(module is first['inner'] for module in runs), 'a failed reload decision keeps every module'
    assert 'the edit check failed' in report, 'the failure is reported: ' + report
    kept('inner_helper', 'inner_lazy')


def case_import_in_progress():
    gate = types.SimpleNamespace(entered=threading.Event(), release=threading.Event())
    sys.modules['harness_gate'] = gate
    write('inner/inner_slow.py', 'import harness_gate\nharness_gate.entered.set()\nharness_gate.release.wait(10)\nstate = []\n', time.time_ns())
    errors = []

    def import_slow():
        try:
            import inner_slow
        except BaseException as exc:
            errors.append(exc)

    thread = threading.Thread(target=import_slow)
    thread.start()
    assert gate.entered.wait(10), 'the import started'
    edit('inner/inner_helper.py', 'state = ["edited"]\n')
    runner['_reload_edited']('1')
    gate.release.set()
    thread.join(10)
    assert not errors, 'an import in progress survives the reload decision: ' + repr(errors)
    assert 'inner_slow' in sys.modules, 'a module still importing stays imported'
    assert 'inner_helper' not in sys.modules, 'the edited extension is dropped'


def case_shared_edited():
    edit('common.py', 'import common_dep\nstate = ["edited"]\n')
    second = reload('1')
    assert second['ext'] is not first['ext'] and second['twin'] is not first['twin'], 'an edit to a module two factories import reloads both extensions'
    assert second['ext'].common.state == ['edited'] and second['twin'].common.state == ['edited'], 'both extensions import the edited source'
    assert second['inner'] is first['inner'], 'an extension that does not import the module keeps its factory module'
    kept('inner_helper', 'inner_lazy')
    edit('common.py', 'import common_dep\nstate = ["edited again"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not second['twin'], 'a later edit reloads both extensions again'
    assert third['ext'].common.state == ['edited again'] and third['twin'].common.state == ['edited again'], 'both extensions import the later edit'


case_shared_edited_twin_first = case_shared_edited


def case_shared_kept():
    edit('ext_helper.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['ext'] is not first['ext'] and second['twin'] is first['twin'], 'an edit to a module of one extension reloads that extension alone'
    kept('common', 'common_dep', 'twin_helper')
    assert second['ext'].common is second['twin'].common, 'the reloaded extension imports the shared module that the unedited extension still uses'
    edit('common.py', 'import common_dep\nstate = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not first['twin'], 'an edit to the shared module then reloads both extensions'
    assert third['ext'].common.state == ['edited'] and third['twin'].common.state == ['edited'], 'both extensions import the edited source'


case_shared_kept_twin_first = case_shared_kept


def case_shared_indirect():
    edit('common_dep.py', 'state = ["edited"]\n')
    second = reload('1')
    assert second['ext'] is not first['ext'] and second['twin'] is not first['twin'], 'an edit to a module that a shared module imports reloads every extension that imports the shared module'
    assert second['ext'].common.common_dep.state == ['edited'] and second['twin'].common.common_dep.state == ['edited'], 'both extensions import the edited source'
    assert second['inner'] is first['inner'], 'an extension that does not import the module keeps its factory module'


case_shared_indirect_twin_first = case_shared_indirect


def shared_modules(files, ext_import, twin_import):
    # Writes modules for both factories to import and reloads once; ext's factory imports them before twin's does.
    for name, text in files.items():
        os.makedirs(os.path.dirname(os.path.join(ROOT, *name.split('/'))), exist_ok=True)
        write(name, text, time.time_ns())
    edit('ext_main.py', source('ext_main.py').replace('import common\n', 'import common\n' + ext_import + '\n'))
    edit('twin_main.py', source('twin_main.py').replace('import common\n', 'import common\n' + twin_import + '\n'))
    second = reload('1')
    assert second['ext'] is not first['ext'] and second['twin'] is not first['twin'], 'both edited factory modules are imported again'
    return second


def case_shared_package():
    second = shared_modules({'spkg/__init__.py': 'state = []\n', 'spkg/part.py': 'state = []\n'}, 'import spkg.part', 'import spkg.part')
    edit('spkg/__init__.py', 'state = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not second['twin'], 'an edit to a package reloads every extension that imports one of its modules'
    package = sys.modules['spkg']
    assert package.state == ['edited'] and third['twin'].spkg is package, 'both extensions import the edited package'
    assert getattr(package, 'part', None) is sys.modules['spkg.part'], 'the package and its module are imported again together'


def case_shared_relative():
    second = shared_modules({'spkg/__init__.py': 'state = []\n', 'spkg/part.py': 'state = []\n', 'spkg/rel.py': 'from . import part\n'}, 'import spkg.part', 'import spkg.rel')
    edit('spkg/part.py', 'state = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not second['twin'], 'a relative import of a module that another factory imported first counts for both extensions'
    assert third['twin'].spkg.rel.part.state == ['edited'], 'both extensions import the edited source'


def case_shared_star():
    second = shared_modules({'spkg/__init__.py': '__all__ = ["part"]\n', 'spkg/part.py': 'state = []\n'}, 'import spkg.part', 'from spkg import *')
    edit('spkg/part.py', 'state = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not second['twin'], 'a star import of a package counts each module that __all__ names'
    assert third['twin'].part.state == ['edited'], 'both extensions import the edited source'


def case_shared_import_module():
    # twin's factory imports via through an installed plugin loader, so only the factory call's own record makes twin a user of via.
    loader = '.venv/lib/site-packages/plugin_loader.py'
    second = shared_modules({'via.py': 'import importlib\nvia_dep = importlib.import_module("via_dep")\nstate = []\n', 'via_dep.py': 'state = []\n', loader: 'import importlib\n\ndef load(name):\n    return importlib.import_module(name)\n'}, 'import via', 'import plugin_loader\nvia = plugin_loader.load("via")')
    edit('via.py', 'import importlib\nvia_dep = importlib.import_module("via_dep")\nstate = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not second['twin'], 'importlib.import_module in a factory call counts as an import'
    assert third['twin'].via.state == ['edited'] and third['ext'].via is third['twin'].via, 'both extensions import the edited source'
    edit('via_dep.py', 'state = ["edited"]\n')
    fourth = reload('3')
    assert fourth['ext'] is not third['ext'] and fourth['twin'] is not third['twin'], 'importlib.import_module in a module that a factory call imports counts as an import'
    assert fourth['twin'].via.via_dep.state == ['edited'], 'both extensions import the edited source'


case_shared_import_module_twin_first = case_shared_import_module


def case_shared_relative_import_module():
    second = shared_modules({'spkg/__init__.py': 'state = []\n', 'spkg/part.py': 'state = []\n'}, 'import spkg.part', 'import importlib\npart = importlib.import_module(".part", "spkg")')
    edit('spkg/part.py', 'state = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not second['twin'], 'importlib.import_module with a relative name counts as an import of the module it resolves to'
    assert third['twin'].part.state == ['edited'], 'both extensions import the edited source'


def case_shared_import_after_load():
    write('inner/inner_late.py', 'state = []\n', time.time_ns())
    edit('common.py', 'import common_dep\nstate = []\n\ndef later():\n    global late\n    import inner_late as late\n')
    second = reload('1')
    assert second['ext'] is not first['ext'] and second['twin'] is not first['twin'], 'both extensions import the edited shared module'
    # A handler calls later, which imports a module of the nested directory after common's import.
    sys.modules['common'].later()
    edit('inner/inner_late.py', 'state = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not second['ext'] and third['twin'] is not second['twin'], 'a module that a used module imports after its own import counts for every extension that uses it'
    assert third['inner'] is not first['inner'], 'the module still belongs to the extension of its directory'
    assert 'inner_late' not in sys.modules, 'the edited module is dropped'


def case_shared_dropped_use_twin_first():
    edit('twin_main.py', source('twin_main.py').replace('import common\n', ''))
    second = reload('1')
    assert second['twin'] is not first['twin'] and second['ext'] is first['ext'], 'the edited factory module is imported again'
    kept('common', 'common_dep')
    edit('common.py', 'import common_dep\nstate = ["edited"]\n')
    third = reload('2')
    assert third['ext'] is not first['ext'] and third['ext'].common.state == ['edited'], 'the extension that still imports the module reloads'
    assert third['twin'] is second['twin'], 'an extension whose reloaded factory no longer imports the module keeps its factory module'


def case_unclaimed_edit_during_reload():
    edit('twin_helper.py', 'state = ["edited"]\n')
    runner['_reload_edited']('1')
    # The edit lands after the reload decision and before the factory calls that follow it.
    time.sleep(0.05)
    write('ext_lazy.py', 'state = ["edited"]\n', time.time_ns())
    time.sleep(0.05)
    second = {name: load(name) for name in ORDER}
    assert second['twin'] is not first['twin'] and second['ext'] is first['ext'], 'the edited extension reloads'
    third = reload('2')
    assert third['ext'] is not first['ext'] and third['twin'] is not second['twin'], 'an edit older than one user\'s last factory call still reloads every extension that uses the module'
    assert 'ext_lazy' not in sys.modules, 'the edited module is dropped'


def case_shared_edit_before_admission_outside_reload():
    edit('twin_main.py', source('twin_main.py').replace('def new_extension():\n', 'def new_extension():\n    import common\n'))
    second = reload('1')
    assert second['twin'] is not first['twin'] and second['ext'] is first['ext'], 'the edited factory module is imported again'
    edit('common.py', 'import common_dep\nstate = ["edited"]\n')
    runner['_reload_edited']('0')
    assert load('twin') is second['twin'], 'an admission outside a reload keeps the module'
    third = reload('2')
    assert third['ext'] is not first['ext'] and third['twin'] is not second['twin'], 'a factory call outside a reload that imports an edited module again does not hide the edit'
    assert third['ext'].common.state == ['edited'] and third['twin'].common.state == ['edited'], 'both extensions import the edited source'


if case == 'list':
    print('\n'.join(sorted(name[5:] for name in globals() if name.startswith('case_'))))
else:
    globals()['case_' + case]()
    print('ok')
`

// pythonReloadCacheFixture is one extension directory that holds two extensions, ext and twin, a nested extension directory inner, the SDK, an installed package and the runner's own directory, as a home directory does. ext's factory imports inner.ext_owned, a module under inner's directory. The factories of ext and twin both import common, which imports common_dep. A fourth member, idle, shares the directory and never starts, as a member outside PIG_EXT_ACTIVE_MEMBERS does.
var pythonReloadCacheFixture = map[string]string{
	"ext_main.py":                    "import sys\nimport harness_probe\nimport ext_helper\nimport inner.ext_owned\nimport common\nstate = []\n\ndef new_extension():\n    return harness_probe.Ext(sys.modules[__name__])\n",
	"ext_helper.py":                  "state = []\n",
	"ext_lazy.py":                    "state = []\n",
	"common.py":                      "import common_dep\nstate = []\n",
	"common_dep.py":                  "state = []\n",
	"twin_main.py":                   "import sys\nimport harness_probe\nimport twin_helper\nimport common\nstate = []\n\ndef new_extension():\n    return harness_probe.Ext(sys.modules[__name__])\n",
	"twin_helper.py":                 "state = []\n",
	"inner/inner_main.py":            "import sys\nimport harness_probe\nimport inner_helper\nstate = []\n\ndef new_extension():\n    return harness_probe.Ext(sys.modules[__name__])\n",
	"inner/inner_helper.py":          "state = []\n",
	"inner/inner_lazy.py":            "state = []\n",
	"inner/ext_owned.py":             "state = []\n",
	"sdk/fake_sdk.py":                "state = []\n",
	".venv/lib/site-packages/dep.py": "state = []\n",
	"cache/cache_mod.py":             "state = []\n",
}

// TestPythonRunnerReimportsOnlyAnEditedExtensionsOwnSource runs each case of the harness in a fresh process on a fresh fixture.
func TestPythonRunnerReimportsOnlyAnEditedExtensionsOwnSource(t *testing.T) {
	python := toolchain.PythonExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found: %v", python, err)
	}
	harness := filepath.Join(t.TempDir(), "harness.py")
	if err := os.WriteFile(harness, []byte(pythonReloadCacheHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		root := t.TempDir()
		files := map[string]string{"cache/runner.py": renderPythonRunner([]PythonExtension{
			{Name: "ext", Root: root, Package: "ext_main", Factory: "new_extension"},
			{Name: "idle", Root: root, Package: "idle_main", Factory: "new_extension"},
			{Name: "inner", Root: filepath.Join(root, "inner"), Package: "inner_main", Factory: "new_extension"},
			{Name: "twin", Root: root, Package: "twin_main", Factory: "new_extension"},
		}, filepath.Join(root, "sdk"))}
		maps.Copy(files, pythonReloadCacheFixture)
		for name, text := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, python, append([]string{harness, filepath.Join(root, "cache", "runner.py")}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("harness %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	cases := strings.Fields(run(t, "list"))
	if len(cases) == 0 {
		t.Fatal("the harness lists no case")
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if out := run(t, name); out != "ok" {
				t.Fatalf("harness %s:\n%s", name, out)
			}
		})
	}
}
