package runtimecell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// PythonExtension describes one factory-style Python SDK extension that can be
// packed into a generated subprocess runner. The module must expose a factory
// returning pig_sdk.Extension, typically:
//
//	def new_extension() -> pig_sdk.Extension
//
// The generated runner is a Python subprocess and uses one socket per contained
// extension.
type PythonExtension struct {
	Name    string
	Root    string
	Package string
	Factory string
	Hash    string
}

// PythonPackedCell describes a generated Python packed-cell artifact.
type PythonPackedCell struct {
	Key           string
	Hash          string
	CacheDir      string
	BinaryPath    string
	Cached        bool
	BuildDuration time.Duration
	Extensions    []PythonExtension
}

func BuildPythonPackedCell(ctx context.Context, cacheRoot, key string, extensions []PythonExtension) (*PythonPackedCell, error) {
	if len(extensions) == 0 {
		return nil, fmt.Errorf("packed Python cell requires at least one extension")
	}
	normalized, err := normalizePythonExtensions(extensions)
	if err != nil {
		return nil, err
	}
	lockCandidates := append([]string{os.Getenv("PIG_SDK_PY_ROOT")}, stagedSDKRoots("sdk-py")...)
	return pigsdklock.WithBuildCandidates(ctx, lockCandidates, func() (*PythonPackedCell, error) {
		sdkRoot, err := findPythonSDKRoot()
		if err != nil {
			return nil, err
		}
		return pigsdklock.WithBuildForRoot(ctx, sdkRoot, func() (*PythonPackedCell, error) {
			hash := pythonPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return nil, fmt.Errorf("hash Python packed cell: %s", after)
			}
			cellDir := filepath.Join(cacheRoot, "cells", "python", hash)
			start := time.Now()
			artifactName := packedRunnerName(runtime.GOOS, "python")
			entry, err := PublishArtifact(ctx, cellDir, artifactName, hash, "python", func(scratch string) (string, error) {
				out := filepath.Join(scratch, artifactName)
				if err := os.WriteFile(out, []byte(renderPythonRunner(normalized, sdkRoot)), 0o755); err != nil {
					return "", fmt.Errorf("write generated Python runner: %w", err)
				}
				return out, nil
			})
			if err != nil {
				return nil, err
			}
			var dur time.Duration
			if !entry.Reused {
				dur = time.Since(start)
			}
			return &PythonPackedCell{Key: key, Hash: hash, CacheDir: entry.Dir, BinaryPath: entry.ArtifactPath, Cached: entry.Reused, BuildDuration: dur, Extensions: normalized}, nil
		})
	})
}

func normalizePythonExtensions(extensions []PythonExtension) ([]PythonExtension, error) {
	out := append([]PythonExtension(nil), extensions...)
	for i := range out {
		ext := &out[i]
		ext.Name = strings.TrimSpace(ext.Name)
		ext.Root = strings.TrimSpace(ext.Root)
		ext.Package = strings.TrimSpace(ext.Package)
		ext.Factory = strings.TrimSpace(ext.Factory)
		ext.Hash = strings.TrimSpace(ext.Hash)
		if ext.Name == "" {
			return nil, fmt.Errorf("extension[%d]: name is required", i)
		}
		if ext.Root == "" {
			return nil, fmt.Errorf("extension %q: root is required", ext.Name)
		}
		abs, err := filepath.Abs(ext.Root)
		if err != nil {
			return nil, fmt.Errorf("extension %q: resolve root: %w", ext.Name, err)
		}
		ext.Root = abs
		if ext.Package == "" {
			return nil, fmt.Errorf("extension %q: package/module is required", ext.Name)
		}
		if ext.Factory != "new_extension" {
			return nil, fmt.Errorf("extension %q: factory must be def new_extension() -> Extension", ext.Name)
		}
		if ext.Hash == "" {
			ext.Hash = "unknown"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func pythonPackedCellHash(cacheRoot, key string, extensions []PythonExtension, sdkRoot string) string {
	sdkHash := hashTree(sdkRoot)
	if strings.HasPrefix(sdkHash, "error:") {
		return sdkHash
	}
	h := sha256.New()
	_, _ = h.Write([]byte("pig-python-packed-cell\x00"))
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte("\x00"))
	hashBuildInput(h, "sdk", sdkHash)
	hashBuildInput(h, "runtime", commandVersion(cacheRoot, toolchain.PythonExecutable(runtime.GOOS, exec.LookPath), "--version"))
	hashBuildInput(h, "template", renderPythonRunner(extensions, sdkRoot))
	for _, ext := range extensions {
		for _, part := range []string{ext.Name, ext.Package, ext.Factory, ext.Hash} {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte("\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func findPythonSDKRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("PIG_SDK_PY_ROOT")); root != "" {
		if abs, err := filepath.Abs(root); err == nil && statHasFile(abs, filepath.Join("pig_sdk", "__init__.py")) {
			return abs, nil
		}
	}
	for _, root := range stagedSDKRoots("sdk-py") {
		if _, err := os.Stat(filepath.Join(root, "pig_sdk", "__init__.py")); err == nil {
			return root, nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/MichaelKinsy/PiG") {
			if cand := filepath.Join(dir, "extensions", "sdk-py"); statHasFile(cand, filepath.Join("pig_sdk", "__init__.py")) {
				return cand, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	// Staged copy under the config-root data dir (~/.pig/source) or
	// PIG_SOURCE_ROOT, written by Stock Pig's SDK staging step. This is what
	// lets an installed pig resolve the Python SDK on a clean host with no
	// checkout and no env config.
	for _, root := range installedPigSourceRoots() {
		cand := filepath.Join(root, "extensions", "sdk-py")
		if _, err := os.Stat(filepath.Join(cand, "pig_sdk", "__init__.py")); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("cannot locate github.com/MichaelKinsy/PiG checkout for Python SDK; set PIG_SDK_PY_ROOT")
}

func renderPythonRunner(extensions []PythonExtension, sdkRoot string) string {
	var b strings.Builder
	b.WriteString("import builtins\nimport importlib.machinery\nimport importlib.util\nimport hashlib\nimport os\nimport sys\nimport threading\nimport time\n\n")
	b.WriteString("sys.dont_write_bytecode = True\nos.environ.setdefault('PYTHONDONTWRITEBYTECODE', '1')\n\n")
	fmt.Fprintf(&b, "sys.path.insert(0, %q)\n", filepath.ToSlash(sdkRoot))
	for _, ext := range extensions {
		fmt.Fprintf(&b, "sys.path.insert(0, %q)\n", filepath.ToSlash(ext.Root))
	}
	b.WriteString("\nITEMS = [\n")
	for _, ext := range extensions {
		fmt.Fprintf(&b, "    (%q, %q, %q, %q, %q),\n", ext.Name, SocketEnvName(ext.Name), ext.Package, ext.Factory, filepath.ToSlash(ext.Root))
	}
	b.WriteString("]\n\n")
	fmt.Fprintf(&b, "_KEPT = [os.path.normcase(os.path.join(os.path.abspath(p), '')) for p in (%q, sys.prefix, sys.exec_prefix, sys.base_prefix, sys.base_exec_prefix, os.path.dirname(os.path.abspath(__file__)))]\n", filepath.ToSlash(sdkRoot))
	b.WriteString(pythonRunnerModuleCache)
	b.WriteString("def _run(item, sock):\n")
	b.WriteString("    _factory(item).run_with_socket(sock)\n\n")
	b.WriteString(pythonRunnerFailureReport)
	b.WriteString(pythonRunnerMain)
	return b.String()
}

// pythonRunnerMain starts each member's factory, then admits a further generation of a member's factory for every "admit" line on stdin: Pi re-invokes an extension's factory in the process that already holds its module state. The runner exits once every generation has ended, unless the host parked it for a replacement Session. A "park" line names a socket the runner connects to once it holds the process open, so the host retires its last generation only after the notice took effect.
const pythonRunnerMain = `def _unescape(field):
    # The host percent-encodes the four characters that delimit a control line (packed_go.go encodeAdmissionField); the percent sign decodes last.
    return field.replace('%09', '\t').replace('%0A', '\n').replace('%0D', '\r').replace('%25', '%')

def main():
    errors = []
    cond = threading.Condition()
    state = {'live': 0, 'parked': False, 'closed': False}
    active_value = os.environ.get('PIG_EXT_ACTIVE_MEMBERS')
    active = set(active_value.split(',')) if active_value else None

    def target(item, sock):
        try:
            if not sock:
                raise RuntimeError(f'{item[1]} not set for {item[0]}')
            _run(item, sock)
        except BaseException as exc:
            errors.append(f'{item[0]}: {exc}')
            _report_member_failure(item, exc, sock)
        finally:
            with cond:
                state['live'] -= 1
                cond.notify_all()

    def start(item, sock):
        with cond:
            state['live'] += 1
        threading.Thread(target=target, args=(item, sock)).start()

    for item in ITEMS:
        if active is not None and item[0] not in active:
            continue
        start(item, os.environ.get(item[1]) or (os.environ.get('PIG_EXT_SOCKET') if len(ITEMS) == 1 else None))

    def control():
        for line in sys.stdin:
            fields = [_unescape(field) for field in line.rstrip('\r\n').split('\t')]
            if fields[0] == 'park':
                with cond:
                    state['parked'] = True
                    cond.notify_all()
                if len(fields) > 2 and fields[2]:
                    from pig_sdk import _connect_unix
                    try:
                        _connect_unix(fields[2]).close()
                    except OSError:
                        pass
            elif fields[0] == 'admit' and len(fields) >= 3:
                _reload_edited(fields[5] if len(fields) > 5 else '0')
                for item in ITEMS:
                    if item[0] == fields[1]:
                        with cond:
                            state['parked'] = False
                        start(item, fields[2])
        with cond:
            state['closed'] = True
            cond.notify_all()

    threading.Thread(target=control, daemon=True).start()
    with cond:
        while state['live'] > 0 or (state['parked'] and not state['closed']):
            cond.wait()
    if errors:
        print('\n'.join(errors), file=sys.stderr)
        raise SystemExit(1)

if __name__ == '__main__':
    main()
`

// pythonRunnerModuleCache keeps each extension's modules for the life of the process, as Pi keeps an .mjs module, so module state survives /reload and Session replacement. An extension uses each module its factory call imports, also a module that another extension imported before, each module that a used module imports, and the packages of a used submodule. The runner sees an import of a module already imported because it wraps builtins.__import__ and importlib.import_module. A module that no factory call imported belongs to every extension whose root is the deepest extension root that holds it. The first admission of a reload pass the runner has not seen finds each module changed since its import, reloads every extension that uses one, drops each module whose loaded users all reload, and leaves every other module in place, so each reloading extension's next factory call imports the edited source, as Pi's jiti import re-evaluates an edited .ts extension. Every admission of one reload carries the same pass. A decision that fails is reported on stderr and keeps every module. The SDK, the Python installation, the runner's own directory and the site-packages and dist-packages directories under an extension's directory stay imported, so an extension in the home directory reloads only its own source.
const pythonRunnerModuleCache = `_MODULES = {}
_STAMPS = {}
_CLAIMS = {}
_DEPENDS = {}
_LOADED = {}
_RELOAD = {'pass': '0'}
_ROOT = {item[0]: os.path.normcase(os.path.join(os.path.abspath(item[4]), '')) for item in ITEMS}
_ROOTS = sorted(set(_ROOT.values()), key=len, reverse=True)

class _Window(threading.local):
    # The modules the current thread's factory call imports first and every module it imports; None outside a factory call.
    first = None
    used = None

_WINDOW = _Window()
_BUILTIN_IMPORT = builtins.__import__
_IMPORT_MODULE = importlib.import_module

_SEEN = {}

class _Claims:
    # Records each module the current thread's factory call imports first and the stamp of its source before the import system reads it, keyed by the normalized path _source_of reports, so an edit that lands before the factory call returns is not mistaken for the source that was imported. It never finds a module itself.
    @staticmethod
    def find_spec(fullname, path=None, target=None):
        first = _WINDOW.first
        if first is not None:
            first.add(fullname)
            try:
                spec = importlib.machinery.PathFinder.find_spec(fullname, path)
                if spec is not None and spec.has_location and spec.origin:
                    _SEEN[fullname] = (os.path.normcase(os.path.abspath(spec.origin)), _stamp_of(spec.origin))
            except Exception:
                _SEEN.pop(fullname, None)
        return None

def _record(importer, name, module, fromlist, returned):
    # Records the absolute names of the modules one import used, for the current thread's factory call and for the importing module. When returned is set, the import is recorded by the name of the module it returned, which resolves a relative name as the import system did. A failure to record never fails the import.
    try:
        if returned:
            name = object.__getattribute__(module, '__dict__')['__name__']
        names = {name}
        for item in fromlist or ():
            if item == '*':
                for public in getattr(sys.modules.get(name), '__all__', ()):
                    if isinstance(public, str) and name + '.' + public in sys.modules:
                        names.add(name + '.' + public)
            elif isinstance(item, str) and name + '.' + item in sys.modules:
                names.add(name + '.' + item)
        used = _WINDOW.used
        if used is not None:
            used.update(names)
        owner = importer.get('__name__') if importer is not None else None
        if owner is not None:
            depends = _DEPENDS.get(owner)
            if depends is None:
                _DEPENDS[owner] = names
            elif not names <= depends:
                depends.update(names)
    except Exception:
        pass

def _import(name, globals=None, locals=None, fromlist=(), level=0):
    module = _BUILTIN_IMPORT(name, globals, locals, fromlist, level)
    _record(globals, name, module, fromlist, level > 0)
    return module

def _import_module(name, package=None):
    module = _IMPORT_MODULE(name, package)
    try:
        importer = sys._getframe(1).f_globals
    except Exception:
        importer = None
    _record(importer, name, module, (), True)
    return module

sys.meta_path.insert(0, _Claims)
builtins.__import__ = _import
importlib.import_module = _import_module

def _module(module_name, root):
    # An imported module stays until _reload_edited drops it, so module state survives each factory call that reuses it.
    key = '_pig_cell_' + hashlib.sha256((root + ':' + module_name).encode()).hexdigest()
    mod = _MODULES.get(key)
    if mod is not None:
        return mod
    path = os.path.join(root, *module_name.split('.'))
    path = os.path.join(path, '__init__.py') if os.path.isdir(path) else path + '.py'
    spec = importlib.util.spec_from_file_location(key, path, submodule_search_locations=[os.path.dirname(path)])
    mod = importlib.util.module_from_spec(spec)
    sys.modules[key] = mod
    first = _WINDOW.first
    if first is not None:
        first.add(key)
        _SEEN[key] = (os.path.normcase(os.path.abspath(path)), _stamp_of(path))
    spec.loader.exec_module(mod)
    _MODULES[key] = mod
    return mod

def _stamp_of(path):
    # The digest finds a rewrite of the same size inside the file system's timestamp tick, which (modification time, size) cannot tell from no edit.
    try:
        info = os.stat(path)
        with open(path, 'rb') as source:
            digest = hashlib.blake2b(source.read()).digest()
        return (info.st_mtime_ns, info.st_size, digest)
    except OSError:
        return None

def _source_of(mod):
    # Reads the module's own dictionary, because reading an attribute loads a lazily loaded module. A module still executing its import is skipped: dropping it would fail that import.
    try:
        attrs = object.__getattribute__(mod, '__dict__')
        path = attrs.get('__file__')
        if not isinstance(path, str) or getattr(attrs.get('__spec__'), '_initializing', False):
            return None
        return os.path.normcase(os.path.abspath(path))
    except Exception:
        return None

def _own_source(path, prefix):
    for kept in _KEPT:
        if path.startswith(kept) and not prefix.startswith(kept):
            return False
    parts = path[len(prefix):].split(os.sep)
    return 'site-packages' not in parts and 'dist-packages' not in parts

def _root_of(path):
    # The deepest extension root that holds path, when path is that root's own source.
    for prefix in _ROOTS:
        if path.startswith(prefix):
            return prefix if _own_source(path, prefix) else None
    return None

def _begin(name):
    _LOADED.setdefault(name, time.time_ns())

def _claim(name, first, used):
    # Only a module the factory call imported first is stamped: a module imported before may have changed since its import.
    for module in first | used:
        path = _source_of(sys.modules.get(module))
        if path and _root_of(path):
            _CLAIMS.setdefault(module, set()).add(name)
            if module in first:
                seen = _SEEN.pop(module, None)
                _STAMPS[module] = seen[1] if seen is not None and seen[0] == path else _stamp_of(path)

def _factory(item):
    name, env, module_name, factory_name, root = item
    _begin(name)
    _WINDOW.first, _WINDOW.used = set(), set()
    try:
        return getattr(_module(module_name, root), factory_name)()
    finally:
        first, used = _WINDOW.first, _WINDOW.used
        _WINDOW.first = _WINDOW.used = None
        _claim(name, first, used)

def _owners():
    # Maps each extension-owned module to its source and users: the extensions whose factory calls imported it, or else every extension whose root is the deepest that holds it. The users of a module also use each module it imports, and the users of a submodule use its packages.
    owners = {}
    for module, mod in list(sys.modules.items()):
        path = _source_of(mod)
        prefix = _root_of(path) if path else None
        if prefix:
            claims = _CLAIMS.get(module)
            owners[module] = (path, set(claims) if claims is not None else {name for name, root in _ROOT.items() if root == prefix})
    pending = list(owners)
    while pending:
        module = pending.pop()
        names = owners[module][1]
        parts = module.split('.')
        for dependency in (*_DEPENDS.get(module, ()), *('.'.join(parts[:i]) for i in range(1, len(parts)))):
            entry = owners.get(dependency)
            if entry is not None and not names <= entry[1]:
                entry[1].update(names)
                pending.append(dependency)
    return owners

def _edited(module, path, since):
    stamp = _stamp_of(path)
    if module in _STAMPS:
        return stamp != _STAMPS[module]
    # A module no factory call imported first has no stamp; its source changed if the file is newer than the first factory call of an extension that uses it. A pass that finds it unchanged records its stamp, so a later pass also finds a same-size rewrite inside the timestamp tick by its content.
    if stamp is None or stamp[0] > since:
        return True
    _STAMPS[module] = stamp
    return False

def _reload_edited(reload):
    if reload == '0' or reload == _RELOAD['pass']:
        return
    _RELOAD['pass'] = reload
    try:
        owners = _owners()
        reloading = set()
        for module, (path, names) in owners.items():
            loaded = {name for name in names if name in _LOADED}
            if loaded - reloading and _edited(module, path, min(_LOADED[name] for name in loaded)):
                reloading |= loaded
        importlib.invalidate_caches()
        for module, (path, names) in owners.items():
            loaded = {name for name in names if name in _LOADED}
            if loaded and loaded <= reloading:
                sys.modules.pop(module, None)
                _MODULES.pop(module, None)
                _STAMPS.pop(module, None)
                _CLAIMS.pop(module, None)
                _DEPENDS.pop(module, None)
        # A reloading extension's next factory call records the modules it still uses.
        for claims in list(_CLAIMS.values()):
            claims -= reloading
        for name in reloading:
            _LOADED.pop(name, None)
    except Exception as exc:
        print(f'reload: cannot re-import edited Python extensions: {exc!r}', file=sys.stderr, flush=True)

`

// pythonRunnerFailureReport tells the host that one member failed while the
// shared process lives on: it prints the error and connects to the member's
// socket and closes it, so the host's wait for that member ends with a
// visible load error instead of waiting for a connection that never comes.
// It connects as the SDK does: CPython on Windows has no socket.AF_UNIX, and
// the SDK reaches the host's AF_UNIX socket through Winsock there.
const pythonRunnerFailureReport = `def _report_member_failure(item, exc, sock):
    print(f'{item[0]}: {exc}', file=sys.stderr, flush=True)
    if not sock:
        return
    from pig_sdk import _connect_unix
    try:
        _connect_unix(sock).close()
    except OSError:
        pass

`

// CurrentPythonPackedCellEntry returns the valid cache entry selected by the
// same normalization and hash inputs as BuildPythonPackedCell, without building it.
func CurrentPythonPackedCellEntry(cacheRoot, key string, extensions []PythonExtension) (string, bool, error) {
	normalized, err := normalizePythonExtensions(extensions)
	if err != nil {
		return "", false, err
	}
	lockCandidates := append([]string{os.Getenv("PIG_SDK_PY_ROOT")}, stagedSDKRoots("sdk-py")...)
	return pigsdklock.WithBuildEntryCandidates(context.Background(), lockCandidates, func() (string, bool, error) {
		sdkRoot, err := findPythonSDKRoot()
		if err != nil {
			return "", false, err
		}
		return pigsdklock.WithBuildEntryForRoot(context.Background(), sdkRoot, func() (string, bool, error) {
			hash := pythonPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return "", false, fmt.Errorf("hash Python packed cell: %s", after)
			}
			entry := filepath.Join(cacheRoot, "cells", "python", hash)
			_, valid := validCellEntry(entry, EntryIdentity{InputDigest: hash, Artifact: "runner.py", Language: "python"})
			return entry, valid, nil
		})
	})
}
