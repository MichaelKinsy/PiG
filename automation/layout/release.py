#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Apply the release root cleanup after the public-library section."""
import argparse
import json
from pathlib import Path, PurePosixPath
import posixpath
import re
import subprocess
import tomllib

import plan
import build

HERE = Path(__file__).resolve().parent
DIRS = {'tests': 'test', 'parity': 'test/parity', 'evals': 'test/evals', 'media': 'docs/media'}
FILES = {name: 'docs/parity/' + name for name in ('DIVERGENCES.md', 'PORT_MAP.md', 'DIVERGENCE-IDS.txt')}
MOVES = DIRS | FILES
MODULE = 'github.com/MichaelKinsy/PiG/'
ROOT_ENTRIES = {
    'README.md', 'LICENSE', 'LICENSES', 'NOTICE', 'THIRD_PARTY_NOTICES.md',
    'REUSE.toml', 'CITATION.cff', 'CHANGELOG.md', 'AGENTS.md', 'Makefile',
    'go.mod', 'go.sum', 'go.work', 'go.work.sum', 'embed.go',
    'agent', 'ai', 'coding', 'tui', 'cmd', 'internal', 'extensions', 'piglets',
    'examples', 'docs', 'automation', 'test', 'changelog.d', 'telemetry',
}
IMPORT = re.compile(re.escape(MODULE) + r'((?:tests|parity|evals)(?:/[\w.-]+)*)(?=$|[/#.\s"`])')
TOKEN = re.compile(build.PATH_START + r'(?P<path>(?:tests|parity|evals|media)(?:/[^\s`"\'()<>\[\],;|#?:{}]*|(?(prefix)(?=[\s`"\'),;:]|$)|(?!)))|(?:DIVERGENCES\.md|PORT_MAP\.md|DIVERGENCE-IDS\.txt))')
QUOTED_RELATIVE = re.compile(r'(?P<quote>["\'`])(?P<path>(?:\.\./|\./)[^\s"\'`<>]*)(?P=quote)')
DESTINATION = re.compile(r'(?P<lead>\]\(<?|^\s*\[[^\]\n]+\]:\s*<?|(?:href|src)=["\'])(?P<path>[^\s<>"\')]+)', re.MULTILINE)
GENERATED = re.compile(r'parity/interfaces/(?:pig-go\.json|(?:recommendations|upstream|upstream-tests|behavior-inputs|delta|custom-factory-call-surface-draft)-v[^/]+\.json)$')
SUFFIXES = {'.go','.md','.toml','.json','.jsonl','.yaml','.yml','.py','.sh','.mjs','.js','.ts','.tsx','.html','.css','.txt','.tsv','.mod','.sum','.work','.svg','.mmd','.jsonld','.rs','.cfg','.in','.lock','.mk','.cjs'}


def git(root, *args):
    return subprocess.check_output(['git','-C',str(root),*args])


def moved(path):
    return plan.move_path(path, MOVES)


def original(path):
    reverse = {new:old for old,new in sorted(MOVES.items(), key=lambda item:len(item[1]), reverse=True)}
    return plan.move_path(path, reverse)


def state(root):
    old = [(root/name).exists() for name in MOVES]
    if all(old):
        return 'original'
    if not any(old) and all((root/name).exists() for name in MOVES.values()):
        return 'release'
    raise ValueError('root cleanup requires either the complete original or complete release layout')


def regular(root, path):
    for parent in [root/path, *(root/path).parents]:
        if parent == root:
            break
        if parent.is_symlink():
            raise ValueError(f'tracked path contains a symlink: {path}')


class Rewriter:
    def __init__(self, root, tracked, relocating):
        self.root = root
        self.relocating = relocating
        self.known_paths = set(tracked)
        for name in tracked:
            self.known_paths.update(str(p) for p in PurePosixPath(name).parents)

    def known(self, path):
        return path in self.known_paths or path.startswith(('.upstream/', 'extensions/sdk-ts/node_modules/'))

    def relative(self, value, source):
        if not self.relocating:
            return value
        path, hashmark, fragment = value.partition('#')
        path, querymark, query = path.partition('?')
        resolved = posixpath.normpath(posixpath.join(str(PurePosixPath(source).parent), path))
        if not self.known(resolved):
            return value
        dest = moved(source)
        result = posixpath.relpath(moved(resolved), str(PurePosixPath(dest).parent))
        if path.endswith('/') and not result.endswith('/'):
            result += '/'
        if value.startswith('./') and not result.startswith('.'):
            result = './' + result
        return result + querymark + query + hashmark + fragment

    def references(self, text):
        text = IMPORT.sub(lambda m: MODULE + moved(m[1]), text)
        text = re.sub(r'(https://(?:github\.com/MichaelKinsy/PiG/(?:blob|tree)|raw\.githubusercontent\.com/MichaelKinsy/PiG)/(?:main|master|HEAD)/)([^\s\"\'`<>#)]+)', lambda m:m[1]+moved(m[2]), text)
        return TOKEN.sub(lambda m: (m['prefix'] or '') + moved(m['path']), text)

    def text(self, text, source):
        protected = []
        def hold(value):
            protected.append(value)
            return f'\x00release-{len(protected)-1}\x00'
        if '\x00release-' in text:
            raise ValueError(f'reserved rewrite marker in text: {source}')
        if source.endswith('.md'):
            def link(match):
                target = match['path']
                if re.match(r'(?:\w+:|/|#)',target):
                    return match[0]
                return match['lead'] + hold(self.relative(target, source))
            text = DESTINATION.sub(link, text)
        def quoted(match):
            value = self.relative(match['path'], source)
            if value == match['path']:
                value = self.references(value)
            return match['quote'] + hold(value) + match['quote']
        text = QUOTED_RELATIVE.sub(quoted, text)
        text = self.references(text)
        # A basename classifier keeps the filename even when the ledger's repository path moves.
        text = re.sub(r'(\.name\s*(?:==|!=)\s*["\'])docs/parity/(DIVERGENCES\.md|PORT_MAP\.md|DIVERGENCE-IDS\.txt)(["\'])', r'\1\2\3', text)
        for i in reversed(range(len(protected))):
            text = text.replace(f'\x00release-{i}\x00', protected[i])
        if self.relocating:
            depth = len(PurePosixPath(source).parent.parts)
            delta = len(PurePosixPath(moved(source)).parent.parts) - depth
            if delta:
                # Script-local paths use the same directory-to-root relationship as Go callers.
                text = re.sub(r'((?:pathlib\.)?Path\(__file__\)(?:\.resolve\(\))?\.parents\[)' + str(depth) + r'\]', lambda m:m[1]+str(depth+delta)+']', text)
                text = text.replace(')/' + '/'.join(['..']*depth) + '"', ')/' + '/'.join(['..']*(depth+delta)) + '"') if source.endswith('.sh') else text
        # Root-bound Python Path operands are distinct from upstream test-directory names.
        for old,new in MOVES.items():
            text = re.sub(r'(\b(?:root|ROOT|REPO|repo|repo_root|repository|HERE\.parents\[\d+\]|Path\(args\.root\)|port_map\.parent)\s*/\s*)["\']' + re.escape(old) + r'["\']', lambda m:m[1]+' / '.join(json.dumps(p) for p in new.split('/')), text)
        text = structural(text, original(source))
        return text

    def go(self, source, data, scanned):
        edits=[]
        for item in (scanned['Literals'] or []) + (scanned['Comments'] or []):
            value=item['Value']
            changed = self.references(value) if not item.get('Import') else IMPORT.sub(lambda m:MODULE+moved(m[1]),value)
            if not item.get('Import') and ('../' in value or './' in value):
                changed = QUOTED_RELATIVE.sub(lambda m:m['quote']+self.relative(m['path'],source)+m['quote'], changed)
            if value.startswith(('../','./')) and not item.get('Import'):
                changed=self.relative(value,source)
                if changed==value:
                    changed=self.references(changed)
            if value in {"/" + name + suffix for name in DIRS for suffix in ('', '/')}:
                changed = value
            if item.get('PathRoot') and value in DIRS:
                changed=DIRS[value]
            if value != changed:
                raw=data[item['Start']:item['End']]
                if raw.startswith(b'//') or raw.startswith(b'/*'):
                    replacement=changed.encode()
                elif raw.startswith(b'`') and '`' not in changed:
                    replacement=('`'+changed+'`').encode()
                else:
                    replacement=json.dumps(changed,ensure_ascii=False).encode()
                edits.append((item['Start'],item['End'],replacement))
        for start,end,replacement in sorted(edits,reverse=True):
            data=data[:start]+replacement+data[end:]
        text=data.decode()
        depth=len(PurePosixPath(source).parent.parts)
        delta=len(PurePosixPath(moved(source)).parent.parts)-depth if self.relocating else 0
        if delta:
            parents=', '.join(['".."']*depth)
            newparents=', '.join(['".."']*(depth+delta))
            text=re.sub(r'(filepath\.Join\()'+re.escape(parents)+r'(?=,|\))',lambda m:m[1]+newparents,text)
            text=text.replace('filepath.Abs("'+ '/'.join(['..']*depth) +'")','filepath.Abs("'+ '/'.join(['..']*(depth+delta)) +'")')
        return structural(text, original(source)).encode()


def owned(name):
    if name.startswith(('automation/layout/','extensions/sdk/')):
        return False
    if '/runtime-node/shims/' in name and name.rsplit('/', 1)[-1] != 'pi-ai-bridge.mjs':
        return False
    logical=original(name)
    if GENERATED.fullmatch(logical) or logical in ('parity/coverage.md','parity/normalization-inventory.json'):
        return False
    return Path(name).suffix in SUFFIXES or '/' not in name or name.startswith('.github/')


# These consumers derive a repository root or a family from the old physical layout.
STRUCTURAL = {
    'automation/release/set-version/main_test.go': [('{"add", "--", "coding", "piglets"', '{"add", "--", "internal", "piglets"')],
    'evals/pigeval/registry.py': [('REPO_ROOT = os.path.dirname(EVALS_DIR)', 'REPO_ROOT = os.path.dirname(os.path.dirname(EVALS_DIR))')],
    'docs/project/repository-quality.md': [('Keep `AGENTS.md`, `docs/parity/PORT_MAP.md`, and `docs/parity/DIVERGENCES.md` at the root as maintenance authorities.', 'Keep `AGENTS.md` at the root. Keep the parity maintenance authorities in `docs/parity/`.')],
    'automation/make/parity.mk': [
        ('--source-root ../../.upstream/current', '--source-root ../../../.upstream/current'),
        ('../../automation/ci/check-generated.sh', '../../../automation/ci/check-generated.sh'),
    ],

    'automation/ci/check-port-map-drift.py': [('port_map.parent', 'port_map.parents[2]')],
    'parity/cmd/coverage/main.go': [
        ('loadUnitEvidence(filepath.Dir(*portMap), entries)', 'loadUnitEvidence(filepath.Join(filepath.Dir(*portMap), "..", ".."), entries)'),
        ('loadTestPortingStats(filepath.Dir(*portMap))', 'loadTestPortingStats(filepath.Join(filepath.Dir(*portMap), "..", ".."))'),
    ],
    'parity/cmd/lint/main.go': [('filepath.Join(filepath.Dir(*divergencesMd), "docs", "additive-features.md")', 'filepath.Join(filepath.Dir(*divergencesMd), "..", "additive-features.md")')],
    'parity/cmd/testinventorycheck/release_policy.go': [(
        'data, err := exec.CommandContext(context.Background(), "git", "-C", root, "show", policy.BaselineCommit+":"+filepath.ToSlash(rel)).Output()',
        '''tree, err := exec.CommandContext(context.Background(), "git", "-C", root, "ls-tree", "-r", "--name-only", policy.BaselineCommit).Output()
	if err != nil {
		return fmt.Errorf("read committed test baseline %s (fetch this commit; do not lower the baseline): %w", policy.BaselineCommit, err)
	}
	var candidates []string
	for name := range strings.SplitSeq(strings.TrimSpace(string(tree)), "\\n") {
		if filepath.Base(name) == filepath.Base(rel) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) != 1 {
		return fmt.Errorf("committed test baseline %s must contain one unambiguous %s mapping; found %d", policy.BaselineCommit, filepath.Base(rel), len(candidates))
	}
	data, err := exec.CommandContext(context.Background(), "git", "-C", root, "show", policy.BaselineCommit+":"+candidates[0]).Output()'''
    )],
    'parity/cmd/testinventorycheck/release_policy_test.go': [
        ('import (\n', 'import (\n\t"os"\n'),
        ('\tpolicy.BaselinePorted = nil\n', '''	renamed := filepath.Join(root, "moved", filepath.Base(mapPath))
	if err := os.MkdirAll(filepath.Dir(renamed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mapPath, renamed); err != nil {
		t.Fatal(err)
	}
	mapPath = renamed
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err != nil {
		t.Fatalf("renamed mapping must retain the committed baseline: %v", err)
	}
	policy.BaselinePorted = nil
'''),
    ],
    'parity/closure/family_report.go': [
        ('len(parts) < 3 || parts[0] != "parity" || parts[1] != "scenarios"', 'len(parts) < 4 || parts[0] != "test" || parts[1] != "parity" || parts[2] != "scenarios"'),
        ('len(parts) == 3', 'len(parts) == 4'),
        ('parts[2] == ""', 'parts[3] == ""'),
        ('return parts[2], nil', 'return parts[3], nil'),
    ],
}
FIXTURE_ROOTS = {
    'parity/cmd/coverage/test_porting_test.go',
    'tests/upstream-parity/divergence_consistency_script_test.go',
    'tests/upstream-parity/divergence_quality_test.go',
    'automation/ci/divguard/divguard_test.go',
    'parity/cmd/interfaceinventory/main_test.go',
    'parity/cmd/portreconcile/portmap_flip_test.go',
    'parity/cmd/testinventorycheck/main_test.go',
    'parity/porter/disposition_plan_test.go',
}


def structural(text, source):
    for old,new in STRUCTURAL.get(source, ()):
        if new in text:
            continue
        if old not in text:
            raise ValueError(f'{source}: missing root-layout anchor: {old}')
        text=text.replace(old,new)
    if source in FIXTURE_ROOTS:
        text=re.sub(r'(?m)^(\t*)(root (?::=|=) t\.TempDir\(\)\n)(?!\1if err := os.MkdirAll\(filepath.Join\(root, "docs", "parity"\))',
                    r'\1\2\1if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {\n\1\tt.Fatal(err)\n\1}\n', text)
    return text


def apply(root):
    root=root.resolve()
    if Path(git(root,'rev-parse','--show-toplevel').decode().strip()).resolve()!=root:
        raise ValueError('root must be the Git checkout root')
    relocating=state(root)=='original'
    paths=git(root,'ls-files','-z').decode().rstrip('\0').split('\0')
    entries = {PurePosixPath(moved(name) if relocating else name).parts[0] for name in paths}
    unexpected = sorted(name for name in entries if not name.startswith('.') and name not in ROOT_ENTRIES)
    if unexpected:
        raise ValueError(f'unclassified release root entries: {unexpected}')
    if relocating:
        for old,new in MOVES.items():
            regular(root,old);regular(root,new)
            if (root/new).exists():
                raise ValueError(f'root-cleanup destination already exists: {new}')
    for name in paths:
        if owned(name):regular(root,name)
    go_paths=[name for name in paths if name.endswith('.go') and owned(name)]
    scanned=json.loads(subprocess.check_output(['go','run',str(HERE/'scan/main.go')],input=json.dumps(go_paths).encode(),cwd=root))
    by_name={f['Path']:f for f in scanned}
    rewriter=Rewriter(root,paths,relocating)
    changes={}
    for name in paths:
        if not owned(name):continue
        path=root/name
        before=path.read_bytes()
        try:text=before.decode()
        except UnicodeError:continue
        if '\0' in text:continue
        after=rewriter.go(name,before,by_name[name]) if name in by_name else rewriter.text(text,name).encode()
        if original(name).startswith('parity/scenarios/') and name.endswith('.toml'):
            old_contract = tomllib.loads(text)
            new_contract = tomllib.loads(after.decode())
            for key in ('assert', 'covers', 'diverge'):
                if old_contract.get(key) != new_contract.get(key):
                    raise ValueError(f'{name}: root cleanup would change scenario {key}')
        if after!=before:changes[name]=after
    for name,data in changes.items():(root/name).write_bytes(data)
    if relocating:
        for old,new in MOVES.items():
            (root/new).parent.mkdir(parents=True,exist_ok=True)
            subprocess.run(['git','-C',str(root),'mv','--',old,new],check=True)
    changed_go=[moved(name) if relocating else name for name in changes if name.endswith('.go')]
    if changed_go:subprocess.run(['gofmt','-w',*changed_go],cwd=root,check=True)
    fragments=root/'changelog.d'
    if fragments.is_dir() and not any(fragments.iterdir()):fragments.rmdir()
    if (root/'docs/knowledge-graph/pig.graph.json').is_file():
        subprocess.run(['python3','-B','automation/gen/gen-knowledge-graph.py'],cwd=root,check=True)
    print(f'layout-release: moved {sum(moved(p)!=p for p in paths) if relocating else 0} tracked files; rewrote {len(changes)} files')


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state',action='store_true')
    parser.add_argument('root',type=Path)
    args=parser.parse_args()
    if args.state:
        layout = state(args.root.resolve())
        if layout == 'release':
            for name in plan.PUBLIC_ROOTS:
                if not (args.root/name).is_dir():
                    raise ValueError(f'public library root is missing: {name}')
            for name in plan.PRIVATE_HELPERS:
                if (args.root/name).exists() or not (args.root/'internal'/name).is_dir():
                    raise ValueError(f'private helper layout is incomplete: {name}')
        print(layout)
    else:
        apply(args.root)
