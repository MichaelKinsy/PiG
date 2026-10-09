#!/usr/bin/env python3
"""Breakdown of ledger rows closed since a baseline commit, by how they were closed. Deterministic: ledger data + git history only.

usage: breakdown.py BASE_COMMIT [HEAD_REF] [OUT_DIR]   (run from the repository root; needs `go`)

Inputs : the baseline mapping file at BASE_COMMIT (test/parity/interfaces/mapping-v1.0.4.json), the current mapping-v<UpstreamVersion>.json,
         every mapping revision between them (git history), the Go symbols at BASE_COMMIT, and the Go test ASTs at HEAD (testrefs helper).
Classes: a row is "closed since base" when it is ported/designed-out now and was not closed (or absent) at BASE_COMMIT. Exclusive, first match wins:
  4 designed-out : disposition designed-out now.
  5 retarget     : the row id is new in the current version or its upstream shape hash changed since base (Pi 1.0.x -> 1.1.0 row changes).
  2 marker       : every evidence test of the row carries a `// pi:` marker comment (a marker on an existing test).
  1 hand port    : the row was first closed by a hand-written rationale (reviewed hand row), OR its first Go target symbol did not exist at
                   BASE_COMMIT (a port landed since base) and the row has an evidence test. The script cannot prove a mutation check per row;
                   the lane rules require one, so class 1 is "hand port, mutation check required by lane rules, not machine-proven".
  3 tool proof   : everything else: closed by the detector's derivation (shape match, name rule, generated constructor row) on a Go symbol
                   that already existed at BASE_COMMIT.
Behaviour check (class 3 only): a Go test function that references the row's symbol (member name, else top-level name) directly or through a
same-package helper (depth 3) AND asserts (t.Error*/Fatal*/Fail*, assert./require.) in the target's package or in an evidence test's package.
Rows with no such test are listed in no-behaviour-test.tsv (lower bound: a bare member name shared with another type counts as a touch).
"""
import json, os, re, subprocess, sys, collections

BASE = sys.argv[1]
HEAD = sys.argv[2] if len(sys.argv) > 2 else 'HEAD'
OUT = sys.argv[3] if len(sys.argv) > 3 else 'build/ledger-breakdown'
HERE = os.path.dirname(os.path.abspath(__file__))
os.makedirs(OUT, exist_ok=True)


def sh(*a, **k):
    return subprocess.check_output(a, text=True, **k)


HEADFILE = [f for f in os.listdir('test/parity/interfaces') if re.fullmatch(r'mapping-v\d+\.\d+\.\d+\.json', f)]
HEADFILE = sorted(HEADFILE, key=lambda s: tuple(int(x) for x in re.findall(r'\d+', s)))[-1]
BASEFILE = 'mapping-v1.0.4.json'
H = {x['id']: x for x in json.load(open('test/parity/interfaces/' + HEADFILE))['mappings']}
B = {x['id']: x for x in json.loads(sh('git', 'show', f'{BASE}:test/parity/interfaces/{BASEFILE}'))['mappings']}
closed = lambda x: x['disposition'] in ('ported', 'designed-out')

# first-closure history
first_path = os.path.join(OUT, 'first-closure.json')
if not os.path.exists(first_path):
    subprocess.check_call([sys.executable, os.path.join(HERE, 'closure_history.py'), BASE, HEAD, first_path])
first = json.load(open(first_path))

# Go test references at HEAD
refs_path = os.path.join(OUT, 'test-refs.json')
if not os.path.exists(refs_path):
    exe = os.path.join(OUT, 'testrefs')
    subprocess.check_call(['go', 'build', '-o', exe, './test/parity/interface-closure/breakdown/testrefs'])
    with open(refs_path, 'w') as fh:
        subprocess.check_call([exe, '.'], stdout=fh)
refs = json.load(open(refs_path))

# marker tests: `// pi:` comments inside a test function
markers = set()
for dirpath, dns, fns in os.walk('.'):
    dns[:] = [d for d in dns if d not in ('.git', '.upstream', 'node_modules', 'vendor', 'build')]
    for fn in fns:
        if fn.endswith('_test.go'):
            p = os.path.join(dirpath, fn)
            txt = open(p, errors='replace').read()
            if '// pi:' not in txt:
                continue
            for m in re.finditer(r'^func (Test\w+)\(', txt, re.M):
                body_end = txt.find('\nfunc ', m.end())
                body = txt[m.end(): body_end if body_end > 0 else len(txt)]
                if '// pi:' in body:
                    markers.add(os.path.relpath(p, '.') + '#' + m.group(1))

basefile_cache = {}


def base_text(path):
    if path not in basefile_cache:
        try:
            basefile_cache[path] = sh('git', 'show', f'{BASE}:{path}', stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError:
            basefile_cache[path] = None
    return basefile_cache[path]


def existed_at_base(target):
    path, _, sym = target.partition('#')
    txt = base_text(path)
    if txt is None or not sym:
        return False
    top, _, member = sym.partition('.')
    top_re = rf'(^|\n)\s*(type|func|var|const)\s+{re.escape(top)}\b|func\s*\([^)]*\)\s*{re.escape(top)}\b|(^|\n)\s+{re.escape(top)}\s'
    if not re.search(top_re, txt):
        return False
    return bool(re.search(rf'\b{re.escape(member)}\b', txt)) if member else True


def idents(target):
    sym = target.partition('#')[2]
    top, _, member = sym.partition('.')
    return top, member


def behaviour(row):
    """'tested' | 'shape-only' | 'none', plus 'strict' flag (one test reaches the type and the member)."""
    tgts = row.get('pigTargets') or []
    if not tgts:
        return 'none', False
    top, member = idents(tgts[0])
    dirs = {os.path.dirname(tgts[0].partition('#')[0])}
    for e in row.get('evidence') or []:
        if e.startswith('test:'):
            dirs.add(os.path.dirname(e[5:].partition('#')[0]))
    want = member or top
    best, strict = 'none', False
    for d in dirs:
        r = refs.get(d or '.', {})
        hits = r.get(want, [])
        if hits:
            if any(h.endswith('|a') for h in hits):
                best = 'tested'
                if not member or (set(h for h in hits if h.endswith('|a')) & set(r.get(top, []))):
                    strict = True
            elif best == 'none':
                best = 'shape-only'
    return best, strict


def pkg(i):
    m = re.match(r'pkg:([^/]+)/', i)
    return m.group(1) if m else ('cli' if i.startswith('cli') else '?')


new = [i for i in H if closed(H[i]) and not (i in B and closed(B[i]))]
carried = [i for i in B if closed(B[i]) and i in H and closed(H[i])]
lost = [i for i in B if closed(B[i]) and not (i in H and closed(H[i]))]
CLASSES = {1: 'hand port', 2: 'marker', 3: 'tool proof', 4: 'designed out', 5: 'retarget'}
cls = {}
sub = collections.Counter()
for i in new:
    r = H[i]
    f = first.get(i, {})
    derived = f.get('rat', r.get('rationale', '')).startswith('Derived by the interface gap detector')
    ev = [e for e in (r.get('evidence') or []) if e.startswith('test:')]
    tgt = (r.get('pigTargets') or [''])[0]
    if r['disposition'] == 'designed-out':
        c = 4
    elif i not in B or B[i]['upstreamShapeHash'] != r['upstreamShapeHash']:
        c = 5
    elif ev and all(e[5:] in markers for e in ev):
        c = 2
    elif not derived:
        c = 1
        sub[(1, 'reviewed hand row')] += 1
    elif tgt and ev and not existed_at_base(tgt):
        c = 1
        sub[(1, 'Go symbol added since base')] += 1
    else:
        c = 3
        sub[(3, 'construct:0 row' if i.endswith('::construct:0') else 'member/call/type row')] += 1
    cls[i] = c

per = collections.defaultdict(collections.Counter)
for i, c in cls.items():
    per[pkg(i)][c] += 1
tot = collections.Counter(cls.values())

flag = []
weak = []
beh = collections.Counter()
for i, c in cls.items():
    if c == 3:
        b, strict = behaviour(H[i])
        beh[(b, strict)] += 1
        if b == 'tested' and not strict:
            weak.append((i, pkg(i), (H[i].get('pigTargets') or [''])[0]))
        if b != 'tested':
            flag.append((i, pkg(i), b, (H[i].get('pigTargets') or [''])[0], ';'.join(e for e in (H[i].get('evidence') or []))[:160]))
flag.sort()
with open(os.path.join(OUT, 'no-behaviour-test.tsv'), 'w') as fh:
    fh.write('id\tpackage\tstate\tgo target\tevidence\n')
    for r in flag:
        fh.write('\t'.join(r) + '\n')

with open(os.path.join(OUT, 'name-only-behaviour-test.tsv'), 'w') as fh:
    fh.write('id\tpackage\tgo target\n')
    for r in sorted(weak):
        fh.write('\t'.join(r) + '\n')

lines = [f'# Ledger breakdown since {BASE[:10]}', '',
         f'Baseline: `{BASE[:10]}` (`mapping-v1.0.4.json`: {len(B)} rows, {sum(1 for x in B.values() if x["disposition"] == "ported")} ported + {sum(1 for x in B.values() if x["disposition"] == "designed-out")} designed out). Head: `{HEAD}` (`{HEADFILE}`: {len(H)} rows).',
         f'Closed now: {sum(1 for x in H.values() if closed(x))} ({sum(1 for x in H.values() if x["disposition"] == "ported")} ported, {sum(1 for x in H.values() if x["disposition"] == "designed-out")} designed out), pending {sum(1 for x in H.values() if not closed(x))}.',
         f'Closed at baseline and still closed: {len(carried)}; closed at baseline, not closed now: {len(lost)}; rows added by the version leap: {len(set(H) - set(B))}; rows removed: {len(set(B) - set(H))}.',
         f'**Closed since baseline: {len(new)}.** Generated by `test/parity/interface-closure/breakdown/breakdown.py`; rules are in its docstring.', '',
         '| class | rows | share |', '|---|---:|---:|']
for c in (1, 2, 3, 4, 5):
    lines.append(f'| {c} {CLASSES[c]} | {tot[c]} | {100 * tot[c] / max(1, len(new)):.1f}% |')
lines += [f'| total | {len(new)} | 100% |', '', 'Sub-splits: ' + '; '.join(f'{CLASSES[k[0]]} / {k[1]}: {v}' for k, v in sorted(sub.items())), '',
          '## Per package', '', '| package | 1 hand port | 2 marker | 3 tool proof | 4 designed out | 5 retarget | total |', '|---|---:|---:|---:|---:|---:|---:|']
for p in sorted(per):
    c = per[p]
    lines.append(f'| {p} | {c[1]} | {c[2]} | {c[3]} | {c[4]} | {c[5]} | {sum(c.values())} |')
lines += ['', '## Class 3 behaviour check', '',
          f'Tool-proof rows: {tot[3]}. With an asserting Go test referencing the symbol: {beh[("tested", True)] + beh[("tested", False)]} (one test reaches both type and member: {beh[("tested", True)]}; member/name only: {beh[("tested", False)]}).',
          f'**No behaviour test touching the symbol: {len(flag)}** (referenced only by non-asserting tests: {beh[("shape-only", False)] + beh[("shape-only", True)]}; referenced by no Go test: {beh[("none", False)]}). List: `no-behaviour-test.tsv` ({len(flag)} rows). Weaker tier (an asserting test uses the member name but no test reaches the type too): `name-only-behaviour-test.tsv` ({len(weak)} rows).', '',
          '### No-behaviour-test rows per package', '', '| package | rows |', '|---|---:|']
fp = collections.Counter(r[1] for r in flag)
for p in sorted(fp):
    lines.append(f'| {p} | {fp[p]} |')
open(os.path.join(OUT, 'ledger-breakdown.md'), 'w').write('\n'.join(lines) + '\n')
print('\n'.join(lines[:14]))
